package swap

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// TestABISelectors pins the packed selectors to the canonical Uniswap V3 ones.
// If the tuple layouts drift (field order, deadline present/absent, QuoterV2's
// amountIn-before-fee ordering), the selectors change and this catches it
// offline; the fork test is the end-to-end confirmation.
func TestABISelectors(t *testing.T) {
	cases := []struct {
		name, src, method, sel string
	}{
		{"classic", classicRouterABI, "exactInputSingle", "414bf389"},
		{"router02", router02ABI, "exactInputSingle", "04e45aaf"},
		{"quoterV2", quoterV2ABI, "quoteExactInputSingle", "c6a5026a"},
	}
	for _, tc := range cases {
		parsed, err := abi.JSON(strings.NewReader(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m, ok := parsed.Methods[tc.method]
		if !ok {
			t.Fatalf("%s: method %s missing", tc.name, tc.method)
		}
		if got := hex.EncodeToString(m.ID); got != tc.sel {
			t.Errorf("%s %s selector = %s, want %s (signature %s)", tc.name, tc.method, got, tc.sel, m.Sig)
		}
	}
}
