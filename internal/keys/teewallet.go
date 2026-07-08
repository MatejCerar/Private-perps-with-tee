package keys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TeeWalletKeyer resolves per-user signers to keys sealed inside the TEE,
// reached over the tee-node sign server (HL_KEY_MODE=tee-wallet). The extension
// never holds these private keys: keys are generated in-enclave by an on-chain
// KEY_GENERATE instruction, and this client only asks the node for a public key
// (/key-info) or a signature (/sign). This is the PMW custody model for C9.
type TeeWalletKeyer struct {
	client   *SignClient
	registry WalletRegistry
}

// WalletRegistry maps a Flare user to their provisioned (walletID, keyID). The
// provisioning flow (create project → wallet → addKey) populates it from the
// on-chain wallet events.
type WalletRegistry interface {
	Lookup(user common.Address) (walletID common.Hash, keyID uint64, ok bool)
}

// NewTeeWalletKeyer builds a Keyer over the sign server at signURL using the
// given user→wallet registry.
func NewTeeWalletKeyer(signURL string, registry WalletRegistry) *TeeWalletKeyer {
	return &TeeWalletKeyer{client: NewSignClient(signURL), registry: registry}
}

func (k *TeeWalletKeyer) UserSigner(user common.Address) (Signer, error) {
	wID, kID, ok := k.registry.Lookup(user)
	if !ok {
		return nil, fmt.Errorf("no TEE wallet provisioned for user %s", user.Hex())
	}
	addr, err := k.client.KeyAddress(wID, kID)
	if err != nil {
		return nil, fmt.Errorf("resolving wallet address for %s: %w", user.Hex(), err)
	}
	return &teeWalletSigner{client: k.client, walletID: wID, keyID: kID, addr: addr}, nil
}

// teeWalletSigner signs via POST /sign/{walletID}/{keyID}. The address is
// resolved once (at construction) from /key-info.
type teeWalletSigner struct {
	client   *SignClient
	walletID common.Hash
	keyID    uint64
	addr     common.Address
}

func (s *teeWalletSigner) Address() common.Address { return s.addr }

func (s *teeWalletSigner) SignPreimage(preimage []byte) ([]byte, error) {
	return s.client.Sign(s.walletID, s.keyID, preimage)
}

// SignClient is a thin HTTP client for the tee-node extension sign server
// (localhost, SIGN_PORT). Wire format mirrors tee-node's types.SignRequest /
// SignResponse: a []byte field marshals as base64, so {"message":"<b64>"} in,
// {"signature":"<b64>"} out.
type SignClient struct {
	baseURL string
	http    *http.Client
}

// NewSignClient returns a SignClient for the sign server base URL
// (e.g. "http://127.0.0.1:7701").
func NewSignClient(baseURL string) *SignClient {
	return &SignClient{baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

type signRequestBody struct {
	Message []byte `json:"message"`
}

type signResponseBody struct {
	Message   []byte `json:"message"`
	Signature []byte `json:"signature"`
}

// keyInfoBody is the subset of the /key-info key-existence proof we need: the
// 64-byte uncompressed public key (X‖Y, no 0x04 prefix).
type keyInfoBody struct {
	PublicKey []byte `json:"PublicKey"`
}

// KeyAddress fetches the wallet key's public key from /key-info and derives its
// EVM/HL address (keccak256(X‖Y)[12:]).
func (c *SignClient) KeyAddress(walletID common.Hash, keyID uint64) (common.Address, error) {
	url := fmt.Sprintf("%s/key-info/%s/%d", c.baseURL, walletID.Hex(), keyID)
	resp, err := c.http.Get(url)
	if err != nil {
		return common.Address{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return common.Address{}, fmt.Errorf("key-info %d: status %d: %s", keyID, resp.StatusCode, string(raw))
	}
	var ki keyInfoBody
	if err := json.Unmarshal(raw, &ki); err != nil {
		return common.Address{}, fmt.Errorf("decoding key-info: %w", err)
	}
	return PubKeyBytesToAddress(ki.PublicKey)
}

// Sign posts the preimage to /sign/{walletID}/{keyID} and returns the 65-byte
// [R‖S‖V] signature of keccak256(preimage) (the server applies keccak256).
func (c *SignClient) Sign(walletID common.Hash, keyID uint64, preimage []byte) ([]byte, error) {
	body, err := json.Marshal(signRequestBody{Message: preimage})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/sign/%s/%d", c.baseURL, walletID.Hex(), keyID)
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sign key %d: status %d: %s", keyID, resp.StatusCode, string(raw))
	}
	var sr signResponseBody
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, fmt.Errorf("decoding sign response: %w", err)
	}
	if len(sr.Signature) != 65 {
		return nil, fmt.Errorf("sign key %d: expected 65-byte signature, got %d", keyID, len(sr.Signature))
	}
	return sr.Signature, nil
}

// PubKeyBytesToAddress derives the EVM address from a 64-byte uncompressed
// secp256k1 public key (X‖Y, the tee-node key-existence format).
func PubKeyBytesToAddress(pub []byte) (common.Address, error) {
	if len(pub) != 64 {
		return common.Address{}, fmt.Errorf("expected 64-byte public key, got %d", len(pub))
	}
	// keccak256 over X‖Y, take the low 20 bytes — the standard EVM address rule.
	return common.BytesToAddress(crypto.Keccak256(pub)[12:]), nil
}
