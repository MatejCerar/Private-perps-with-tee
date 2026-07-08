// Package swap is a minimal Uniswap V3-style exact-in swap client for the
// stable-conversion legs of the funding path (Flare: FXRP/USDT0 → stable on
// SparkDEX, a Uniswap V3 fork; Arbitrum: USDT → native USDC on canonical
// Uniswap V3). Everything is parameterized by router/quoter/token addresses so
// testnet ↔ fork ↔ mainnet is a config swap.
//
// Two router shapes exist in the wild:
//   - RouterClassic: the original V3 SwapRouter, exactInputSingle params
//     include a deadline field. Most forks (SparkDEX included) use this shape.
//   - Router02: Uniswap's SwapRouter02, same call without the deadline field
//     (deadline moves to an outer multicall).
//
// Quotes use QuoterV2's quoteExactInputSingle via eth_call (it is technically
// state-mutating but designed to be staticcalled).
package swap

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// RouterKind selects the exactInputSingle calldata shape.
type RouterKind string

const (
	RouterClassic RouterKind = "classic" // V3 SwapRouter (with deadline) — Uniswap V3 + most forks (SparkDEX)
	Router02      RouterKind = "router02" // SwapRouter02 (no deadline)
)

// classicRouterABI: original SwapRouter.exactInputSingle (struct includes deadline).
const classicRouterABI = `[
	{"name":"exactInputSingle","type":"function","stateMutability":"payable",
		"inputs":[{"name":"params","type":"tuple","components":[
			{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},
			{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},
			{"name":"deadline","type":"uint256"},{"name":"amountIn","type":"uint256"},
			{"name":"amountOutMinimum","type":"uint256"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}],
		"outputs":[{"name":"amountOut","type":"uint256"}]}
]`

// router02ABI: SwapRouter02.exactInputSingle (no deadline in the struct).
const router02ABI = `[
	{"name":"exactInputSingle","type":"function","stateMutability":"payable",
		"inputs":[{"name":"params","type":"tuple","components":[
			{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},
			{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},
			{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"},
			{"name":"sqrtPriceLimitX96","type":"uint160"}]}],
		"outputs":[{"name":"amountOut","type":"uint256"}]}
]`

// quoterV2ABI: QuoterV2.quoteExactInputSingle. NOTE: amountIn precedes fee in
// the V2 params struct (differs from the V1 quoter's flat argument order).
const quoterV2ABI = `[
	{"name":"quoteExactInputSingle","type":"function","stateMutability":"nonpayable",
		"inputs":[{"name":"params","type":"tuple","components":[
			{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},
			{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},
			{"name":"sqrtPriceLimitX96","type":"uint160"}]}],
		"outputs":[
			{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},
			{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}]}
]`

const erc20ABI = `[
	{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"type":"function"}
]`

// classicExactInputSingleParams mirrors ISwapRouter.ExactInputSingleParams.
type classicExactInputSingleParams struct {
	TokenIn           common.Address
	TokenOut          common.Address
	Fee               *big.Int
	Recipient         common.Address
	Deadline          *big.Int
	AmountIn          *big.Int
	AmountOutMinimum  *big.Int
	SqrtPriceLimitX96 *big.Int
}

// router02ExactInputSingleParams mirrors IV3SwapRouter.ExactInputSingleParams.
type router02ExactInputSingleParams struct {
	TokenIn           common.Address
	TokenOut          common.Address
	Fee               *big.Int
	Recipient         common.Address
	AmountIn          *big.Int
	AmountOutMinimum  *big.Int
	SqrtPriceLimitX96 *big.Int
}

// quoteExactInputSingleParams mirrors IQuoterV2.QuoteExactInputSingleParams.
type quoteExactInputSingleParams struct {
	TokenIn           common.Address
	TokenOut          common.Address
	AmountIn          *big.Int
	Fee               *big.Int
	SqrtPriceLimitX96 *big.Int
}

// Client wraps one chain's RPC connection plus the parsed ABIs.
type Client struct {
	eth     *ethclient.Client
	chainID *big.Int
	classic abi.ABI
	r02     abi.ABI
	quoter  abi.ABI
	erc20   abi.ABI

	// sendMu serializes sendTx per client (same single-key nonce-race rationale
	// as arb.Client / stargate.Client).
	sendMu sync.Mutex
}

// New dials the given RPC URL and reads the chain ID.
func New(ctx context.Context, rpcURL string) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}
	chainID, err := eth.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read chain id: %w", err)
	}
	c := &Client{eth: eth, chainID: chainID}
	for _, p := range []struct {
		dst *abi.ABI
		src string
	}{{&c.classic, classicRouterABI}, {&c.r02, router02ABI}, {&c.quoter, quoterV2ABI}, {&c.erc20, erc20ABI}} {
		parsed, err := abi.JSON(strings.NewReader(p.src))
		if err != nil {
			return nil, fmt.Errorf("parse abi: %w", err)
		}
		*p.dst = parsed
	}
	return c, nil
}

// Close releases the underlying RPC connection.
func (c *Client) Close() { c.eth.Close() }

// ChainID returns the connected chain's ID.
func (c *Client) ChainID() *big.Int { return new(big.Int).Set(c.chainID) }

