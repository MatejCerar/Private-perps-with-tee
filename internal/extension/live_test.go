//go:build hltestnet

package extension

import (
	"context"
	"os"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestLiveDepositThenPerpOrder exercises the REAL handlers (processDeposit then
// processPlaceOrder) against Hyperliquid testnet — not just the adapter. It
// validates the integrated deposit -> spot->perp sweep -> perp order path,
// including the handler's "sweep the whole spot balance" behavior.
//
//	HL_TREASURY_KEY=... HL_MASTER_SEED=... go test -tags hltestnet ./internal/extension/ -run TestLiveDepositThenPerpOrder -v
func TestLiveDepositThenPerpOrder(t *testing.T) {
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
	const url = "https://api.hyperliquid-testnet.xyz"
	factory := hl.NewRealFactory(url)
	treasuryAddr := crypto.PubkeyToAddress(tk.PublicKey).Hex()
	treasury := factory.Account(tk, treasuryAddr)

	// Ensure the treasury can transfer (standard mode). Best-effort: no-op if
	// already standard; only fails with open positions.
	if err := treasury.SetAbstraction(context.Background(), hl.AbstractionStandard); err != nil {
		t.Logf("treasury SetAbstraction (continuing): %v", err)
	}

	e := NewWith(0, 0, factory, treasury, treasuryAddr, seed)

	user := common.HexToAddress("0x000000000000000000000000000000000000bEEF")

	// DEPOSIT $30 through the real handler (treasury spotSend -> user spot).
	depMsg := envelope(t, user, types.DepositRequest{UsdAmount: 30})
	depAction, depDF := actionFor(depMsg)
	depRes := e.processDeposit(depAction, depDF)
	t.Logf("deposit: status=%d log=%s data=%s", depRes.Status, depRes.Log, string(depRes.Data))
	if depRes.Status != 1 {
		t.Fatalf("deposit failed: %s", depRes.Log)
	}

	// PLACE_ORDER (perp market) through the real handler — this triggers the
	// spot->perp sweep before placing.
	ordMsg := envelope(t, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "market", Size: 0.02, Leverage: 2,
	})
	ordAction, ordDF := actionFor(ordMsg)
	ordRes := e.processPlaceOrder(ordAction, ordDF)
	t.Logf("order: status=%d log=%s data=%s", ordRes.Status, ordRes.Log, string(ordRes.Data))
	if ordRes.Status != 1 {
		t.Fatalf("place order failed: %s", ordRes.Log)
	}

	// Read the position back from the user's own HL account.
	acct, _ := e.accountFor(user)
	pos, err := acct.Positions(context.Background())
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	t.Logf("user HL account %s positions: %+v", acct.Address(), pos)
}
