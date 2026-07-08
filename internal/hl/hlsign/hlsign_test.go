package hlsign

import (
	"math/big"
	"testing"

	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	gohl "github.com/sonirico/go-hyperliquid"
)

// The whole package earns its keep only if it is byte-identical to upstream
// go-hyperliquid for the same key + action. These tests sign via BOTH our
// Signer path (rawSigner over key K) and gohl's native path (key K) and assert
// the r/s/v match exactly — for an L1 action (orders) and a user-signed action
// (withdraw3 etc.). If go-hyperliquid ever changes its encoding, these break.

// orderWire mirrors the shape of an L1 order action closely enough to exercise
// msgpack encoding (nested struct, ordered fields, float + bool + string). The
// equality test only needs the SAME value on both sides, not a real order.
type orderWire struct {
	Type   string      `msgpack:"type" json:"type"`
	Orders []orderEntry `msgpack:"orders" json:"orders"`
	Group  string      `msgpack:"grouping" json:"grouping"`
}

type orderEntry struct {
	Asset      int     `msgpack:"a" json:"a"`
	IsBuy      bool    `msgpack:"b" json:"b"`
	Price      string  `msgpack:"p" json:"p"`
	Size       string  `msgpack:"s" json:"s"`
	ReduceOnly bool    `msgpack:"r" json:"r"`
	OrderType  otWire  `msgpack:"t" json:"t"`
}

type otWire struct {
	Limit limitWire `msgpack:"limit" json:"limit"`
}
type limitWire struct {
	Tif string `msgpack:"tif" json:"tif"`
}

func sampleL1Action() orderWire {
	return orderWire{
		Type: "order",
		Orders: []orderEntry{{
			Asset: 4, IsBuy: true, Price: "906", Size: "0.014", ReduceOnly: false,
			OrderType: otWire{Limit: limitWire{Tif: "Gtc"}},
		}},
		Group: "na",
	}
}

func TestSignL1ActionMatchesUpstream(t *testing.T) {
	priv, _ := crypto.HexToECDSA("11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff")
	signer := keys.NewRawSigner(priv)
	action := sampleL1Action()
	const nonce int64 = 1_700_000_000_000

	for _, mainnet := range []bool{false, true} {
		mine, err := SignL1Action(signer, action, "", nonce, nil, mainnet)
		if err != nil {
			t.Fatal(err)
		}
		up, err := gohl.SignL1Action(priv, action, "", nonce, nil, mainnet)
		if err != nil {
			t.Fatal(err)
		}
		if mine.R != up.R || mine.S != up.S || mine.V != up.V {
			t.Fatalf("L1 action sig mismatch (mainnet=%v):\n mine=%+v\n up  =%+v", mainnet, mine, up)
		}
	}
}

func TestSignL1ActionWithExpiresAfterMatchesUpstream(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer := keys.NewRawSigner(priv)
	action := sampleL1Action()
	exp := int64(1_700_000_600_000)

	mine, err := SignL1Action(signer, action, "", 1_700_000_000_001, &exp, false)
	if err != nil {
		t.Fatal(err)
	}
	up, err := gohl.SignL1Action(priv, action, "", 1_700_000_000_001, &exp, false)
	if err != nil {
		t.Fatal(err)
	}
	if mine != SignatureResult(up) {
		t.Fatalf("mismatch with expiresAfter:\n mine=%+v\n up  =%+v", mine, up)
	}
}

func TestSignUserActionMatchesUpstream(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer := keys.NewRawSigner(priv)

	// A withdraw3 user-signed action (the exact payloadTypes HL expects).
	payloadTypes := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"},
		{Name: "destination", Type: "string"},
		{Name: "amount", Type: "string"},
		{Name: "time", Type: "uint64"},
	}
	newAction := func() map[string]any {
		return map[string]any{
			"type":        "withdraw3",
			"destination": "0xabc4Cbf716472c47a61c8c2c5076895600F3cf10",
			"amount":      "5.0",
			"time":        big.NewInt(1_700_000_000_000),
		}
	}

	for _, mainnet := range []bool{false, true} {
		// Each call mutates the map (adds signatureChainId/hyperliquidChain), so
		// build a fresh one per side.
		mine, err := SignUserAction(signer, newAction(), payloadTypes,
			"HyperliquidTransaction:Withdraw", mainnet)
		if err != nil {
			t.Fatal(err)
		}
		up, err := gohl.SignUserSignedAction(priv, newAction(), payloadTypes,
			"HyperliquidTransaction:Withdraw", mainnet)
		if err != nil {
			t.Fatal(err)
		}
		if mine != SignatureResult(up) {
			t.Fatalf("user action sig mismatch (mainnet=%v):\n mine=%+v\n up  =%+v", mainnet, mine, up)
		}
	}
}

// The signature must recover to the signer's address (sanity that r/s/v is a
// valid EIP-712 signature, not just equal to upstream).
func TestSignedActionRecoversToSigner(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer := keys.NewRawSigner(priv)
	hash, err := actionHash(sampleL1Action(), "", 123, nil)
	if err != nil {
		t.Fatal(err)
	}
	td := l1Payload(constructPhantomAgent(hash, false))
	domainSep, _ := td.HashStruct("EIP712Domain", td.Domain.Map())
	structHash, _ := hashStructLenient(td, td.PrimaryType, td.Message)
	var ds, sh [32]byte
	copy(ds[:], domainSep)
	copy(sh[:], structHash)
	preimage := keys.EIP712Preimage(ds, sh)

	sig, _ := signer.SignPreimage(preimage)
	pub, err := crypto.SigToPub(crypto.Keccak256(preimage), sig)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(*pub) != signer.Address() {
		t.Fatalf("recovered %s != signer %s", crypto.PubkeyToAddress(*pub), signer.Address())
	}
}
