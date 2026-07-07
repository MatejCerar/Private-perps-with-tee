package extension

import (
	"context"
	"math"
	"testing"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/common"
)

// setHL sets the fake HL equity for a user (deposits land in perp, so we model
// the whole balance as PerpAccountValueUSD).
func setHL(t *testing.T, e *Extension, userAddr common.Address, total float64) {
	t.Helper()
	acct, err := e.accountFor(userAddr)
	if err != nil {
		t.Fatal(err)
	}
	fa := acct.(*hl.FakeAccount)
	fa.PosSnapshot.PerpAccountValueUSD = total
	fa.PosSnapshot.SpotUSDC = 0
}

func TestReconcileLateDepositCredit(t *testing.T) {
	// The Task 1.6 drift: a deposit timed out (handler did not credit the
	// ledger), but HL credited it late. Reconciliation must now credit the ledger.
	shortCreditTimeout(t)
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	// No depositHook: HL never credits within the short timeout -> handler fails,
	// ledger stays 0 (TestBridgeDepositCreditTimeoutLeavesLedgerUncerdited).
	a, df := depositMsg(t, user, 20)
	if res := e.processDeposit(a, df); res.Status != 0 {
		t.Fatalf("expected timeout rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0 after timeout, got %v", e.led.Balance(user))
	}
	// Simulate HL crediting the deposit late.
	setHL(t, e, user, 20)
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta-20) > 1e-9 {
		t.Fatalf("delta=%v want +20", delta)
	}
	if e.led.Balance(user) != 20 {
		t.Fatalf("ledger=%v want 20 after reconcile", e.led.Balance(user))
	}
}

func TestReconcilePnLGain(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 20)
	setHL(t, e, user, 25) // +5 P&L
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta-5) > 1e-9 {
		t.Fatalf("delta=%v want +5", delta)
	}
	if e.led.Balance(user) != 25 {
		t.Fatalf("ledger=%v want 25", e.led.Balance(user))
	}
}

func TestReconcilePnLLoss(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 20)
	setHL(t, e, user, 12) // -8 P&L
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta+8) > 1e-9 {
		t.Fatalf("delta=%v want -8", delta)
	}
	if e.led.Balance(user) != 12 {
		t.Fatalf("ledger=%v want 12", e.led.Balance(user))
	}
}

func TestReconcileClampsAtZero(t *testing.T) {
	// A loss larger than the recorded balance (leveraged blowup / liquidation)
	// must clamp the ledger at 0, not go negative.
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 5)
	setHL(t, e, user, 0) // full loss
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta+5) > 1e-9 {
		t.Fatalf("delta=%v want -5", delta)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger=%v want 0 (clamped)", e.led.Balance(user))
	}
}

func TestReconcileWithdrawFeeDrift(t *testing.T) {
	// withdraw3 charges a ~1 USDC fee on top of the withdrawn amount, so after a
	// withdraw the real HL balance is 1 below the ledger. Reconciliation debits
	// the fee to the user.
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 20)
	setHL(t, e, user, 19) // -1 fee
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta+1) > 1e-9 {
		t.Fatalf("delta=%v want -1", delta)
	}
	if e.led.Balance(user) != 19 {
		t.Fatalf("ledger=%v want 19", e.led.Balance(user))
	}
}

func TestReconcileInToleranceNoop(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 20)
	setHL(t, e, user, 20.005) // 0.005 drift, within default 0.01 tolerance
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if delta != 0 {
		t.Fatalf("delta=%v want 0 (in tolerance)", delta)
	}
	if e.led.Balance(user) != 20 {
		t.Fatalf("ledger should be unchanged at 20, got %v", e.led.Balance(user))
	}
}

func TestReconcileSkipsBusyUser(t *testing.T) {
	// While an op holds the user lock, Reconcile must skip (TryLock) and leave
	// the ledger untouched — never block on a mid-deposit user.
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 20)
	setHL(t, e, user, 30) // would credit +10 if reconciled

	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	done := make(chan struct{})
	go func() {
		_, _ = e.Reconcile(context.Background(), user)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Reconcile blocked on a busy user lock (should TryLock+skip)")
	}
	// Unchanged because it was skipped.
	if e.led.Balance(user) != 20 {
		t.Fatalf("ledger should be unchanged while user is busy, got %v", e.led.Balance(user))
	}
}

func TestReconcileAll(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	u1, _ := userFake(t, e)
	e.led.Credit(u1, 20)
	setHL(t, e, u1, 25) // +5

	u2 := common.HexToAddress("0x000000000000000000000000000000000000B0B")
	if _, err := e.accountFor(u2); err != nil { // materialize so ReconcileAll sees it
		t.Fatal(err)
	}
	e.led.Credit(u2, 40)
	setHL(t, e, u2, 10) // -30

	results := e.ReconcileAll(context.Background())
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if e.led.Balance(u1) != 25 {
		t.Fatalf("u1 ledger=%v want 25", e.led.Balance(u1))
	}
	if e.led.Balance(u2) != 10 {
		t.Fatalf("u2 ledger=%v want 10", e.led.Balance(u2))
	}
}

// Ensure the import is used even if a future edit removes a helper reference.
var _ = config.MinDepositUSD