package keys

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var seed = bytes.Repeat([]byte{0xAB}, 32)

func TestDeriveDeterministic(t *testing.T) {
	u := common.HexToAddress("0x1111111111111111111111111111111111111111")
	k1, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(k1.PublicKey) != crypto.PubkeyToAddress(k2.PublicKey) {
		t.Fatal("derivation not deterministic")
	}
}

func TestDeriveDistinctUsers(t *testing.T) {
	a := common.HexToAddress("0x1111111111111111111111111111111111111111")
	b := common.HexToAddress("0x2222222222222222222222222222222222222222")
	ka, _ := DeriveUserKey(seed, a)
	kb, _ := DeriveUserKey(seed, b)
	if crypto.PubkeyToAddress(ka.PublicKey) == crypto.PubkeyToAddress(kb.PublicKey) {
		t.Fatal("distinct users produced same key")
	}
}

func TestDeriveValidKey(t *testing.T) {
	u := common.HexToAddress("0x3333333333333333333333333333333333333333")
	k, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	if k.D.Sign() == 0 {
		t.Fatal("zero scalar")
	}
}
