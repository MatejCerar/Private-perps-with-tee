// Package stargate is a minimal Stargate V2 (LayerZero OFT) client for the
// Flare → Arbitrum funding leg. Stargate exposes the standard IOFT surface on
// every chain: quoteOFT (expected received amount + limits), quoteSend (the
// native fee for the message), and send (burn/lock on source, deliver on
// destination). The same interface covers both deployment shapes we touch:
// StargatePoolUSDC on Arbitrum (a pool of native USDC) and StargateOFTUSDC on
// Flare (a "hydra" OFT that burns bridged USDC.e) — so one client, parameterized
// by pool + endpoint IDs, serves testnet and mainnet with config-only changes.
//
// Delivery is asynchronous (LayerZero relays the message); WaitForBalance polls
// the destination token balance, mirroring arb.WaitForUSDCBalance.
//
// Mode note: an empty oftCmd selects "taxi" (immediate single-message delivery);
// a non-empty oftCmd selects "bus" (batched, cheaper, slower). This client
// always rides taxi — deterministic latency matters more than fee for reserve
// top-ups.
package stargate

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

// LayerZero V2 endpoint IDs for the chains this path touches.
const (
	EIDEthereumSepolia uint32 = 40161
	EIDArbitrumSepolia uint32 = 40231
	EIDArbitrumOne     uint32 = 30110
	EIDFlare           uint32 = 30295
)

// stargateABI is the minimal IStargate/IOFT surface: token, quoteOFT,
// quoteSend, send. Struct layouts follow LayerZero V2's IOFT.
const stargateABI = `[
	{"name":"token","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
	{"name":"quoteOFT","type":"function","stateMutability":"view",
		"inputs":[{"name":"_sendParam","type":"tuple","components":[
			{"name":"dstEid","type":"uint32"},{"name":"to","type":"bytes32"},
			{"name":"amountLD","type":"uint256"},{"name":"minAmountLD","type":"uint256"},
			{"name":"extraOptions","type":"bytes"},{"name":"composeMsg","type":"bytes"},{"name":"oftCmd","type":"bytes"}]}],
		"outputs":[
			{"name":"limit","type":"tuple","components":[{"name":"minAmountLD","type":"uint256"},{"name":"maxAmountLD","type":"uint256"}]},
			{"name":"oftFeeDetails","type":"tuple[]","components":[{"name":"feeAmountLD","type":"int256"},{"name":"description","type":"string"}]},
			{"name":"receipt","type":"tuple","components":[{"name":"amountSentLD","type":"uint256"},{"name":"amountReceivedLD","type":"uint256"}]}]},
	{"name":"quoteSend","type":"function","stateMutability":"view",
		"inputs":[{"name":"_sendParam","type":"tuple","components":[
			{"name":"dstEid","type":"uint32"},{"name":"to","type":"bytes32"},
			{"name":"amountLD","type":"uint256"},{"name":"minAmountLD","type":"uint256"},
			{"name":"extraOptions","type":"bytes"},{"name":"composeMsg","type":"bytes"},{"name":"oftCmd","type":"bytes"}]},
			{"name":"_payInLzToken","type":"bool"}],
		"outputs":[{"name":"fee","type":"tuple","components":[{"name":"nativeFee","type":"uint256"},{"name":"lzTokenFee","type":"uint256"}]}]},
	{"name":"send","type":"function","stateMutability":"payable",
		"inputs":[{"name":"_sendParam","type":"tuple","components":[
			{"name":"dstEid","type":"uint32"},{"name":"to","type":"bytes32"},
			{"name":"amountLD","type":"uint256"},{"name":"minAmountLD","type":"uint256"},
			{"name":"extraOptions","type":"bytes"},{"name":"composeMsg","type":"bytes"},{"name":"oftCmd","type":"bytes"}]},
			{"name":"_fee","type":"tuple","components":[{"name":"nativeFee","type":"uint256"},{"name":"lzTokenFee","type":"uint256"}]},
			{"name":"_refundAddress","type":"address"}],
		"outputs":[
			{"name":"msgReceipt","type":"tuple","components":[{"name":"guid","type":"bytes32"},{"name":"nonce","type":"uint64"},
				{"name":"fee","type":"tuple","components":[{"name":"nativeFee","type":"uint256"},{"name":"lzTokenFee","type":"uint256"}]}]},
			{"name":"oftReceipt","type":"tuple","components":[{"name":"amountSentLD","type":"uint256"},{"name":"amountReceivedLD","type":"uint256"}]}]}
]`

