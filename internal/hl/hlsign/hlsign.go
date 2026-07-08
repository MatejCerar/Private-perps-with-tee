// Package hlsign reproduces Hyperliquid's action signing (EIP-712 over the
// msgpack action hash for L1 actions; direct EIP-712 for user-signed actions)
// but signs through a keys.Signer instead of a raw *ecdsa.PrivateKey. That lets
// HL orders/cancels/withdraws be signed by a TEE-custodied wallet key (C9/PMW)
// exactly as they are by a seed-derived key — the signature is byte-identical
// because the preimage is identical (verified against upstream in the tests).
//
// The msgpack/typed-data primitives (addressToBytes, convertStr16ToStr8,
// actionHash, constructPhantomAgent, l1Payload, hashStructLenient) are vendored
// verbatim from github.com/sonirico/go-hyperliquid@v0.33.1 (MIT, © 2025
// Marquitos) so byte-for-byte behavior matches; only the signing leaf (signInner)
// is swapped to take a Signer. Keeping them in-tree also means the exact action
// encoding is pinned and can't drift under us.
package hlsign

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/vmihailenco/msgpack/v5"
)

// SignatureResult is HL's r/s/v signature shape (hex r/s, v in {27,28}). Matches
// go-hyperliquid's SignatureResult field-for-field so it drops into the same
// {"action","nonce","signature"} exchange payloads.
type SignatureResult struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

// --- vendored primitives (go-hyperliquid, MIT) -----------------------------

func addressToBytes(address string) []byte {
	address = strings.TrimPrefix(address, "0x")
	b, _ := hex.DecodeString(address)
	return b
}

// convertStr16ToStr8 rewrites msgpack str16 (0xda + 2-byte len) as str8
// (0xd9 + 1-byte len) for strings <256 bytes, matching Python msgpack — HL
// hashes the Python-encoded bytes, so this must match exactly.
func convertStr16ToStr8(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		b := data[i]
		if b == 0xda && i+2 < len(data) {
			length := (int(data[i+1]) << 8) | int(data[i+2])
			if length < 256 {
				result = append(result, 0xd9, byte(length))
				i += 3
				if i+length <= len(data) {
					result = append(result, data[i:i+length]...)
					i += length
				}
				continue
			}
		}
		result = append(result, b)
		i++
	}
	return result
}

// actionHash computes HL's L1 action hash: msgpack(action) ‖ nonce(8, BE) ‖
// vault flag(+addr) ‖ optional expiresAfter, then keccak256.
func actionHash(action any, vaultAddress string, nonce int64, expiresAfter *int64) ([]byte, error) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	// Must NOT sort map keys — Python preserves insertion order; structs encode
	// in field-declaration order.
	enc.UseCompactInts(true)
	if err := enc.Encode(action); err != nil {
		return nil, fmt.Errorf("msgpack action: %w", err)
	}
	data := convertStr16ToStr8(buf.Bytes())

	if nonce < 0 {
		return nil, fmt.Errorf("nonce cannot be negative: %d", nonce)
	}
	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, uint64(nonce))
	data = append(data, nonceBytes...)

	if vaultAddress == "" {
		data = append(data, 0x00)
	} else {
		data = append(data, 0x01)
		data = append(data, addressToBytes(vaultAddress)...)
	}

	if expiresAfter != nil {
		if *expiresAfter < 0 {
			return nil, fmt.Errorf("expiresAfter cannot be negative: %d", *expiresAfter)
		}
		data = append(data, 0x00)
		eb := make([]byte, 8)
		binary.BigEndian.PutUint64(eb, uint64(*expiresAfter))
		data = append(data, eb...)
	}
	return crypto.Keccak256(data), nil
}

func constructPhantomAgent(hash []byte, isMainnet bool) map[string]any {
	source := "b" // testnet
	if isMainnet {
		source = "a" // mainnet
	}
	return map[string]any{"source": source, "connectionId": hash}
}

