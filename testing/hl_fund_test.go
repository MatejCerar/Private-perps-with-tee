//go:build hltestnet

package testing

import (
	"context"
	"os"
	"strconv"
	"testing"

	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/crypto"
)

// TestFundTreasuryPerp moves USDC from the treasury's spot wallet into its perp
// (Core) wallet, which is the balance usdSend spends. Run once to seed the
// treasury before the round-trip test:
//
//	HL_TREASURY_KEY=... HL_MOVE_TO_PERP=100 go test -tags hltestnet ./testing/ -run TestFundTreasuryPerp -v
func TestFundTreasuryPerp(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	amtStr := os.Getenv("HL_MOVE_TO_PERP")
	if tkHex == "" || amtStr == "" {
		t.Skip("set HL_TREASURY_KEY and HL_MOVE_TO_PERP (USDC amount to move spot->perp)")
	}
	amt, err := strconv.ParseFloat(amtStr, 64)
	if err != nil {
		t.Fatalf("HL_MOVE_TO_PERP: %v", err)
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	factory := hl.NewRealFactory("https://api.hyperliquid-testnet.xyz")
	addr := crypto.PubkeyToAddress(tk.PublicKey).Hex()
	treasury := factory.Account(tk, addr)

	if err := treasury.ClassTransfer(context.Background(), amt, true); err != nil {
		t.Fatalf("class transfer spot->perp: %v", err)
	}
	t.Logf("moved %.2f USDC spot->perp on %s", amt, addr)
}

// TestTreasuryPlaceOrder places a small perp order directly from the treasury
// account. It proves the core HL adapter path (metadata init, order placement,
// position read) works against live testnet, independent of the per-user
// funding model. A unified-account treasury trades perps from its single
// balance, so no spot->perp transfer is needed.
//
//	HL_TREASURY_KEY=... go test -tags hltestnet ./testing/ -run TestTreasuryPlaceOrder -v
func TestTreasuryPlaceOrder(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	if tkHex == "" {
		t.Skip("set HL_TREASURY_KEY")
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	factory := hl.NewRealFactory("https://api.hyperliquid-testnet.xyz")
	addr := crypto.PubkeyToAddress(tk.PublicKey).Hex()
	acct := factory.Account(tk, addr)

	// 0.02 ETH ~ $32 on testnet — safely above HL's $10 minimum order value.
	res, err := acct.PlaceOrder(ctx, hl.OrderReq{
		Coin: "ETH", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.02, Leverage: 2,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	t.Logf("order result: %+v", res)

	pos, err := acct.Positions(ctx)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	t.Logf("positions: %+v", pos)
}
