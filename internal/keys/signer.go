package keys

import (
	"bytes"
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// Signer is one signing identity (a user's HL account, or the Arbitrum reserve).
// It hides WHERE the private key lives: an in-process seed-derived key
// (SeedKeyer, today) or a key sealed inside the TEE reached over the sign server
// (TeeWalletKeyer, PMW custody). The extension only ever needs an address and a
// signature, never the key itself — which is what makes TEE custody possible.
//
// SignPreimage returns secp256k1(keccak256(preimage)) as a 65-byte [R‖S‖V]
// recovery-id signature. It takes the PRE-image (the bytes that get hashed), not
// a pre-computed digest, because the tee-node sign server always applies
// keccak256 itself (EVMSignAlgo = crypto.Sign(keccak256(msg))). Everything we
// sign fits this shape once the right preimage is supplied:
//
//   - EIP-712 (HL actions):   preimage = 0x19 0x01 ‖ domainSeparator ‖ structHash
//   - Arbitrum legacy tx:     preimage = RLP(nonce,gasPrice,gas,to,value,data,chainID,0,0)
type Signer interface {
	Address() common.Address
	SignPreimage(preimage []byte) ([]byte, error)
}

// Keyer produces the Signer for a given Flare user. SeedKeyer derives it from
// the master seed; TeeWalletKeyer resolves it to a TEE-custodied wallet key.
type Keyer interface {
	UserSigner(user common.Address) (Signer, error)
}

// rawSigner signs with an in-process *ecdsa.PrivateKey (seed-derived user key or
// the reserve key). It reproduces the tee-node sign server's EVMSignAlgo exactly
// so a seed-mode and a tee-wallet-mode signature over the same preimage are
// byte-identical.
type rawSigner struct {
	priv *ecdsa.PrivateKey
	addr common.Address
}

// NewRawSigner wraps an in-process private key as a Signer.
func NewRawSigner(priv *ecdsa.PrivateKey) Signer {
	return &rawSigner{priv: priv, addr: crypto.PubkeyToAddress(priv.PublicKey)}
}

func (s *rawSigner) Address() common.Address { return s.addr }

func (s *rawSigner) SignPreimage(preimage []byte) ([]byte, error) {
	return crypto.Sign(crypto.Keccak256(preimage), s.priv)
}

// SeedKeyer derives per-user signers from a TEE-held master seed (today's
// custody model — HL_KEY_MODE=seed).
type SeedKeyer struct {
	masterSeed []byte
}

// NewSeedKeyer returns a Keyer backed by the master seed.
func NewSeedKeyer(masterSeed []byte) *SeedKeyer { return &SeedKeyer{masterSeed: masterSeed} }

func (k *SeedKeyer) UserSigner(user common.Address) (Signer, error) {
	priv, err := DeriveUserKey(k.masterSeed, user)
	if err != nil {
		return nil, err
	}
	return NewRawSigner(priv), nil
}

// EIP712Preimage builds the 66-byte EIP-712 signing preimage
// (0x19 0x01 ‖ domainSeparator ‖ structHash). keccak256 of it is the EIP-712
// digest; hand it to Signer.SignPreimage.
func EIP712Preimage(domainSeparator, structHash [32]byte) []byte {
	p := make([]byte, 0, 2+32+32)
	p = append(p, 0x19, 0x01)
	p = append(p, domainSeparator[:]...)
	p = append(p, structHash[:]...)
	return p
}

// SignLegacyTx signs a legacy Arbitrum transaction through a Signer and returns
// the signed tx. It reconstructs the EIP-155 signing preimage
// (RLP(nonce,gasPrice,gas,to,value,data,chainID,0,0)) and — critically —
// verifies that keccak256(preimage) equals go-ethereum's own tx signing hash
// before trusting it. That guard means a subtly-wrong preimage fails loudly
// instead of producing a signature that recovers to the wrong address; and once
// it passes, the tee-wallet path is guaranteed correct because it applies the
// identical keccak256+sign to the identical preimage.
func SignLegacyTx(tx *types.Transaction, chainID *big.Int, s Signer) (*types.Transaction, error) {
	if tx.Type() != types.LegacyTxType {
		return nil, fmt.Errorf("SignLegacyTx: expected legacy tx, got type %d", tx.Type())
	}
	signer := types.LatestSignerForChainID(chainID)
	preimage, err := rlp.EncodeToBytes([]interface{}{
		tx.Nonce(), tx.GasPrice(), tx.Gas(), tx.To(), tx.Value(), tx.Data(),
		chainID, uint(0), uint(0),
	})
	if err != nil {
		return nil, fmt.Errorf("rlp-encoding tx preimage: %w", err)
	}
	if !bytes.Equal(crypto.Keccak256(preimage), signer.Hash(tx).Bytes()) {
		return nil, fmt.Errorf("tx signing preimage does not hash to the expected digest (chainID/tx-type mismatch?)")
	}
	sig, err := s.SignPreimage(preimage)
	if err != nil {
		return nil, fmt.Errorf("signing tx: %w", err)
	}
	return tx.WithSignature(signer, sig)
}
