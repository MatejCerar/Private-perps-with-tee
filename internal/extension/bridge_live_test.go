//go:build hltestnet

package extension

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
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

// orderStatus returns the recorded status of the most recent ledger order with
// the given HL OrderID, or "" if none is recorded.
func orderStatus(e *Extension, user common.Address, oid int64) string {
	for _, r := range e.led.SnapshotFor(user).RecentOrders {
		if r.OrderID == oid {
			return r.Status
		}
	}
	return ""
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

// TestBridgeHandlersLive is the consolidated Phase-1 live gate. It drives the
// real extension action handlers in bridge funding mode against Arbitrum Sepolia
// + HL testnet and asserts, in one pass, the live behavior each unit-tested task
// left as a "remaining testnet gate":
//
//	deposit      [1.6] Bridge2 deposit funds the account through the reserve.
//	unified      [1.2] the fresh account was switched to HL unified mode live
//	             (SetAbstraction(unifiedAccount) accepted on a flat account).
//	order        [1.2/1.3] a perp order fills from the single deposit — no sweep.
//	limit_cancel [1.3] a resting limit order + CANCEL reflect resting→cancelled.
//	spot         [1.2/1.3] a spot order draws from the same unified pool (tunable).
//	withdraw     [1.6] withdraw3 returns funds to the reserve.
//	close        leaves the perp position flat for clean re-runs.
//	reconcile    [1.5] ReconcileAll converges the ledger to real HL equity, then a
//	             simulated late-deposit-credit drift (real > book) is healed upward.
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

	// [1.2] The deposit handler switched the fresh account to HL unified mode
	// (ensureUnified). Membership in unifiedSet means SetAbstraction(unifiedAccount)
	// was accepted live on the flat account — the key 1.2 risk. Perp + spot then
	// fill from the single collateral pool (order/spot subtests) with no class
	// transfer. If this fails, move the SetAbstraction call to after the first
	// credit (see the plan's Task 1.2 gate note).
	t.Run("unified", func(t *testing.T) {
		if _, ok := e.unifiedSet.Load(user); !ok {
			t.Fatal("account not switched to unified: SetAbstraction(unifiedAccount) was rejected on the fresh account")
		}
		acct, err := e.accountFor(user)
		if err != nil {
			t.Fatal(err)
		}
		total, err := hlTotalUSD(ctx, acct)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("unified confirmed: SetAbstraction(unified) accepted; HL equity=%.4f (single pool)", total)
	})

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

	// A far-from-mid limit buy rests on the book; capture its OrderID, cancel it
	// through the handler, and confirm the ledger reflects resting -> cancelled.
	t.Run("limit_cancel", func(t *testing.T) {
		msg := envelope(t, user, types.PlaceOrderRequest{
			Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "limit", Size: 0.01, LimitPx: 100,
		})
		a, df := actionFor(msg)
		res := e.processPlaceOrder(a, df)
		if res.Status != 1 {
			t.Fatalf("limit order status=%d log=%s", res.Status, res.Log)
		}
		var ord types.OrderResponse
		if err := json.Unmarshal(res.Data, &ord); err != nil {
			t.Fatal(err)
		}
		if ord.OrderID == 0 {
			t.Fatalf("expected a resting OrderID (order may have filled): %s", string(res.Data))
		}
		if st := orderStatus(e, user, ord.OrderID); st != "resting" {
			t.Fatalf("ledger status for %d = %q, want resting", ord.OrderID, st)
		}
		cmsg := envelope(t, user, types.CancelRequest{Coin: "ETH", OrderID: ord.OrderID})
		ca, cdf := actionFor(cmsg)
		if cres := e.processCancel(ca, cdf); cres.Status != 1 {
			t.Fatalf("cancel status=%d log=%s", cres.Status, cres.Log)
		}
		if st := orderStatus(e, user, ord.OrderID); st != "cancelled" {
			t.Fatalf("ledger status for %d = %q, want cancelled", ord.OrderID, st)
		}
		t.Logf("limit rest+cancel ok: oid=%d", ord.OrderID)
	})

	// Spot buy through the handler. Testnet spot pairs/liquidity and the $10 min
	// order value vary, so tune via SPOT_COIN/SPOT_SIZE/SPOT_PX and treat a
	// rejection as non-fatal — this subtest documents/drives the spot path.
	t.Run("spot", func(t *testing.T) {
		coin := liveEnv("SPOT_COIN", "PURR/USDC")
		size, _ := strconv.ParseFloat(liveEnv("SPOT_SIZE", "250"), 64)
		px, _ := strconv.ParseFloat(liveEnv("SPOT_PX", "0.05"), 64)
		msg := envelope(t, user, types.PlaceOrderRequest{
			Market: "spot", Coin: coin, IsBuy: true, OrderType: "limit", Size: size, LimitPx: px,
		})
		a, df := actionFor(msg)
		res := e.processPlaceOrder(a, df)
		if res.Status != 1 {
			t.Logf("spot order status=%d log=%s (non-fatal: testnet spot pair/liquidity/min-value varies; tune SPOT_COIN/SPOT_SIZE/SPOT_PX)", res.Status, res.Log)
			return
		}
		t.Logf("spot order ok: %s", string(res.Data))
		// Cancel it back out so the account stays clean for re-runs.
		var ord types.OrderResponse
		if json.Unmarshal(res.Data, &ord) == nil && ord.OrderID != 0 {
			cmsg := envelope(t, user, types.CancelRequest{Coin: coin, OrderID: ord.OrderID})
			ca, cdf := actionFor(cmsg)
			if cres := e.processCancel(ca, cdf); cres.Status == 1 {
				t.Logf("spot resting order %d cancelled", ord.OrderID)
			}
		}
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

	// [1.5] Reconciliation against real HL balances. After trading + the withdraw
	// fee, the ledger has drifted from real HL equity; ReconcileAll must converge
	// it. Then simulate the Task 1.6 late-deposit-credit drift (HL credited but the
	// handler missed it → real > book) and confirm Reconcile heals the ledger
	// upward — the exact scenario unit-tested by TestReconcileLateDepositCredit,
	// here against a real HL account. Runs last, with the position flat, so real
	// equity is stable (no open-position mark-price noise).
	t.Run("reconcile", func(t *testing.T) {
		acct, err := e.accountFor(user)
		if err != nil {
			t.Fatal(err)
		}
		const tol = 1.0 // USD; loose to absorb equity moving between reads live

		// Converge natural drift (P&L, ~1 USDC withdraw fee).
		e.ReconcileAll(ctx)
		real, err := hlTotalUSD(ctx, acct)
		if err != nil {
			t.Fatal(err)
		}
		book := e.led.Balance(user)
		if d := math.Abs(book - real); d > tol {
			t.Fatalf("after ReconcileAll, ledger=%.4f vs real=%.4f drift=%.4f > %.2f", book, real, d, tol)
		}
		t.Logf("reconcile converged: ledger=%.4f real=%.4f", book, real)

		// Simulate a deposit HL credited but the handler missed (ledger behind).
		const simDrift = 3.0
		e.led.Adjust(user, -simDrift)
		if behind := e.led.Balance(user); behind >= book {
			t.Fatalf("sim drift did not lower the ledger: %.4f (was %.4f)", behind, book)
		}
		delta, err := e.Reconcile(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if delta <= 0 {
			t.Fatalf("expected a positive heal delta for a behind ledger, got %.4f", delta)
		}
		healed := e.led.Balance(user)
		real2, err := hlTotalUSD(ctx, acct)
		if err != nil {
			t.Fatal(err)
		}
		if d := math.Abs(healed - real2); d > tol {
			t.Fatalf("after heal, ledger=%.4f vs real=%.4f drift=%.4f > %.2f", healed, real2, d, tol)
		}
		t.Logf("late-credit heal ok: delta=+%.4f ledger=%.4f real=%.4f", delta, healed, real2)
	})
}