const erc20ABI = `[
	{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"type":"function"}
]`

// sendParam mirrors IOFT.SendParam for abi packing.
type sendParam struct {
	DstEid       uint32
	To           [32]byte
	AmountLD     *big.Int
	MinAmountLD  *big.Int
	ExtraOptions []byte
	ComposeMsg   []byte
	OftCmd       []byte
}

// messagingFee mirrors IOFT.MessagingFee.
type messagingFee struct {
	NativeFee  *big.Int
	LzTokenFee *big.Int
}

// oftLimit / oftReceipt mirror IOFT return structs for abi.ConvertType.
type oftLimit struct {
	MinAmountLD *big.Int
	MaxAmountLD *big.Int
}
type oftReceipt struct {
	AmountSentLD     *big.Int
	AmountReceivedLD *big.Int
}

// Quote is the result of quoting a send: what the recipient would receive, the
// pool's current min/max, and the LayerZero native fee to attach.
type Quote struct {
	AmountReceivedLD *big.Int // delivered on destination (after pool fees)
	MinAmountLD      *big.Int // pool's lower bound for amountLD
	MaxAmountLD      *big.Int // pool's upper bound for amountLD
	NativeFee        *big.Int // wei of source-chain gas token to send with the tx
}

// Client wraps one chain's RPC connection plus the parsed ABIs. Source and
// destination chains each get their own Client.
type Client struct {
	eth     *ethclient.Client
	chainID *big.Int
	sg      abi.ABI
	erc20   abi.ABI

	// sendMu serializes sendTx per client: concurrent sends sharing one key
	// (the reserve) must not race on the nonce. Same rationale as arb.Client.
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
	sg, err := abi.JSON(strings.NewReader(stargateABI))
	if err != nil {
		return nil, fmt.Errorf("parse stargate abi: %w", err)
	}
	erc, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, fmt.Errorf("parse erc20 abi: %w", err)
	}
	return &Client{eth: eth, chainID: chainID, sg: sg, erc20: erc}, nil
}

// Close releases the underlying RPC connection.
func (c *Client) Close() { c.eth.Close() }

// ChainID returns the connected chain's ID.
func (c *Client) ChainID() *big.Int { return new(big.Int).Set(c.chainID) }

// AddressToBytes32 left-pads an EVM address into the bytes32 recipient format
// LayerZero uses.
func AddressToBytes32(a common.Address) [32]byte {
	var out [32]byte
	copy(out[12:], a.Bytes())
	return out
}

// Token returns the ERC20 the pool sends (address(0) for native-gas pools).
func (c *Client) Token(ctx context.Context, pool common.Address) (common.Address, error) {
	data, err := c.sg.Pack("token")
	if err != nil {
		return common.Address{}, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: data}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("token call: %w", err)
	}
	vals, err := c.sg.Unpack("token", out)
	if err != nil {
		return common.Address{}, err
	}
	return vals[0].(common.Address), nil
}

// newSendParam builds a taxi-mode SendParam. minAmountLD=0 is valid for
// quoting; Send requires a real minimum (from Quote).
func newSendParam(dstEid uint32, to common.Address, amountLD, minAmountLD *big.Int) sendParam {
	if minAmountLD == nil {
		minAmountLD = big.NewInt(0)
	}
	return sendParam{
		DstEid:       dstEid,
		To:           AddressToBytes32(to),
		AmountLD:     amountLD,
		MinAmountLD:  minAmountLD,
		ExtraOptions: []byte{}, // pool's enforced options cover delivery gas
		ComposeMsg:   []byte{},
		OftCmd:       []byte{}, // empty = taxi
	}
}

