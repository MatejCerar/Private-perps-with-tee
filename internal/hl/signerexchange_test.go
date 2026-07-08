package hl

import (
	"testing"

	"extension-scaffold/internal/hl/hlsign"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/crypto"
	gohl "github.com/sonirico/go-hyperliquid"
)

func TestFloatToWire(t *testing.T) {
	cases := map[float64]string{
		906:      "906",
		0.014:    "0.014",
		1809.6:   "1809.6",
		5:        "5",
		0.5:      "0.5",
		2.301:    "2.301",
		100:      "100",
	}
	for in, want := range cases {
		got, err := floatToWire(in)
		if err != nil {
			t.Fatalf("floatToWire(%v): %v", in, err)
		}
		if got != want {
			t.Fatalf("floatToWire(%v) = %q, want %q", in, got, want)
		}
	}
}

// The signerExchange builds its order action from go-hyperliquid's own exported
// OrderAction/OrderWire types, then signs with hlsign. Signing that exact action
// with hlsign (our Signer path) and with gohl.SignL1Action (upstream) must be
// byte-identical — proving the action we put on the wire is signed exactly as
// go-hyperliquid would sign the same action.
func TestOrderActionSigningMatchesUpstream(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer := keys.NewRawSigner(priv)

	action := gohl.OrderAction{
		Type: "order",
		Orders: []gohl.OrderWire{{
			Asset:      4,
			IsBuy:      true,
			LimitPx:    "906",
			Size:       "0.014",
			ReduceOnly: false,
			OrderType:  gohl.OrderWireType{Limit: &gohl.OrderWireTypeLimit{Tif: gohl.TifGtc}},
		}},
		Grouping: "na",
	}
	const nonce int64 = 1_700_000_000_123

	for _, mainnet := range []bool{false, true} {
		mine, err := hlsign.SignL1Action(signer, action, "", nonce, nil, mainnet)
		if err != nil {
			t.Fatal(err)
		}
		up, err := gohl.SignL1Action(priv, action, "", nonce, nil, mainnet)
		if err != nil {
			t.Fatal(err)
		}
		if mine != hlsign.SignatureResult(up) {
			t.Fatalf("order-action sig mismatch (mainnet=%v):\n mine=%+v\n up  =%+v", mainnet, mine, up)
		}
	}
}
