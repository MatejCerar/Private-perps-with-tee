package arb

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EIP-2612 Permit type hash:
//
//	Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)
var permitTypeHash = crypto.Keccak256([]byte("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"))

// permitReadABI is the EIP-2612 read surface of USDC: the domain separator and
// per-owner nonce used to build the permit digest.
const permitReadABI = `[
	{"constant":true,"inputs":[],"name":"DOMAIN_SEPARATOR","outputs":[{"name":"","type":"bytes32"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"nonces","outputs":[{"name":"","type":"uint256"}],"type":"function"}
]`

// batchedDepositABI is Bridge2.batchedDepositWithPermit. Failed permit/deposit
// entries are SKIPPED (they emit FailedPermitDeposit), not reverted, so a mined
// receipt does NOT imply every user was credited — verify via HL credit.
const batchedDepositABI = `[
	{"name":"batchedDepositWithPermit","type":"function","inputs":[{"name":"deposits","type":"tuple[]","components":[
		{"name":"user","type":"address"},
		{"name":"usd","type":"uint64"},
		{"name":"deadline","type":"uint64"},
		{"name":"signature","type":"tuple","components":[
			{"name":"v","type":"uint8"},
			{"name":"r","type":"bytes32"},
			{"name":"s","type":"bytes32"}
		]}
	]}],"outputs":[]}
]`

// Signature is the {v,r,s} tuple Bridge2 expects inside DepositWithPermit.
type Signature struct {
	V uint8
	R [32]byte
	S [32]byte
}

// PermitDeposit is one entry of batchedDepositWithPermit's array. The user's
// permit signature must already be filled in (see SignPermit). USD is raw 6-decimal
// USDC units (must fit uint64); Deadline is a unix timestamp.
type PermitDeposit struct {
	User      common.Address
	USD       *big.Int
	Deadline  *big.Int
	Signature Signature
}

// depositTuple mirrors the on-chain tuple for ABI packing.
type depositTuple struct {
	User      common.Address
	Usd       uint64
	Deadline  uint64
	Signature sigTuple
}

type sigTuple struct {
	V uint8
	R [32]byte
	S [32]byte
}

// domainSepCache memoizes DOMAIN_SEPARATOR() per token address — it is immutable
// for the life of the token contract, so a one-time read per process is enough.
var domainSepCache sync.Map // map[common.Address][32]byte

// SignPermit produces the EIP-2612 permit signature of (owner=userKey, spender,
// value, deadline) over the token's domain. It reads the owner's current nonce
// and the token's DOMAIN_SEPARATOR on-chain, so it must be called shortly before
// the relayer submits the batched deposit (the nonce is consumed once per permit).
func (c *Client) SignPermit(ctx context.Context, userKey *ecdsa.PrivateKey, token, spender common.Address, value *big.Int, deadline *big.Int) (Signature, error) {
	owner := crypto.PubkeyToAddress(userKey.PublicKey)

	// Domain separator (cached).
	var domain [32]byte
	if v, ok := domainSepCache.Load(token); ok {
		domain = v.([32]byte)
	} else {
		read, err := abi.JSON(strings.NewReader(permitReadABI))
		if err != nil {
			return Signature{}, fmt.Errorf("parse permit read abi: %w", err)
		}
		data, err := read.Pack("DOMAIN_SEPARATOR")
		if err != nil {
			return Signature{}, err
		}
		out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
		if err != nil {
			return Signature{}, fmt.Errorf("DOMAIN_SEPARATOR call: %w", err)
		}
		vals, err := read.Unpack("DOMAIN_SEPARATOR", out)
		if err != nil {
			return Signature{}, err
		}
		ds, ok := vals[0].([32]byte)
		if !ok {
			return Signature{}, fmt.Errorf("DOMAIN_SEPARATOR: unexpected type %T", vals[0])
		}
		domain = ds
		domainSepCache.Store(token, domain)
	}

	// Nonce.
	read, err := abi.JSON(strings.NewReader(permitReadABI))
	if err != nil {
		return Signature{}, fmt.Errorf("parse permit read abi: %w", err)
	}
	data, err := read.Pack("nonces", owner)
	if err != nil {
		return Signature{}, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return Signature{}, fmt.Errorf("nonces call: %w", err)
	}
	vals, err := read.Unpack("nonces", out)
	if err != nil {
		return Signature{}, err
	}
	nonce, ok := vals[0].(*big.Int)
	if !ok {
		return Signature{}, fmt.Errorf("nonces: unexpected type %T", vals[0])
	}

	// structHash = keccak256(abi.encode(PERMIT_TYPEHASH, owner, spender, value, nonce, deadline))
	var buf []byte
	buf = append(buf, permitTypeHash...)
	buf = append(buf, common.LeftPadBytes(owner.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(spender.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(value.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(nonce.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(deadline.Bytes(), 32)...)
	structHash := crypto.Keccak256(buf)

	// digest = keccak256(0x19 . 0x01 . domainSeparator . structHash)
	var prefixed []byte
	prefixed = append(prefixed, 0x19, 0x01)
	prefixed = append(prefixed, domain[:]...)
	prefixed = append(prefixed, structHash...)
	digest := crypto.Keccak256(prefixed)

	sig, err := crypto.Sign(digest, userKey)
	if err != nil {
		return Signature{}, fmt.Errorf("sign permit: %w", err)
	}
	// crypto.Sign returns [r(32) || s(32) || v(0|1)]; EIP-2612 expects v in {27,28}.
	var s Signature
	copy(s.R[:], sig[0:32])
	copy(s.S[:], sig[32:64])
	s.V = sig[64] + 27
	return s, nil
}

// BatchedDepositWithPermit submits a single Arbitrum tx that deposits USDC for
// every entry via Bridge2's batchedDepositWithPermit: each entry's permit sets the
// bridge's allowance, then transferFrom pulls the USDC from the user to the bridge.
// The relayer (relayerKey) pays gas; users need no ETH, only the USDC at their
// address. A mined receipt does NOT mean every entry credited (failed entries are
// skipped on-chain) — callers must verify each user's HL credit afterwards.
func (c *Client) BatchedDepositWithPermit(ctx context.Context, relayerKey *ecdsa.PrivateKey, usdc, bridge common.Address, deposits []PermitDeposit) (common.Hash, error) {
	batched, err := abi.JSON(strings.NewReader(batchedDepositABI))
	if err != nil {
		return common.Hash{}, fmt.Errorf("parse batched deposit abi: %w", err)
	}
	tuples := make([]depositTuple, 0, len(deposits))
	for i, d := range deposits {
		if d.USD.IsUint64() == false {
			return common.Hash{}, fmt.Errorf("deposit %d: usd %s overflows uint64", i, d.USD.String())
		}
		if d.Deadline.IsUint64() == false {
			return common.Hash{}, fmt.Errorf("deposit %d: deadline %s overflows uint64", i, d.Deadline.String())
		}
		tuples = append(tuples, depositTuple{
			User:      d.User,
			Usd:       d.USD.Uint64(),
			Deadline:  d.Deadline.Uint64(),
			Signature: sigTuple{V: d.Signature.V, R: d.Signature.R, S: d.Signature.S},
		})
	}
	data, err := batched.Pack("batchedDepositWithPermit", tuples)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack batched deposit: %w", err)
	}
	// C2 is shelved (batchedDepositWithPermit is not deployed); the relayer is a
	// raw key here, wrapped as a Signer for sendTx's uniform interface.
	return c.sendTx(ctx, keys.NewRawSigner(relayerKey), bridge, big.NewInt(0), data)
}