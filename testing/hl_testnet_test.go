//go:build hltestnet

package testing

import (
	"context"
	"os"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Run with: HL_TREASURY_KEY=... HL_MASTER_SEED=... go test -tags hltestnet ./testing/ -run TestHLTestnetRoundTrip -v
func TestHLTestnetRoundTrip(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	seedHex := os.Getenv("HL_MASTER_SEED")
	if tkHex == "" || seedHex == "" {
		t.Skip("set HL_TREASURY_KEY and HL_MASTER_SEED")
	}
	seed, err := keys.MustHexSeed(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	factory := hl.NewRealFactory("https://api.hyperliquid-testnet.xyz")
	treasuryAddr := crypto.PubkeyToAddress(tk.PublicKey).Hex()
	treasury := factory.Account(tk, treasuryAddr)

	user := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	uk, _ := keys.DeriveUserKey(seed, user)
	uAddr := crypto.PubkeyToAddress(uk.PublicKey).Hex()
	uAcct := factory.Account(uk, uAddr)

	if err := treasury.SendUSD(ctx, uAddr, 15); err != nil {
		t.Fatalf("fund user from treasury: %v", err)
	}
	// Perp market buy, min ~$10 notional.
	if _, err := uAcct.PlaceOrder(ctx, hl.OrderReq{
		Coin: "ETH", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.005, Leverage: 2,
	}); err != nil {
		t.Fatalf("place order: %v", err)
	}
	pos, err := uAcct.Positions(ctx)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	t.Logf("positions: %+v", pos)
}
