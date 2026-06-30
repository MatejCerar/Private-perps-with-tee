package extension

import (
	"bytes"
	"testing"

	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/common"
)

func newTestExt() *Extension {
	seed := bytes.Repeat([]byte{0x01}, 32)
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	return NewWith(0, 0, &hl.FakeFactory{}, treasury, "0xTREASURY", seed)
}

func TestAccountForIsDeterministicAndCached(t *testing.T) {
	e := newTestExt()
	u := common.HexToAddress("0x1111111111111111111111111111111111111111")
	a1, err := e.accountFor(u)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := e.accountFor(u)
	if a1 != a2 {
		t.Fatal("expected cached account instance")
	}
	if a1.Address() == "" {
		t.Fatal("expected derived HL address")
	}
}
