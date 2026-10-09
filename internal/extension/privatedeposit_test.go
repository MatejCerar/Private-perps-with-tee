package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

func paddr(b byte) common.Address {
	var a common.Address
	a[19] = b
	return a
}

// newPrivExt builds a treasury-mode extension with private deposit enabled and a
// deterministic, immediately-due pool and NO background goroutine (tests drive
// drainDueBatch). Config globals are restored on cleanup.
func newPrivExt(t *testing.T, treasury *hl.FakeAccount, batchMin int) *Extension {
	t.Helper()
	origBatch, origDenoms := config.BatchMin, config.Denominations
	t.Cleanup(func() { config.BatchMin, config.Denominations = origBatch, origDenoms })
	config.BatchMin = batchMin
	config.Denominations = []float64{25, 100, 500}

	seed := bytes.Repeat([]byte{0x01}, 32)
	e := NewWith(0, 0, &hl.FakeFactory{}, treasury, treasury.Addr, seed)
	e.depositPool = NewDepositPool(0, 0, 1) // immediately due, deterministic
	return e
}

func deposit(t *testing.T, e *Extension, user common.Address, usd float64) types.DepositResponse {
	t.Helper()
	msg := envelope(t, user, types.DepositRequest{UsdAmount: usd})
	a, df := actionFor(msg)
	res := e.processDeposit(a, df)
	if res.Status != 1 {
		t.Fatalf("deposit status=%d log=%s", res.Status, res.Log)
	}
	var resp types.DepositResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPrivateDepositIsPendingNotSynchronous(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExt(t, treasury, 1)
	user := paddr(1)

	resp := deposit(t, e, user, 100)
	if !resp.PendingFunding {
		t.Fatal("expected pendingFunding=true")
	}
	if len(treasury.SentUSD) != 0 {
		t.Fatalf("no synchronous funding expected, got %+v", treasury.SentUSD)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger must stay 0 until batch funds, got %v", e.led.Balance(user))
	}
	if !e.depositPool.InFlight(user) {
		t.Fatal("deposit should be escrowed in the pool")
	}
	// The HL address is the ORIGINAL seed-derived account (unchanged model).
	want, _ := e.accountFor(user)
	if resp.HLAddress != want.Address() {
		t.Fatalf("expected derived account %s, got %s", want.Address(), resp.HLAddress)
	}
}

func TestBatchFunderFundsDerivedAccountFromTreasury(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExt(t, treasury, 2) // anonymity floor of 2
	u1, u2 := paddr(1), paddr(2)
	r1 := deposit(t, e, u1, 100)
	r2 := deposit(t, e, u2, 100)

	funded := e.drainDueBatch(context.Background())
	if funded != 2 {
		t.Fatalf("expected 2 funded, got %d", funded)
	}
	if len(treasury.SentUSD) != 2 {
		t.Fatalf("expected 2 treasury sends, got %+v", treasury.SentUSD)
	}
	dests := map[string]float64{}
	for _, s := range treasury.SentUSD {
		dests[s.Dest] = s.Amount
		if s.Amount != 100 {
			t.Fatalf("non-uniform funding amount %v", s.Amount)
		}
	}
	if dests[r1.HLAddress] != 100 || dests[r2.HLAddress] != 100 {
		t.Fatalf("treasury did not fund both derived accounts: %+v", dests)
	}
	if e.led.Balance(u1) != 100 || e.led.Balance(u2) != 100 {
		t.Fatalf("ledger not credited after batch: %v, %v", e.led.Balance(u1), e.led.Balance(u2))
	}
	if e.depositPool.Len() != 0 {
		t.Fatalf("pool should be empty after funding, len=%d", e.depositPool.Len())
	}
}

func TestBatchFunderRespectsAnonymityFloor(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExt(t, treasury, 3) // need 3 before any fund
	deposit(t, e, paddr(1), 100)
	deposit(t, e, paddr(2), 100)
	if funded := e.drainDueBatch(context.Background()); funded != 0 {
		t.Fatalf("below floor should fund 0, funded %d", funded)
	}
	if len(treasury.SentUSD) != 0 {
		t.Fatal("no funding below the floor")
	}
}

func TestPrivateDepositRejectsNonDenomination(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExt(t, treasury, 1)
	msg := envelope(t, paddr(1), types.DepositRequest{UsdAmount: 37})
	a, df := actionFor(msg)
	if res := e.processDeposit(a, df); res.Status == 1 {
		t.Fatal("non-denomination deposit should be rejected")
	}
}

func TestReconcileSkipsInFlightPrivateDeposit(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExt(t, treasury, 5) // high floor so it stays pending
	user := paddr(1)
	deposit(t, e, user, 100)
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if delta != 0 || e.led.Balance(user) != 0 {
		t.Fatalf("reconcile collapsed the funding delay: delta=%v bal=%v", delta, e.led.Balance(user))
	}
}

// --- private withdraw (the mirror) ---

func withdraw(t *testing.T, e *Extension, user common.Address, usd float64) types.WithdrawResponse {
	t.Helper()
	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: usd})
	a, df := actionFor(msg)
	res := e.processWithdraw(a, df)
	if res.Status != 1 {
		t.Fatalf("withdraw status=%d log=%s", res.Status, res.Log)
	}
	var resp types.WithdrawResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// newPrivExtW builds a private extension with BOTH pools and a funded ledger so a