// QuoteSend quotes a taxi send of amountLD to `to` on dstEid: the delivered
// amount (after pool fees), the pool's limits, and the native fee. Read-only.
func (c *Client) QuoteSend(ctx context.Context, pool common.Address, dstEid uint32, to common.Address, amountLD *big.Int) (*Quote, error) {
	sp := newSendParam(dstEid, to, amountLD, nil)

	data, err := c.sg.Pack("quoteOFT", sp)
	if err != nil {
		return nil, fmt.Errorf("pack quoteOFT: %w", err)
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("quoteOFT call: %w", err)
	}
	vals, err := c.sg.Unpack("quoteOFT", out)
	if err != nil {
		return nil, fmt.Errorf("unpack quoteOFT: %w", err)
	}
	limit := *abi.ConvertType(vals[0], new(oftLimit)).(*oftLimit)
	receipt := *abi.ConvertType(vals[2], new(oftReceipt)).(*oftReceipt)

	// Quote the messaging fee with the real minAmountLD so the fee matches what
	// Send will submit.
	sp.MinAmountLD = receipt.AmountReceivedLD
	data, err = c.sg.Pack("quoteSend", sp, false)
	if err != nil {
		return nil, fmt.Errorf("pack quoteSend: %w", err)
	}
	out, err = c.eth.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("quoteSend call: %w", err)
	}
	vals, err = c.sg.Unpack("quoteSend", out)
	if err != nil {
		return nil, fmt.Errorf("unpack quoteSend: %w", err)
	}
	fee := *abi.ConvertType(vals[0], new(messagingFee)).(*messagingFee)

	return &Quote{
		AmountReceivedLD: receipt.AmountReceivedLD,
		MinAmountLD:      limit.MinAmountLD,
		MaxAmountLD:      limit.MaxAmountLD,
		NativeFee:        fee.NativeFee,
	}, nil
}

// Send bridges amountLD of the pool's token from `from` to `to` on dstEid in
// taxi mode: approves the pool if the current allowance is short, then calls
// send with quote.NativeFee attached. minAmountLD guards slippage — pass
// quote.AmountReceivedLD (or lower it for tolerance). Returns the source-chain
// tx hash; delivery on the destination is asynchronous (WaitForBalance).
func (c *Client) Send(ctx context.Context, from *ecdsa.PrivateKey, pool, token, to common.Address, dstEid uint32, amountLD, minAmountLD, nativeFee *big.Int) (common.Hash, error) {
	fromAddr := crypto.PubkeyToAddress(from.PublicKey)

	// Approve only when the standing allowance is short (idempotent top-up).
	allowData, err := c.erc20.Pack("allowance", fromAddr, pool)
	if err != nil {
		return common.Hash{}, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &token, Data: allowData}, nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("allowance call: %w", err)
	}
	vals, err := c.erc20.Unpack("allowance", out)
	if err != nil {
		return common.Hash{}, err
	}
	if vals[0].(*big.Int).Cmp(amountLD) < 0 {
		approveData, err := c.erc20.Pack("approve", pool, amountLD)
		if err != nil {
			return common.Hash{}, err
		}
		if _, err := c.sendTx(ctx, from, token, big.NewInt(0), approveData); err != nil {
			return common.Hash{}, fmt.Errorf("approve: %w", err)
		}
	}

	sp := newSendParam(dstEid, to, amountLD, minAmountLD)
	fee := messagingFee{NativeFee: nativeFee, LzTokenFee: big.NewInt(0)}
	sendData, err := c.sg.Pack("send", sp, fee, fromAddr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack send: %w", err)
	}
	return c.sendTx(ctx, from, pool, nativeFee, sendData)
}

// SendRaw signs and sends an arbitrary contract call from `from`, waiting for
// the receipt. Escape hatch for one-off calls that don't merit a typed method
// (e.g. the testnet USDC mock's public mint in live tests); production paths
// use the typed methods above.
func (c *Client) SendRaw(ctx context.Context, from *ecdsa.PrivateKey, to common.Address, value *big.Int, data []byte) (common.Hash, error) {
	return c.sendTx(ctx, from, to, value, data)
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

// WaitForBalance polls the token balance of addr until it reaches at least
// `min` or the timeout elapses — the destination-side observation of an
// asynchronous LayerZero delivery.
func (c *Client) WaitForBalance(ctx context.Context, token, addr common.Address, min *big.Int, poll, timeout time.Duration) (*big.Int, error) {
	deadline := time.Now().Add(timeout)
	for {
		bal, err := c.BalanceOf(ctx, token, addr)
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

// sendTx builds, signs, sends, and waits for a legacy transaction, holding
// sendMu across the read-nonce -> send -> wait-mined sequence (same nonce-race
// rationale as arb.Client.sendTx).
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
	gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(2)) // headroom vs base-fee drift
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
