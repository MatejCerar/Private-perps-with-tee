package extension

import (
	"context"
	"math/rand"
	"testing"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
)

var splitDenoms = []float64{10, 25, 50, 100, 500}

// --- pure decomposition ---

func TestSplitIntoDenomsExactMultiple(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		out := splitIntoDenoms(500, splitDenoms, 15, rnd)
		if sumFloats(out) != 500 {
			t.Fatalf("chunks must sum to 500, got %v (sum %.2f)", out, sumFloats(out))
		}
		if len(out) < 1 || len(out) > 15 {
			t.Fatalf("chunk count %d out of [1,15]", len(out))
		}
		for _, c := range out {
			if !config.IsDenomination(c) {
				t.Fatalf("chunk %.2f is not a valid denomination", c)
			}
		}
	}
}

func TestSplitIntoDenomsSnapsRemainder(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	// $37 with a $5 GCD grid -> fund $35 (e.g. 25+10), remainder $2.
	out := splitIntoDenoms(37, splitDenoms, 15, rnd)
	if sumFloats(out) != 35 {
		t.Fatalf("expected $35 funded (remainder $2), got %v (sum %.2f)", out, sumFloats(out))
	}
	for _, c := range out {
		if !config.IsDenomination(c) {
			t.Fatalf("chunk %.2f is not a valid denomination", c)
		}
	}
}

func TestSplitIntoDenomsNeverOverFunds(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	for usd := 10; usd <= 600; usd++ {
		out := splitIntoDenoms(float64(usd), splitDenoms, 15, rnd)
		if s := sumFloats(out); s > float64(usd) {
			t.Fatalf("over-funded: deposit %d funded %.2f", usd, s)
		}
		// $10 is a denomination, so the largest representable amount <= usd always
		// leaves a remainder below the smallest denomination.
		if float64(usd)-sumFloats(out) >= smallestDenom(splitDenoms) {
			t.Fatalf("remainder >= smallest denom for %d: funded %.2f", usd, sumFloats(out))
		}
	}
}

// --- integration: a single deposit fans out across several real shard accounts ---

func newSplitExt(t *testing.T, treasury *hl.FakeAccount) *Extension {
	t.Helper()
	origBatch, origDenoms := config.BatchMin, config.Denominations
	origSplit, origMax := config.SplitDeposits, config.MaxShards
	t.Cleanup(func() {
		config.BatchMin, config.Denominations = origBatch, origDenoms
		config.SplitDeposits, config.MaxShards = origSplit, origMax
	})
	config.BatchMin = 1
	config.Denominations = splitDenoms
	config.SplitDeposits = true
	config.MaxShards = 15

	e := newPrivExt(t, treasury, 1)
	// newPrivExt forces Denominations to {25,100,500}; restore the split set.
	config.Denominations = splitDenoms
	return e
}

func TestSplitDepositFansOutAcrossShards(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(1)

	resp := deposit(t, e, user, 500)
	if resp.Shards < 2 {
		t.Fatalf("a $500 deposit should spread across several shards, got %d", resp.Shards)
	}
	if resp.FundedUsd != 500 || resp.RemainderUsd != 0 {
		t.Fatalf("funded=%.2f remainder=%.2f, want 500/0", resp.FundedUsd, resp.RemainderUsd)
	}
	if e.depositPool.Len() != resp.Shards {
		t.Fatalf("pool has %d entries, expected %d shards", e.depositPool.Len(), resp.Shards)
	}

	funded := e.drainDueBatch(context.Background())
	if funded != resp.Shards {
		t.Fatalf("expected %d shards funded, got %d", resp.Shards, funded)
	}
	if len(treasury.SentUSD) != resp.Shards {
		t.Fatalf("expected %d treasury sends (one per shard), got %d", resp.Shards, len(treasury.SentUSD))
	}
	// Distinct destination accounts, each a valid denomination, summing to 500.
	dests := map[string]bool{}
	var total float64
	for _, s := range treasury.SentUSD {
		if dests[s.Dest] {
			t.Fatalf("shard account %s funded twice (accounts must be standalone)", s.Dest)
		}
		dests[s.Dest] = true
		if !config.IsDenomination(s.Amount) {
			t.Fatalf("funded a non-denomination amount %.2f", s.Amount)
		}
		total += s.Amount
	}
	if total != 500 {
		t.Fatalf("shard funding should total 500, got %.2f", total)
	}
	if e.led.Balance(user) != 500 {
		t.Fatalf("ledger should aggregate all shards: got %.2f", e.led.Balance(user))
	}
	if e.depositPool.Len() != 0 {
		t.Fatalf("pool should be empty after funding, len=%d", e.depositPool.Len())
	}
}

func TestSplitDepositSnapsRemainder(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(2)

	resp := deposit(t, e, user, 37)
	if resp.FundedUsd != 35 || resp.RemainderUsd != 2 {
		t.Fatalf("funded=%.2f remainder=%.2f, want 35/2", resp.FundedUsd, resp.RemainderUsd)
	}
	e.drainDueBatch(context.Background())
	if e.led.Balance(user) != 35 {
		t.Fatalf("ledger should credit the denominable part only: got %.2f", e.led.Balance(user))
	}
}
