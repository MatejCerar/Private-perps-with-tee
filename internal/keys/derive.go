// Package keys derives deterministic per-user Hyperliquid signing keys
// from a TEE-held master seed. Same (seed, user) always yields the same key.
package keys

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/hkdf"
)

// MustHexSeed decodes a hex master seed and requires at least 32 bytes.
func MustHexSeed(hexSeed string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexSeed, "0x"))
	if err != nil {
		return nil, fmt.Errorf("seed not hex: %w", err)
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("seed must be >= 32 bytes, got %d", len(b))
	}
	return b, nil
}

// DeriveUserKey derives the secp256k1 private key for a user via HKDF-SHA256.
// A counter byte appended to the info string handles the rare case where the
// derived 32 bytes are not a valid secp256k1 scalar.
func DeriveUserKey(masterSeed []byte, user common.Address) (*ecdsa.PrivateKey, error) {
	base := "hl-account:" + strings.ToLower(user.Hex())
	for counter := byte(0); counter < 255; counter++ {
		info := append([]byte(base), counter)
		r := hkdf.New(sha256.New, masterSeed, nil, info)
		out := make([]byte, 32)
		if _, err := io.ReadFull(r, out); err != nil {
			return nil, err
		}
		key, err := crypto.ToECDSA(out) // validates range [1, n-1]
		if err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("failed to derive valid key for %s", user.Hex())
}

// DeriveShardKey derives the key for one of a user's split "shard" accounts.
// Shard 0 is the user's primary account (identical to DeriveUserKey) so nothing
// already funded moves when splitting is enabled; shards >= 1 are fresh, unrelated
// accounts. Same (seed, user, shardIndex) always yields the same key.
func DeriveShardKey(masterSeed []byte, user common.Address, shardIndex int) (*ecdsa.PrivateKey, error) {
	if shardIndex <= 0 {
		return DeriveUserKey(masterSeed, user)
	}
	base := fmt.Sprintf("hl-shard:%s:%d", strings.ToLower(user.Hex()), shardIndex)
	for counter := byte(0); counter < 255; counter++ {
		info := append([]byte(base), counter)
		r := hkdf.New(sha256.New, masterSeed, nil, info)
		out := make([]byte, 32)
		if _, err := io.ReadFull(r, out); err != nil {
			return nil, err
		}
		key, err := crypto.ToECDSA(out)
		if err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("failed to derive valid shard key for %s #%d", user.Hex(), shardIndex)
}
