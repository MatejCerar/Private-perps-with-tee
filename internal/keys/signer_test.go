package keys

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

func TestRawSignerMatchesGethSign(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	s := NewRawSigner(priv)

	preimage := []byte("hello hyperliquid")
	got, err := s.SignPreimage(preimage)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := crypto.Sign(crypto.Keccak256(preimage), priv)
	if !bytes.Equal(got, want) {
		t.Fatalf("SignPreimage != crypto.Sign(keccak256): %x vs %x", got, want)
	}
	// The signature must recover to the signer's address (proves [R||S||V] form).
	pub, err := crypto.SigToPub(crypto.Keccak256(preimage), got)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(*pub) != s.Address() {
		t.Fatalf("recovered %s != signer %s", crypto.PubkeyToAddress(*pub), s.Address())
	}
}

// The tee-node sign server signs crypto.Sign(keccak256(msg)); a wallet key and a
// seed key over the same preimage must therefore be byte-identical. This asserts
// the exact server semantics our rawSigner mirrors.
func TestRawSignerMirrorsSignServerSemantics(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	s := NewRawSigner(priv)
	msg := []byte{0x19, 0x01, 0xde, 0xad, 0xbe, 0xef}

	got, err := s.SignPreimage(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly signKeccak256Secp256k1ECDSA in tee-node/pkg/wallets/signing.go.
	want, _ := crypto.Sign(crypto.Keccak256(msg), priv)
	if !bytes.Equal(got, want) {
		t.Fatal("rawSigner does not mirror the sign server's EVMSignAlgo")
	}
}

func TestSeedKeyerDeterministicAndDistinct(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	k := NewSeedKeyer(seed)
	u1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	u2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	s1a, err := k.UserSigner(u1)
	if err != nil {
		t.Fatal(err)
	}
	s1b, _ := k.UserSigner(u1)
	s2, _ := k.UserSigner(u2)

	if s1a.Address() != s1b.Address() {
		t.Fatal("same user must derive the same address")
	}
	if s1a.Address() == s2.Address() {
		t.Fatal("distinct users must derive distinct addresses")
	}
	// Address must match direct derivation.
	priv, _ := DeriveUserKey(seed, u1)
	if s1a.Address() != crypto.PubkeyToAddress(priv.PublicKey) {
		t.Fatal("Keyer address != DeriveUserKey address")
	}
}

// SignLegacyTx via the preimage path must produce exactly what go-ethereum's
// native types.SignTx produces — this validates the reconstructed EIP-155 RLP
// preimage, which is what the tee-wallet path will send to the sign server.
func TestSignLegacyTxMatchesGeth(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	s := NewRawSigner(priv)
	chainID := big.NewInt(421614) // Arbitrum Sepolia

	to := common.HexToAddress("0x08cfc1B6b2dCF36A1480b99353A354AA8AC56f89")
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    7,
		To:       &to,
		Value:    big.NewInt(0),
		Gas:      120000,
		GasPrice: big.NewInt(100_000_000),
		Data:     []byte{0xa9, 0x05, 0x9c, 0xbb, 0x01, 0x02},
	})

	signer := types.LatestSignerForChainID(chainID)
	native, err := types.SignTx(tx, signer, priv)
	if err != nil {
		t.Fatal(err)
	}
	viaSigner, err := SignLegacyTx(tx, chainID, s)
	if err != nil {
		t.Fatal(err)
	}

	nb, _ := native.MarshalBinary()
	vb, _ := viaSigner.MarshalBinary()
	if !bytes.Equal(nb, vb) {
		t.Fatalf("signed tx bytes differ:\n native=%x\n signer=%x", nb, vb)
	}
	// Sender must recover to the signer's address.
	sender, err := types.Sender(signer, viaSigner)
	if err != nil {
		t.Fatal(err)
	}
	if sender != s.Address() {
		t.Fatalf("recovered sender %s != %s", sender, s.Address())
	}
}

func TestSignLegacyTxRejectsNonLegacy(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	s := NewRawSigner(priv)
	to := common.HexToAddress("0x01")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(421614), Nonce: 0, To: &to,
		Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
	})
	if _, err := SignLegacyTx(tx, big.NewInt(421614), s); err == nil {
		t.Fatal("expected SignLegacyTx to reject a dynamic-fee tx")
	}
}

// The EIP-712 preimage (0x1901 ‖ domain ‖ struct) must hash to the same digest
// go-ethereum computes for the typed data, and a rawSigner over that preimage
// must equal crypto.Sign(digest) — i.e. a valid EIP-712 signature. This is the
// shape every HL action (SetAbstraction, orders, withdraw3) signs.
func TestEIP712PreimageMatchesTypedDataDigest(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	s := NewRawSigner(priv)

	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
			},
			"Agent": {
				{Name: "source", Type: "string"},
				{Name: "connectionId", Type: "bytes32"},
			},
		},
		PrimaryType: "Agent",
		Domain: apitypes.TypedDataDomain{
			Name:    "Exchange",
			Version: "1",
			ChainId: ethmath.NewHexOrDecimal256(1337),
		},
		Message: apitypes.TypedDataMessage{
			"source":       "a",
			"connectionId": bytes32Hex(),
		},
	}

	domainSep, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		t.Fatal(err)
	}
	structHash, err := td.HashStruct(td.PrimaryType, td.Message)
	if err != nil {
		t.Fatal(err)
	}
	var ds, sh [32]byte
	copy(ds[:], domainSep)
	copy(sh[:], structHash)

	preimage := EIP712Preimage(ds, sh)

	// Cross-check: our preimage hashes to apitypes' own digest.
	digest, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(crypto.Keccak256(preimage), digest) {
		t.Fatalf("EIP712Preimage hash != TypedDataAndHash digest")
	}

	got, err := s.SignPreimage(preimage)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := crypto.Sign(digest, priv)
	if !bytes.Equal(got, want) {
		t.Fatal("EIP-712 signature via preimage != crypto.Sign(digest)")
	}
}

// helpers -------------------------------------------------------------------

func bytes32Hex() string {
	var b [32]byte
	b[31] = 0x01
	return "0x" + common.Bytes2Hex(b[:])
}
