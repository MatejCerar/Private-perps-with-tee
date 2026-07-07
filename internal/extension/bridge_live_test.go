//go:build hltestnet

package extension

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func liveEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ensureReserveUSDC tops up the Arbitrum reserve to >= minUSD by moving spot->perp
// and withdraw3-ing out of the reserve's HL balance, so the deposit handler has
// USDC to distribute. Mirrors the bootstrap in testing/arb_loop_test.go.
func ensureReserveUSDC(t *testing.T, ctx context.Context, client *arb.Client, reserveHL hl.Account, reserveAddr common.Address, usdc common.Address, minUSD float64) {
	t.Helper()
	bal, err := client.USDCBalance(ctx, usdc, reserveAddr)
	if err != nil {
		t.Fatalf("reserve USDC balance: %v", err)
	}
	have := arb.UnitsToUSD(bal)
	if have >= minUSD {
		t.Logf("reserve already holds %.2f USDC (>= %.2f)", have, minUSD)
		return
	}
	move := minUSD - have + 3
	t.Logf("topping up reserve: %.2f USDC spot->perp, then withdraw3 -> Arbitrum", move)
	if err := reserveHL.ClassTransfer(ctx, move, true); err != nil {
		t.Fatalf("reserve spot->perp: %v", err)
	}
	if err := reserveHL.Withdraw(ctx, reserveAddr.Hex(), move-2); err != nil {
		t.Fatalf("reserve withdraw3: %v — is the reserve's HL testnet account funded (/drip)?", err)
	}
	if _, err := client.WaitForUSDCBalance(ctx, usdc, reserveAddr, arb.USDToUnits(minUSD-2), 10*time.Second, 6*time.Minute); err != nil {
		t.Fatalf("waiting for reserve top-up: %v", err)
	}
}

// TestBridgeHandlersLive drives the real extension action handlers in bridge
// funding mode against Arbitrum Sepolia + HL testnet: DEPOSIT (Bridge2) → ORDER
// (perp) → WITHDRAW (withdraw3) → close, asserting handler status + ledger.
//
//	HL_TREASURY_KEY=... [ARB_RESERVE_KEY=...] \
//	  go test -tags hltestnet ./internal/extension/ -run TestBridgeHandlersLive -v -timeout 30m
func TestBridgeHandlersLive(t *testing.T) {
	reserveHex := os.Getenv("ARB_RESERVE_KEY")
	if reserveHex == "" {
		reserveHex = os.Getenv("HL_TREASURY_KEY")
	}
	if reserveHex == "" {
		t.Skip("set HL_TREASURY_KEY (or ARB_RESERVE_KEY): reserve funded on HL testnet + Arbitrum Sepolia ETH")
	}
	reserveKey, err := crypto.HexToECDSA(strings.TrimPrefix(reserveHex, "0x"))
	if err != nil {
		t.Fatalf("reserve key: %v", err)
	}
	reserveAddr := crypto.PubkeyToAddress(reserveKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	client, err := arb.New(ctx, liveEnv("ARBITRUM_RPC_URL", "https://sepolia-rollup.arbitrum.io/rpc"))
	if err != nil {
		t.Fatalf("arb client: %v", err)
	}
	defer client.Close()

	usdc := common.HexToAddress(liveEnv("ARB_USDC_ADDRESS", "0x1baAbB04529D43a73232B713C0FE471f7c7334d5"))
	bridge := common.HexToAddress(liveEnv("HL_BRIDGE2_ADDRESS", "0x08cfc1B6b2dCF36A1480b99353A354AA8AC56f89"))

	factory := hl.NewRealFactory("https://api.hyperliquid-testnet.xyz")
	seed := crypto.FromECDSA(reserveKey) // deterministic throwaway user, per reserve
	e := NewWithBridge(0, 0, factory, seed, client, reserveKey, usdc, bridge)

	ethBal, err := client.ETHBalance(ctx, reserveAddr)
	if err != nil {
		t.Fatalf("reserve ETH: %v", err)
	}
	if ethBal.Sign() == 0 {
		t.Fatalf("reserve %s has no Arbitrum Sepolia ETH — fund it from a faucet", reserveAddr.Hex())
	}

	const (
		depositUSD  = 20.0
		withdrawUSD = 5.0
	)
	reserveHL := factory.Account(reserveKey, reserveAddr.Hex())
	ensureReserveUSDC(t, ctx, client, reserveHL, reserveAddr, usdc, depositUSD+10)

	user := common.HexToAddress("0x000000000000000000000000000000000000bEEF")

	if !t.Run("deposit", func(t *testing.T) {
		msg := envelope(t, user, types.DepositRequest{UsdAmount: depositUSD})
		a, df := actionFor(msg)
		res := e.processDeposit(a, df)
		if res.Status != 1 {
			t.Fatalf("deposit status=%d log=%s", res.Status, res.Log)
		}
		if e.led.Balance(user) != depositUSD {
			t.Fatalf("ledger balance=%v want=%v", e.led.Balance(user), depositUSD)
		}
		var resp types.DepositResponse
		if err := json.Unmarshal(res.Data, &resp); err != nil {
			t.Fatal(err)
		}
		t.Logf("deposit ok: hlAddr=%s ledger=%.2f", resp.HLAddress, resp.BalanceUsd)
	}) {
		t.Fatal("deposit failed; aborting")
	}

	t.Run("order", func(t *testing.T) {
		msg := envelope(t, user, types.PlaceOrderRequest{
			Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "market", Size: 0.01, Leverage: 2,
		})
		a, df := actionFor(msg)
		res := e.processPlaceOrder(a, df)
		if res.Status != 1 {
			t.Fatalf("order status=%d log=%s", res.Status, res.Log)
		}
		t.Logf("order ok: %s", string(res.Data))
	})

	t.Run("withdraw", func(t *testing.T) {
		msg := envelope(t, user, types.WithdrawRequest{UsdAmount: withdrawUSD})
		a, df := actionFor(msg)
		res := e.processWithdraw(a, df)
		if res.Status != 1 {
			t.Fatalf("withdraw status=%d log=%s", res.Status, res.Log)
		}
		if want := depositUSD - withdrawUSD; e.led.Balance(user) != want {
			t.Fatalf("ledger balance=%v want=%v", e.led.Balance(user), want)
		}
		t.Logf("withdraw ok: ledger=%.2f (withdraw3 -> reserve %s)", e.led.Balance(user), reserveAddr.Hex())
	})

	// Leave the throwaway user flat so re-runs start clean (avoids uPnL drift on
	// the next deposit's credit check).
	t.Run("close", func(t *testing.T) {
		msg := envelope(t, user, types.PlaceOrderRequest{
			Market: "perp", Coin: "ETH", OrderType: "market", ReduceOnly: true, Size: 0.01,
		})
		a, df := actionFor(msg)
		res := e.processPlaceOrder(a, df)
		if res.Status != 1 {
			t.Logf("close order status=%d log=%s (non-fatal)", res.Status, res.Log)
		}
	})
}