// withdraw can be debited. batchMin applies to both pools.
func newPrivExtW(t *testing.T, treasury *hl.FakeAccount, batchMin int) *Extension {
	e := newPrivExt(t, treasury, batchMin)
	e.withdrawPool = NewDepositPool(0, 0, 1) // immediately due, deterministic
	return e
}

func TestPrivateWithdrawIsPendingAndDelayed(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExtW(t, treasury, 5) // high floor: stays pending
	user := paddr(1)
	e.led.Credit(user, 100) // fund the ledger first

	resp := withdraw(t, e, user, 100)
	if !resp.PendingPayout {
		t.Fatal("expected pendingPayout=true")
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be debited immediately, got %v", e.led.Balance(user))
	}
	if len(treasury.SentUSD) != 0 {
		t.Fatalf("no HL reclaim should happen until the payout batch, got %+v", treasury.SentUSD)
	}
	if !e.withdrawPool.InFlight(user) {
		t.Fatal("withdraw should be queued in the pool")
	}
}

func TestPayoutBatcherReclaimsAfterBatch(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExtW(t, treasury, 2)
	u1, u2 := paddr(1), paddr(2)
	// Each user's derived HL account is the FakeAccount that SendUSD(dest=treasury)
	// records on; here we just assert the reclaim fires for the batch.
	e.led.Credit(u1, 100)
	e.led.Credit(u2, 100)
	withdraw(t, e, u1, 100)
	withdraw(t, e, u2, 100)

	done := e.drainDuePayouts(context.Background())
	if done != 2 {
		t.Fatalf("expected 2 settled, got %d", done)
	}
	if e.withdrawPool.Len() != 0 {
		t.Fatalf("withdraw pool should be empty after payout, len=%d", e.withdrawPool.Len())
	}
	// Ledger stays debited (0) after a successful payout.
	if e.led.Balance(u1) != 0 || e.led.Balance(u2) != 0 {
		t.Fatalf("ledger should remain debited after payout: %v %v", e.led.Balance(u1), e.led.Balance(u2))
	}
}

func TestPayoutBatcherRespectsFloor(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExtW(t, treasury, 3)
	e.led.Credit(paddr(1), 100)
	e.led.Credit(paddr(2), 100)
	withdraw(t, e, paddr(1), 100)
	withdraw(t, e, paddr(2), 100)
	if done := e.drainDuePayouts(context.Background()); done != 0 {
		t.Fatalf("below floor should settle 0, got %d", done)
	}
}

func TestPrivateWithdrawRejectsNonDenomination(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExtW(t, treasury, 1)
	e.led.Credit(paddr(1), 100)
	msg := envelope(t, paddr(1), types.WithdrawRequest{UsdAmount: 37})
	a, df := actionFor(msg)
	if res := e.processWithdraw(a, df); res.Status == 1 {
		t.Fatal("non-denomination withdraw should be rejected")
	}
	// and the ledger must not have been debited on reject
	if e.led.Balance(paddr(1)) != 100 {
		t.Fatalf("ledger should be untouched on reject, got %v", e.led.Balance(paddr(1)))
	}
}

func TestReconcileSkipsInFlightWithdraw(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newPrivExtW(t, treasury, 5)
	user := paddr(1)
	e.led.Credit(user, 100)
	withdraw(t, e, user, 100)
	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if delta != 0 {
		t.Fatalf("reconcile should skip an in-flight withdraw, got delta %v", delta)
	}
}

func TestDepositPoolDelayInRange(t *testing.T) {
	p := NewDepositPool(10*time.Second, 20*time.Second, 1)
	if !p.Enqueue(paddr(1), 100) {
		t.Fatal("first enqueue should succeed")
	}
	if p.Enqueue(paddr(1), 100) {
		t.Fatal("duplicate enqueue should be rejected")
	}
	d := p.EstimatedDelay(paddr(1))
	if d < 9*time.Second || d > 21*time.Second {
		t.Fatalf("delay %s out of [10s,20s] window", d)
	}
}
