package stargate

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestAddressToBytes32(t *testing.T) {
	a := common.HexToAddress("0xabc4Cbf716472c47a61c8c2c5076895600F3cf10")
	b := AddressToBytes32(a)
	if hex.EncodeToString(b[:12]) != strings.Repeat("00", 12) {
		t.Fatalf("first 12 bytes must be zero padding: %x", b)
	}
	if common.BytesToAddress(b[12:]) != a {
		t.Fatalf("address roundtrip failed: %x", b)
	}
}

// TestABISelectors pins the packed selectors to LayerZero V2's canonical IOFT
// ones. If the tuple layouts in stargateABI drift (field order, missing field),
// the computed selectors change and this catches it offline — the live quote
// against a real pool is the end-to-end confirmation.
func TestABISelectors(t *testing.T) {
	parsed, err := abi.JSON(strings.NewReader(stargateABI))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"token":     "fc0c546a", // verified live against all four pools
		"quoteSend": "3b6f743b",
		"quoteOFT":  "0d35b415",
		"send":      "c7c7f5b3",
	}
	for name, sel := range want {
		m, ok := parsed.Methods[name]
		if !ok {
			t.Fatalf("method %s missing", name)
		}
		if got := hex.EncodeToString(m.ID); got != sel {
			t.Errorf("%s selector = %s, want %s (signature %s)", name, got, sel, m.Sig)
		}
	}
}

func TestPackSendParamRoundtrip(t *testing.T) {
	parsed, err := abi.JSON(strings.NewReader(stargateABI))
	if err != nil {
		t.Fatal(err)
	}
	sp := newSendParam(EIDArbitrumOne, common.HexToAddress("0x1234"), big.NewInt(5_000_000), big.NewInt(4_990_000))
	if len(sp.OftCmd) != 0 {
		t.Fatal("taxi mode requires empty oftCmd")
	}
	data, err := parsed.Pack("quoteSend", sp, false)
	if err != nil {
		t.Fatalf("pack quoteSend: %v", err)
	}
	if len(data) < 4 {
		t.Fatal("packed calldata too short")
	}
}
