package extension

import (
	"context"
	"math"
	"testing"

	"extension-scaffold/internal/hl"
)

// --- Layer 5: withdraw fan-in ---

func TestSplitWithdrawFansInAcrossShards(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	e.withdrawPool = NewDepositPool(0, 0, 1) // immediately due, deterministic
	user := paddr(7)

	// Plenty of shards, each holding enough to satisfy any denomination chunk.
	var shardFakes []*hl.FakeAccount
	for i := 0; i < 10; i++ {
		shardFakes = append(shardFakes, fundShard(t, e, user, i, 50))
	}
	e.led.Credit(user, 100)

	resp := withdraw(t, e, user, 100)
	if !resp.PendingPayout {
		t.Fatal("expected pendingPayout=true")
	}
	if resp.FundedUsd != 100 || resp.RemainderUsd != 0 {
		t.Fatalf("funded=%.2f remainder=%.2f, want 100/0", resp.FundedUsd, resp.RemainderUsd)
	}
	if resp.Shards < 2 {
		t.Fatalf("withdraw should fan in across >=2 shards, got %d", resp.Shards)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be debited by the withdrawn total, got %v", e.led.Balance(user))
	}

	done := e.drainDuePayouts(context.Background())
	if done != resp.Shards {
		t.Fatalf("expected %d reclaims settled, got %d", resp.Shards, done)
	}
	// Each used shard sends its chunk to the treasury; the total reclaimed is 100.
	var total float64
	usedShards := 0
	for _, fa := range shardFakes {
		for _, s := range fa.SentUSD {
			if s.Dest == treasury.Addr {
				total += s.Amount
				usedShards++
			}
		}
	}
	if total != 100 {
		t.Fatalf("reclaimed total to treasury = %.2f, want 100", total)
	}
	if usedShards != resp.Shards {
		t.Fatalf("expected %d shards to reclaim, got %d", resp.Shards, usedShards)
	}
	if e.withdrawPool.Len() != 0 {
		t.Fatalf("withdraw pool should be empty after payout, len=%d", e.withdrawPool.Len())
	}
}

func TestSplitWithdrawDebitsOnlyPlacedAmount(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	e.withdrawPool = NewDepositPool(0, 0, 1)
	user := paddr(8)
	// Only one shard with $50; a $100 withdraw can place at most $50 this round.
	fundShard(t, e, user, 0, 50)
	e.led.Credit(user, 100)

	resp := withdraw(t, e, user, 100)
	if resp.FundedUsd > 50 {
		t.Fatalf("cannot place more than the shard holds, funded=%.2f", resp.FundedUsd)
	}
	// Ledger debited only by what was actually placed; the rest stays spendable.
	if e.led.Balance(user) != 100-resp.FundedUsd {
		t.Fatalf("ledger should keep the un-placed remainder: bal=%v funded=%v", e.led.Balance(user), resp.FundedUsd)
	}
}

// --- Layer 3 follow-up: positions merge ---

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestMergeSnapshotsAggregatesBalancesAndPositions(t *testing.T) {
	snaps := []hl.AccountSnapshot{
		{
			SpotUSDC: 10, PerpAccountValueUSD: 100, Withdrawable: 40,
			Perp:       []hl.Position{{Coin: "ETH", Szi: 0.06, EntryPx: 2000, UnrealizedPnl: 1, Leverage: 2, MarginUsed: 60, PositionValue: 120, LiquidationPx: 1500}},
			OpenOrders: []hl.OpenOrder{{Coin: "ETH", Side: "buy", Sz: 0.01, LimitPx: 1900, Oid: 1}},
		},
		{
			SpotUSDC: 5, PerpAccountValueUSD: 50, Withdrawable: 20,
			Perp:       []hl.Position{{Coin: "ETH", Szi: 0.04, EntryPx: 2500, UnrealizedPnl: 2, Leverage: 2, MarginUsed: 40, PositionValue: 100, LiquidationPx: 1600}},
			OpenOrders: []hl.OpenOrder{{Coin: "ETH", Side: "sell", Sz: 0.02, LimitPx: 2600, Oid: 2}},
		},
	}
	m := mergeSnapshots(snaps)

	if m.SpotUSDC != 15 || m.PerpAccountValueUSD != 150 || m.Withdrawable != 60 {
		t.Fatalf("balance sums wrong: spot=%v perp=%v withdrawable=%v", m.SpotUSDC, m.PerpAccountValueUSD, m.Withdrawable)
	}
	if len(m.OpenOrders) != 2 {
		t.Fatalf("open orders should concatenate, got %d", len(m.OpenOrders))
	}
	if len(m.Perp) != 1 {
		t.Fatalf("same-coin positions should merge to one, got %d", len(m.Perp))
	}
	p := m.Perp[0]
	if !approx(p.Szi, 0.10) || !approx(p.UnrealizedPnl, 3) || !approx(p.MarginUsed, 100) || !approx(p.PositionValue, 220) {
		t.Fatalf("sums wrong: %+v", p)
	}
	// Size-weighted entry: (0.06*2000 + 0.04*2500) / 0.10 = 2200.
	if !approx(p.EntryPx, 2200) {
		t.Fatalf("size-weighted entry = %v, want 2200", p.EntryPx)
	}
	// Notional-weighted liq: (120*1500 + 100*1600) / 220 = 1545.4545...
	if !approx(p.LiquidationPx, 340000.0/220.0) {
		t.Fatalf("notional-weighted liq = %v, want %v", p.LiquidationPx, 340000.0/220.0)
	}
	if p.Leverage != 2 {
		t.Fatalf("leverage = %d, want 2", p.Leverage)
	}
}

func TestMergeSnapshotsDropsNetFlatCoin(t *testing.T) {
	snaps := []hl.AccountSnapshot{
		{Perp: []hl.Position{{Coin: "ETH", Szi: 0.05, EntryPx: 2000, PositionValue: 100}}},
		{Perp: []hl.Position{{Coin: "ETH", Szi: -0.05, EntryPx: 2100, PositionValue: 105}}},
	}
	m := mergeSnapshots(snaps)
	if len(m.Perp) != 0 {
		t.Fatalf("a net-flat coin should not appear, got %+v", m.Perp)
	}
}

func TestAggregateAccountSnapshotReadsAllShards(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(9)
	s0 := fundShard(t, e, user, 0, 100)
	s1 := fundShard(t, e, user, 1, 100)
	s0.PosSnapshot.SpotUSDC = 5
	s1.PosSnapshot.SpotUSDC = 5
	s0.PosSnapshot.Perp = []hl.Position{{Coin: "ETH", Szi: 0.05, EntryPx: 2000, PositionValue: 100, LiquidationPx: 1500}}
	s1.PosSnapshot.Perp = []hl.Position{{Coin: "ETH", Szi: 0.05, EntryPx: 2000, PositionValue: 100, LiquidationPx: 1500}}

	m, err := e.aggregateAccountSnapshot(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(m.PerpAccountValueUSD, 200) || !approx(m.SpotUSDC, 10) {
		t.Fatalf("aggregate balances wrong: perp=%v spot=%v", m.PerpAccountValueUSD, m.SpotUSDC)
	}
	if len(m.Perp) != 1 || !approx(m.Perp[0].Szi, 0.10) {
		t.Fatalf("merged position wrong: %+v", m.Perp)
	}
}
