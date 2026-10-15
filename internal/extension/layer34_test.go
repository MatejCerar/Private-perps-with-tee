package extension

import (
	"context"
	"encoding/json"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

// fundShard sets a shard account's HL equity (perp account value) so the
// aggregated read and fan-out see it as funded.
func fundShard(t *testing.T, e *Extension, user common.Address, i int, bal float64) *hl.FakeAccount {
	t.Helper()
	acct, err := e.accountForShard(user, i)
	if err != nil {
		t.Fatal(err)
	}
	fa, ok := acct.(*hl.FakeAccount)
	if !ok {
		t.Fatalf("shard %d is not a FakeAccount", i)
	}
	fa.PosSnapshot.PerpAccountValueUSD = bal
	return fa
}

func placeOrder(t *testing.T, e *Extension, user common.Address, req types.PlaceOrderRequest) types.OrderResponse {
	t.Helper()
	msg := envelope(t, user, req)
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("order status=%d log=%s", res.Status, res.Log)
	}
	var resp types.OrderResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func orderSizeSum(fas ...*hl.FakeAccount) float64 {
	var s float64
	for _, fa := range fas {
		for _, o := range fa.Orders {
			s += o.Size
		}
	}
	return s
}

// --- Layer 3: aggregated read ---

func TestAggregatedReconcileSumsAllShards(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(1)

	// Funds spread across three shards; the ledger starts empty (as after a restart).
	fundShard(t, e, user, 0, 25)
	fundShard(t, e, user, 1, 25)
	fundShard(t, e, user, 2, 50)

	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if delta != 100 {
		t.Fatalf("reconcile should credit the aggregate of all shards (100), got %+v", delta)
	}
	if bal := e.led.Balance(user); bal != 100 {
		t.Fatalf("ledger should equal the summed shard equity (100), got %v", bal)
	}
}

func TestAggregatedReconcileDoesNotClawBackSplitFunds(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(2)

	// Ledger already reflects a $100 split deposit; HL holds it across 0,1,2.
	e.led.Credit(user, 100)
	fundShard(t, e, user, 0, 25)
	fundShard(t, e, user, 1, 25)
	fundShard(t, e, user, 2, 50)

	delta, err := e.Reconcile(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if delta != 0 {
		t.Fatalf("aggregate matches the ledger; no adjustment expected, got %+v", delta)
	}
	if bal := e.led.Balance(user); bal != 100 {
		t.Fatalf("split funds were clawed back: ledger=%v want 100", bal)
	}
}

// --- Layer 4: fan-out trading ---

func TestFanOutMarketOrderSplitsAcrossShards(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(3)
	s0 := fundShard(t, e, user, 0, 100)
	s1 := fundShard(t, e, user, 1, 100)

	resp := placeOrder(t, e, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "market", Size: 0.1, Leverage: 2,
	})
	if !resp.Accepted {
		t.Fatal("order not accepted")
	}
	if len(s0.Orders) != 1 || len(s1.Orders) != 1 {
		t.Fatalf("expected one sub-order per shard, got s0=%d s1=%d", len(s0.Orders), len(s1.Orders))
	}
	if sum := orderSizeSum(s0, s1); sum < 0.0999 || sum > 0.1001 {
		t.Fatalf("sub-orders should sum to the requested size 0.1, got %v", sum)
	}
	if len(s0.Leverages) != 1 || len(s1.Leverages) != 1 {
		t.Fatalf("leverage should be set on each traded shard")
	}
}

func TestFanOutLimitOrderStaysOnOneShard(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(4)
	s0 := fundShard(t, e, user, 0, 100)
	s1 := fundShard(t, e, user, 1, 100)

	placeOrder(t, e, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "limit", Size: 0.1, LimitPx: 2000, Leverage: 2,
	})
	placed := len(s0.Orders) + len(s1.Orders)
	if placed != 1 {
		t.Fatalf("a limit order must stay on one shard (one id to cancel), got %d orders", placed)
	}
}

func TestFanOutReduceOnlyWeightsByPosition(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(5)
	s0 := fundShard(t, e, user, 0, 100)
	s1 := fundShard(t, e, user, 1, 100)
	// Position is lopsided: shard 0 holds 0.08 ETH, shard 1 holds 0.02 ETH.
	s0.PosSnapshot.Perp = []hl.Position{{Coin: "ETH", Szi: 0.08}}
	s1.PosSnapshot.Perp = []hl.Position{{Coin: "ETH", Szi: 0.02}}

	placeOrder(t, e, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: false, OrderType: "market", Size: 0.1, ReduceOnly: true,
	})
	if len(s0.Orders) != 1 || len(s1.Orders) != 1 {
		t.Fatalf("close should hit both shards holding the position, got s0=%d s1=%d", len(s0.Orders), len(s1.Orders))
	}
	// Largest position (shard 0) gets the larger slice.
	if s0.Orders[0].Size <= s1.Orders[0].Size {
		t.Fatalf("shard with the bigger position should close more: s0=%v s1=%v", s0.Orders[0].Size, s1.Orders[0].Size)
	}
	if sum := orderSizeSum(s0, s1); sum < 0.0999 || sum > 0.1001 {
		t.Fatalf("closes should sum to 0.1, got %v", sum)
	}
}

func TestFanOutRejectedSliceFallsBackToLargestShard(t *testing.T) {
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := newSplitExt(t, treasury)
	user := paddr(6)
	s0 := fundShard(t, e, user, 0, 100)
	s1 := fundShard(t, e, user, 1, 100)
	s1.OrderErr = context.Canceled // this shard rejects every order

	resp := placeOrder(t, e, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "market", Size: 0.1, Leverage: 2,
	})
	if !resp.Accepted {
		t.Fatal("order should still be accepted via fallback")
	}
	if len(s1.Orders) != 0 {
		t.Fatalf("the rejecting shard should hold no order, got %d", len(s1.Orders))
	}
	if len(s0.Orders) != 2 {
		t.Fatalf("largest shard should absorb its own slice plus the rejected one (2 orders), got %d", len(s0.Orders))
	}
	if sum := orderSizeSum(s0); sum < 0.0999 || sum > 0.1001 {
		t.Fatalf("the full size should end up placed, got %v", sum)
	}
}