// QuoteExactIn returns the expected amountOut for swapping amountIn of tokenIn
// into tokenOut on the given fee-tier pool, via QuoterV2 (eth_call, read-only).
func (c *Client) QuoteExactIn(ctx context.Context, quoter, tokenIn, tokenOut common.Address, feeTier uint32, amountIn *big.Int) (*big.Int, error) {
	params := quoteExactInputSingleParams{
		TokenIn: tokenIn, TokenOut: tokenOut, AmountIn: amountIn,
		Fee: big.NewInt(int64(feeTier)), SqrtPriceLimitX96: big.NewInt(0),
	}
	data, err := c.quoter.Pack("quoteExactInputSingle", params)
	if err != nil {
		return nil, fmt.Errorf("pack quote: %w", err)
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &quoter, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("quote call: %w", err)
	}
	vals, err := c.quoter.Unpack("quoteExactInputSingle", out)
	if err != nil {
		return nil, fmt.Errorf("unpack quote: %w", err)
	}
	return vals[0].(*big.Int), nil
}

// SwapExactIn swaps amountIn of tokenIn for at least minOut of tokenOut through
// the router (approving first if the allowance is short) and returns the mined
// tx hash. minOut guards slippage — derive it from QuoteExactIn. The recipient
// receives tokenOut directly.
func (c *Client) SwapExactIn(ctx context.Context, from *ecdsa.PrivateKey, router common.Address, kind RouterKind, tokenIn, tokenOut common.Address, feeTier uint32, recipient common.Address, amountIn, minOut *big.Int) (common.Hash, error) {
	fromAddr := crypto.PubkeyToAddress(from.PublicKey)

	// Approve the router if the standing allowance is short.
	allowData, err := c.erc20.Pack("allowance", fromAddr, router)
	if err != nil {
		return common.Hash{}, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &tokenIn, Data: allowData}, nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("allowance call: %w", err)
	}
	vals, err := c.erc20.Unpack("allowance", out)
	if err != nil {
		return common.Hash{}, err
	}
	if vals[0].(*big.Int).Cmp(amountIn) < 0 {
		approveData, err := c.erc20.Pack("approve", router, amountIn)
		if err != nil {
			return common.Hash{}, err
		}
		if _, err := c.sendTx(ctx, from, tokenIn, big.NewInt(0), approveData); err != nil {
			return common.Hash{}, fmt.Errorf("approve: %w", err)
		}
	}

	var data []byte
	switch kind {
	case Router02:
		data, err = c.r02.Pack("exactInputSingle", router02ExactInputSingleParams{
			TokenIn: tokenIn, TokenOut: tokenOut, Fee: big.NewInt(int64(feeTier)),
			Recipient: recipient, AmountIn: amountIn, AmountOutMinimum: minOut,
			SqrtPriceLimitX96: big.NewInt(0),
		})
	default: // RouterClassic
		deadline := big.NewInt(time.Now().Add(10 * time.Minute).Unix())
		data, err = c.classic.Pack("exactInputSingle", classicExactInputSingleParams{
			TokenIn: tokenIn, TokenOut: tokenOut, Fee: big.NewInt(int64(feeTier)),
			Recipient: recipient, Deadline: deadline, AmountIn: amountIn,
			AmountOutMinimum: minOut, SqrtPriceLimitX96: big.NewInt(0),
		})
	}
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack exactInputSingle: %w", err)
	}
	return c.sendTx(ctx, from, router, big.NewInt(0), data)
}

// BalanceOf returns the ERC20 balance of addr in base units.
func (c *Client) BalanceOf(ctx context.Context, token, addr common.Address) (*big.Int, error) {
	data, err := c.erc20.Pack("balanceOf", addr)
	if err != nil {
		return nil, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("balanceOf call: %w", err)
	}
	vals, err := c.erc20.Unpack("balanceOf", out)
	if err != nil {
		return nil, err
	}
	return vals[0].(*big.Int), nil
}

// SendRaw signs and sends an arbitrary contract call from `from` — escape hatch
// for test-only calls (fork-cheatcode funding etc.).
func (c *Client) SendRaw(ctx context.Context, from *ecdsa.PrivateKey, to common.Address, value *big.Int, data []byte) (common.Hash, error) {
	return c.sendTx(ctx, from, to, value, data)
}

// sendTx builds, signs, sends, and waits for a legacy transaction, holding
// sendMu across the read-nonce -> send -> wait-mined sequence.
func (c *Client) sendTx(ctx context.Context, from *ecdsa.PrivateKey, to common.Address, value *big.Int, data []byte) (common.Hash, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	fromAddr := crypto.PubkeyToAddress(from.PublicKey)
	nonce, err := c.eth.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pending nonce: %w", err)
	}
	gasPrice, err := c.eth.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("suggest gas price: %w", err)
	}
	gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(2))
	gasLimit, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr, To: &to, Value: value, Data: data,
	})
	if err != nil {
		return common.Hash{}, fmt.Errorf("estimate gas: %w", err)
	}
	gasLimit = gasLimit + gasLimit/4

	tx := types.NewTx(&types.LegacyTx{
		Nonce: nonce, To: &to, Value: value, Gas: gasLimit, GasPrice: gasPrice, Data: data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(c.chainID), from)
	if err != nil {
		return common.Hash{}, fmt.Errorf("sign tx: %w", err)
	}
	if err := c.eth.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("send tx: %w", err)
	}
	receipt, err := bind.WaitMined(ctx, c.eth, signed)
	if err != nil {
		return signed.Hash(), fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return signed.Hash(), fmt.Errorf("tx %s reverted", signed.Hash().Hex())
	}
	return signed.Hash(), nil
}
