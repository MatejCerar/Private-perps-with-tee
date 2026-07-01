//go:build hltestnet

package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"testing"
	"time"

	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	gohl "github.com/sonirico/go-hyperliquid"
)

const hlTestnet = "https://api.hyperliquid-testnet.xyz"

// setAbstraction sends a raw userSetAbstraction action to switch an account's
// abstraction mode: "disabled" (standard), "unifiedAccount", or "portfolioMargin".
// Returns HL's raw /exchange response.
func setAbstraction(t *testing.T, privKeyHex, mode string) string {
	t.Helper()
	pk, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(pk.PublicKey).Hex()
	nonce := time.Now().UnixMilli()
	action := map[string]any{
		"type":        "userSetAbstraction",
		"user":        addr,
		"abstraction": mode,
		"nonce":       big.NewInt(nonce),
	}
	payloadTypes := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"},
		{Name: "user", Type: "address"},
		{Name: "abstraction", Type: "string"},
		{Name: "nonce", Type: "uint64"},
	}
	sig, err := gohl.SignUserSignedAction(pk, action, payloadTypes, "HyperliquidTransaction:UserSetAbstraction", false)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"action": action, "nonce": nonce, "signature": sig}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(hlTestnet+"/exchange", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func queryAbstraction(t *testing.T, addr string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"type": "userAbstraction", "user": addr})
	resp, err := http.Post(hlTestnet+"/info", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestSetStandardThenSpotSend sets the treasury to standard mode, then tries a
// spotSend to a fresh derived account. If the transfer succeeds (rather than
// "Action disabled when unified account is active"), the abstraction toggle is
// the fix for per-user funding under unified accounts.
//
//	HL_TREASURY_KEY=... HL_MASTER_SEED=... go test -tags hltestnet ./testing/ -run TestSetStandardThenSpotSend -v
func TestSetStandardThenSpotSend(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	seedHex := os.Getenv("HL_MASTER_SEED")
	if tkHex == "" || seedHex == "" {
		t.Skip("set HL_TREASURY_KEY and HL_MASTER_SEED")
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	treasuryAddr := crypto.PubkeyToAddress(tk.PublicKey).Hex()

	ctx := context.Background()
	factory := hl.NewRealFactory(hlTestnet)
	treasury := factory.Account(tk, treasuryAddr)

	// Disabling unified account is rejected while positions/orders are open, so
	// flatten any open perp positions on the treasury first.
	pos, _ := treasury.Positions(ctx)
	for _, p := range pos.Perp {
		if p.Szi == 0 {
			continue
		}
		sz := p.Szi
		if sz < 0 {
			sz = -sz
		}
		if _, err := treasury.PlaceOrder(ctx, hl.OrderReq{
			Coin: p.Coin, Market: "perp", OrderType: "market", ReduceOnly: true, Size: sz,
		}); err != nil {
			t.Logf("close %s (%.4f): %v", p.Coin, sz, err)
		} else {
			t.Logf("closed %s position (%.4f)", p.Coin, sz)
		}
	}

	t.Logf("before: treasury abstraction = %s", queryAbstraction(t, treasuryAddr))
	t.Logf("setAbstraction(disabled) response: %s", setAbstraction(t, tkHex, "disabled"))
	t.Logf("after:  treasury abstraction = %s", queryAbstraction(t, treasuryAddr))

	seed, err := keys.MustHexSeed(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	uk, _ := keys.DeriveUserKey(seed, common.HexToAddress("0x000000000000000000000000000000000000dEaD"))
	uAddr := crypto.PubkeyToAddress(uk.PublicKey).Hex()

	if err := treasury.SendUSD(ctx, uAddr, 5); err != nil {
		t.Fatalf("spotSend after setAbstraction still failed: %v", err)
	}
	t.Logf("SUCCESS: spotSend $5 to %s went through", uAddr)
}
