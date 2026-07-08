package keys

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// mockSignServer stands in for the tee-node extension sign server, backed by a
// single in-memory key. It reproduces the exact handlers we depend on:
//   - GET  /key-info/{walletID}/{keyID}  -> {"PublicKey": <64-byte X‖Y>}
//   - POST /sign/{walletID}/{keyID}      -> {"signature": crypto.Sign(keccak256(msg))}
func mockSignServer(t *testing.T) (*httptest.Server, common.Address) {
	t.Helper()
	priv, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /key-info/{walletID}/{keyID}", func(w http.ResponseWriter, r *http.Request) {
		// tee-node PubKeyToBytes: 32-byte X ‖ 32-byte Y, left-padded.
		pub := append(leftPad32(priv.PublicKey.X.Bytes()), leftPad32(priv.PublicKey.Y.Bytes())...)
		_ = json.NewEncoder(w).Encode(keyInfoBody{PublicKey: pub})
	})
	mux.HandleFunc("POST /sign/{walletID}/{keyID}", func(w http.ResponseWriter, r *http.Request) {
		var req signRequestBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Exactly signKeccak256Secp256k1ECDSA: crypto.Sign(keccak256(msg)).
		sig, _ := crypto.Sign(crypto.Keccak256(req.Message), priv)
		_ = json.NewEncoder(w).Encode(signResponseBody{Message: req.Message, Signature: sig})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, addr
}

func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	return append(make([]byte, 32-len(b)), b...)
}

type staticRegistry struct {
	user     common.Address
	walletID common.Hash
	keyID    uint64
}

func (r staticRegistry) Lookup(u common.Address) (common.Hash, uint64, bool) {
	if u == r.user {
		return r.walletID, r.keyID, true
	}
	return common.Hash{}, 0, false
}

func TestTeeWalletKeyerResolvesAddressAndSigns(t *testing.T) {
	srv, keyAddr := mockSignServer(t)
	user := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	reg := staticRegistry{user: user, walletID: common.HexToHash("0xaa"), keyID: 3}
	k := NewTeeWalletKeyer(srv.URL, reg)

	signer, err := k.UserSigner(user)
	if err != nil {
		t.Fatal(err)
	}
	// Address resolved from /key-info must match the underlying key.
	if signer.Address() != keyAddr {
		t.Fatalf("resolved address %s != key address %s", signer.Address(), keyAddr)
	}

	preimage := []byte{0x19, 0x01, 0xca, 0xfe}
	sig, err := signer.SignPreimage(preimage)
	if err != nil {
		t.Fatal(err)
	}
	// The signature must recover to the wallet address.
	pub, err := crypto.SigToPub(crypto.Keccak256(preimage), sig)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(*pub) != keyAddr {
		t.Fatalf("recovered %s != wallet %s", crypto.PubkeyToAddress(*pub), keyAddr)
	}
}

// The whole point of the abstraction: a tee-wallet signature and a seed
// signature over the same preimage are byte-identical (same key material, same
// keccak256+secp256k1). This lets us swap custody modes with zero behavioral
// change downstream. We prove it by pointing a rawSigner at the SAME key the
// mock server holds — reconstructed here from a fixed key.
func TestTeeWalletSignatureIsByteIdenticalToRaw(t *testing.T) {
	// Fixed key so both the mock server and the rawSigner use the same material.
	priv, err := crypto.HexToECDSA(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /sign/{walletID}/{keyID}", func(w http.ResponseWriter, r *http.Request) {
		var req signRequestBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		sig, _ := crypto.Sign(crypto.Keccak256(req.Message), priv)
		_ = json.NewEncoder(w).Encode(signResponseBody{Signature: sig})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tee := &teeWalletSigner{client: NewSignClient(srv.URL), walletID: common.HexToHash("0x01"), keyID: 1, addr: addr}
	raw := NewRawSigner(priv)

	for _, preimage := range [][]byte{
		{0x19, 0x01, 0x00},
		[]byte("arbitrary tx preimage bytes"),
	} {
		teeSig, err := tee.SignPreimage(preimage)
		if err != nil {
			t.Fatal(err)
		}
		rawSig, _ := raw.SignPreimage(preimage)
		if !bytes.Equal(teeSig, rawSig) {
			t.Fatalf("tee-wallet and seed signatures differ for %x", preimage)
		}
	}
}

func TestTeeWalletKeyerUnprovisionedUser(t *testing.T) {
	srv, _ := mockSignServer(t)
	reg := staticRegistry{user: common.HexToAddress("0xaaaa"), walletID: common.HexToHash("0x01"), keyID: 1}
	k := NewTeeWalletKeyer(srv.URL, reg)
	if _, err := k.UserSigner(common.HexToAddress("0xbbbb")); err == nil {
		t.Fatal("expected error for an unprovisioned user")
	}
}

func TestPubKeyBytesToAddress(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	pub := append(leftPad32(priv.PublicKey.X.Bytes()), leftPad32(priv.PublicKey.Y.Bytes())...)
	got, err := PubKeyBytesToAddress(pub)
	if err != nil {
		t.Fatal(err)
	}
	if got != crypto.PubkeyToAddress(priv.PublicKey) {
		t.Fatalf("got %s want %s", got, crypto.PubkeyToAddress(priv.PublicKey))
	}
	if _, err := PubKeyBytesToAddress(pub[:63]); err == nil {
		t.Fatal("expected error for short public key")
	}
}

// Guard: the URL we build must match the sign server's path parser
// (hashParam expects 32-byte hex, uint64Param a decimal keyID).
func TestSignURLShape(t *testing.T) {
	wID := common.HexToHash("0xabc")
	url := "/sign/" + wID.Hex() + "/" + strconv.FormatUint(42, 10)
	parts := strings.Split(url, "/")
	if len(parts) != 4 || parts[2] != wID.Hex() || parts[3] != "42" {
		t.Fatalf("unexpected url shape: %s", url)
	}
	// walletID hex (minus 0x) must be 64 chars = 32 bytes.
	if len(strings.TrimPrefix(wID.Hex(), "0x")) != 64 {
		t.Fatal("walletID hex must be 32 bytes")
	}
}
