// Package arb is a thin Arbitrum (Sepolia) client used by the Hyperliquid
// funding path. Hyperliquid's only canonical deposit is native USDC sent to the
// Bridge2 contract on Arbitrum, credited to the sending address on the HL L1;
// withdrawals are delivered back to an Arbitrum address by HL validators. This
// package covers the Arbitrum-side primitives: reading USDC balances, moving
// USDC between addresses, funding gas, and depositing to Bridge2 (which is just
// an ERC20 transfer to the bridge address — the transfer's sender is credited).
package arb

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// USDCDecimals is the ERC20 decimals of USDC (native and the HL testnet mock).
const USDCDecimals = 6

// erc20ABI is the minimal ABI needed for balances and transfers.
const erc20ABI = `[
	{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}
]`

// Client wraps an Arbitrum RPC connection plus the parsed ERC20 ABI.
type Client struct {
	eth     *ethclient.Client
	chainID *big.Int
	erc20   abi.ABI
}

// New dials the given Arbitrum RPC URL and reads the chain ID.
func New(ctx context.Context, rpcURL string) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial arbitrum rpc: %w", err)
	}
	chainID, err := eth.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read chain id: %w", err)
	}
	parsed, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, fmt.Errorf("parse erc20 abi: %w", err)
	}
	return &Client{eth: eth, chainID: chainID, erc20: parsed}, nil
}

// Close releases the underlying RPC connection.
func (c *Client) Close() { c.eth.Close() }

// ChainID returns the connected chain's ID (421614 for Arbitrum Sepolia).
func (c *Client) ChainID() *big.Int { return new(big.Int).Set(c.chainID) }

// USDToUnits converts a USD float to USDC base units (6 decimals), rounded.
func USDToUnits(usd float64) *big.Int {
	return big.NewInt(int64(math.Round(usd * math.Pow10(USDCDecimals))))
}

// UnitsToUSD converts USDC base units back to a USD float.
func UnitsToUSD(units *big.Int) float64 {
	if units == nil {
		return 0
	}
	f := new(big.Float).SetInt(units)
	f.Quo(f, big.NewFloat(math.Pow10(USDCDecimals)))
	v, _ := f.Float64()
	return v
}

// USDCBalance returns the token balance of addr in base units.
func (c *Client) USDCBalance(ctx context.Context, token, addr common.Address) (*big.Int, error) {
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
	bal, ok := vals[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("balanceOf: unexpected return type")
	}
	return bal, nil
}

// ETHBalance returns the native ETH balance of addr in wei.
func (c *Client) ETHBalance(ctx context.Context, addr common.Address) (*big.Int, error) {
	return c.eth.BalanceAt(ctx, addr, nil)
}

// TransferUSDC sends `units` of the token from `from` to `to` and waits for the
// receipt. Returns the mined transaction hash.
func (c *Client) TransferUSDC(ctx context.Context, from *ecdsa.PrivateKey, token, to common.Address, units *big.Int) (common.Hash, error) {
	data, err := c.erc20.Pack("transfer", to, units)
	if err != nil {
		return common.Hash{}, err
	}
	return c.sendTx(ctx, from, token, big.NewInt(0), data)
}

// DepositToBridge deposits `units` of USDC to the Hyperliquid Bridge2 contract.
// A Bridge2 deposit is a plain ERC20 transfer to the bridge address; HL
// validators credit the sending address on the L1. USDC must therefore already
// sit at `from`'s address, and the credited HL account is `from`'s address.
func (c *Client) DepositToBridge(ctx context.Context, from *ecdsa.PrivateKey, usdc, bridge common.Address, units *big.Int) (common.Hash, error) {
	return c.TransferUSDC(ctx, from, usdc, bridge, units)
}

// SendETH transfers `wei` of native ETH from `from` to `to` (for gas funding)
// and waits for the receipt.
func (c *Client) SendETH(ctx context.Context, from *ecdsa.PrivateKey, to common.Address, wei *big.Int) (common.Hash, error) {
	return c.sendTx(ctx, from, to, wei, nil)
}

// sendTx builds, signs, sends, and waits for a legacy transaction. A nil/empty
// `data` with a positive `value` is a plain ETH transfer; otherwise it is a
// contract call.
func (c *Client) sendTx(ctx context.Context, from *ecdsa.PrivateKey, to common.Address, value *big.Int, data []byte) (common.Hash, error) {
	fromAddr := crypto.PubkeyToAddress(from.PublicKey)

	nonce, err := c.eth.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pending nonce: %w", err)
	}
	gasPrice, err := c.eth.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("suggest gas price: %w", err)
	}
	// Pad the gas price so a base fee that rises between suggestion and inclusion
	// does not reject the tx ("max fee per gas less than block base fee").
	// Arbitrum charges the actual base fee, so a higher ceiling costs nothing.
	gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(2))
	gasLimit, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr, To: &to, Value: value, Data: data,
	})
	if err != nil {
		return common.Hash{}, fmt.Errorf("estimate gas: %w", err)
	}
	// Pad the estimate; Arbitrum gas accounting can shift between estimate and
	// inclusion, and the surplus is refunded.
	gasLimit = gasLimit + gasLimit/4

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    value,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
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

// WaitForUSDCBalance polls the token balance of addr until it is at least
// `min` (base units) or the context/deadline expires. Useful for observing a
// cross-chain credit (deposit crediting the HL side, or a withdrawal arriving on
// Arbitrum) that has no synchronous confirmation.
func (c *Client) WaitForUSDCBalance(ctx context.Context, token, addr common.Address, min *big.Int, poll, timeout time.Duration) (*big.Int, error) {
	deadline := time.Now().Add(timeout)
	for {
		bal, err := c.USDCBalance(ctx, token, addr)
		if err == nil && bal.Cmp(min) >= 0 {
			return bal, nil
		}
		if time.Now().After(deadline) {
			last := "<err>"
			if bal != nil {
				last = bal.String()
			}
			return bal, fmt.Errorf("timed out waiting for balance >= %s (last=%s)", min.String(), last)
		}
		select {
		case <-ctx.Done():
			return bal, ctx.Err()
		case <-time.After(poll):
		}
	}
}