func l1Payload(phantomAgent map[string]any) apitypes.TypedData {
	// chainId 1337 is a fixed signing-domain identifier for L1 actions (both nets).
	chainID := math.HexOrDecimal256(*big.NewInt(1337))
	return apitypes.TypedData{
		Domain: apitypes.TypedDataDomain{
			ChainId:           &chainID,
			Name:              "Exchange",
			Version:           "1",
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: apitypes.Types{
			"Agent": []apitypes.Type{
				{Name: "source", Type: "string"},
				{Name: "connectionId", Type: "bytes32"},
			},
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		PrimaryType: "Agent",
		Message:     phantomAgent,
	}
}

// hashStructLenient hashes only the fields declared in the primary type, ignoring
// extras in the message (HL user-signed actions carry extra fields like
// signatureChainId/type). Mirrors Python eth_account.
func hashStructLenient(typedData apitypes.TypedData, primaryType string, message map[string]any) ([]byte, error) {
	types := typedData.Types[primaryType]
	filtered := make(map[string]any)
	for _, t := range types {
		val, ok := message[t.Name]
		if !ok {
			continue
		}
		if t.Type == "uint64" {
			uv, err := toUint64(val, t.Name)
			if err != nil {
				return nil, err
			}
			filtered[t.Name] = new(big.Int).SetUint64(uv)
		} else {
			filtered[t.Name] = val
		}
	}
	return typedData.HashStruct(primaryType, filtered)
}

func toUint64(val any, name string) (uint64, error) {
	switch v := val.(type) {
	case uint64:
		return v, nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("negative int64 %d for %s", v, name)
		}
		return uint64(v), nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("negative int %d for %s", v, name)
		}
		return uint64(v), nil
	case float64:
		if v < 0 || v != float64(uint64(v)) {
			return 0, fmt.Errorf("invalid float64 %f for uint64 %s", v, name)
		}
		return uint64(v), nil
	case json.Number:
		return strconv.ParseUint(string(v), 10, 64)
	case string:
		return strconv.ParseUint(v, 10, 64)
	default:
		jb, err := json.Marshal(v)
		if err != nil {
			return 0, fmt.Errorf("marshal %s: %w", name, err)
		}
		var uv uint64
		if err := json.Unmarshal(jb, &uv); err != nil {
			return 0, fmt.Errorf("convert %s to uint64: %w", name, err)
		}
		return uv, nil
	}
}

// --- signing via a keys.Signer ---------------------------------------------

// signInner computes the EIP-712 preimage (0x1901 ‖ domainSep ‖ structHash) and
// signs it through the Signer. Identical preimage to go-hyperliquid's signInner,
// so the resulting r/s/v is byte-identical for the same key material.
func signInner(signer keys.Signer, typedData apitypes.TypedData) (SignatureResult, error) {
	domainSep, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return SignatureResult{}, fmt.Errorf("hash domain: %w", err)
	}
	structHash, err := hashStructLenient(typedData, typedData.PrimaryType, typedData.Message)
	if err != nil {
		return SignatureResult{}, fmt.Errorf("hash message: %w", err)
	}
	var ds, sh [32]byte
	copy(ds[:], domainSep)
	copy(sh[:], structHash)

	sig, err := signer.SignPreimage(keys.EIP712Preimage(ds, sh))
	if err != nil {
		return SignatureResult{}, err
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	v := int(sig[64]) + 27
	return SignatureResult{R: hexutil.EncodeBig(r), S: hexutil.EncodeBig(s), V: v}, nil
}

// SignL1Action signs an L1 action (order, cancel, updateLeverage, …) through the
// Signer: msgpack action hash → phantom agent → EIP-712.
func SignL1Action(signer keys.Signer, action any, vaultAddress string, nonce int64, expiresAfter *int64, isMainnet bool) (SignatureResult, error) {
	hash, err := actionHash(action, vaultAddress, nonce, expiresAfter)
	if err != nil {
		return SignatureResult{}, err
	}
	return signInner(signer, l1Payload(constructPhantomAgent(hash, isMainnet)))
}

// SignUserAction signs a user-signed action (withdraw3, spotSend,
// usdClassTransfer, userSetAbstraction, …) through the Signer: direct EIP-712
// with HL's HyperliquidSignTransaction domain. It injects signatureChainId and
// hyperliquidChain (excluded from hashing via the payloadTypes filter), matching
// go-hyperliquid's SignUserSignedAction.
func SignUserAction(signer keys.Signer, action map[string]any, payloadTypes []apitypes.Type, primaryType string, isMainnet bool) (SignatureResult, error) {
	action["signatureChainId"] = "0x66eee"
	action["hyperliquidChain"] = "Mainnet"
	if !isMainnet {
		action["hyperliquidChain"] = "Testnet"
	}
	chainID := math.HexOrDecimal256(*big.NewInt(421614))
	typedData := apitypes.TypedData{
		Domain: apitypes.TypedDataDomain{
			ChainId:           &chainID,
			Name:              "HyperliquidSignTransaction",
			Version:           "1",
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: apitypes.Types{
			primaryType: payloadTypes,
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		PrimaryType: primaryType,
		Message:     action,
	}
	return signInner(signer, typedData)
}
