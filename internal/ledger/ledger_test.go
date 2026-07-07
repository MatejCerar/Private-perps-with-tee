package ledger

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var u = common.HexToAddress("0xabc0000000000000000000000000000000000001")

func TestCreditDebit(t *testing.T) {
	l := New()
	l.Credit(u, 100)
	if l.Balance(u) != 100 {
		t.Fatalf("balance = %v", l.Balance(u))
	}
	if err := l.Debit(u, 40); err != nil {
		t.Fatal(err)
	}
	if l.Balance(u) != 60 {
		t.Fatalf("balance = %v", l.Balance(u))
	}
}

func TestDebitInsufficient(t *testing.T) {
	l := New()
	l.Credit(u, 10)
	if err := l.Debit(u, 25); err == nil {
		t.Fatal("expected insufficient-funds error")
	}
}

func TestTotalAndOrders(t *testing.T) {
	l := New()
	l.Credit(u, 50)
	l.Credit(common.HexToAddress("0xdef"), 30)
	if l.Total() != 80 {
		t.Fatalf("total = %v", l.Total())
	}
	l.AddOrder(u, OrderRecord{Coin: "BTC", IsBuy: true, Size: 0.001, Status: "submitted"})
	s := l.SnapshotFor(u)
	if s.BalanceUSD != 50 || len(s.RecentOrders) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestMarkCancelled(t *testing.T) {
	l := New()
	l.AddOrder(u, OrderRecord{OrderID: 111, Coin: "BTC", Status: "resting"})
	l.AddOrder(u, OrderRecord{OrderID: 222, Coin: "ETH", Status: "resting"})

	if !l.MarkCancelled(u, 222) {
		t.Fatal("expected MarkCancelled(222) to find the order")
	}
	recs := l.SnapshotFor(u).RecentOrders
	if recs[1].Status != "cancelled" {
		t.Fatalf("order 222 status = %q, want cancelled", recs[1].Status)
	}
	if recs[0].Status != "resting" {
		t.Fatalf("order 111 must be untouched, status = %q", recs[0].Status)
	}
	// An unknown order id is a no-op returning false (best-effort reflection).
	if l.MarkCancelled(u, 999) {
		t.Fatal("MarkCancelled(999) should return false for an unrecorded order")
	}
}

func TestMarkCancelledMostRecentMatch(t *testing.T) {
	// Two records share an order id (e.g. an id reused after aging); the most
	// recent one is the live order, so it is the one marked cancelled.
	l := New()
	l.AddOrder(u, OrderRecord{OrderID: 500, Coin: "BTC", Status: "filled"})
	l.AddOrder(u, OrderRecord{OrderID: 500, Coin: "BTC", Status: "resting"})
	if !l.MarkCancelled(u, 500) {
		t.Fatal("expected a match")
	}
	recs := l.SnapshotFor(u).RecentOrders
	if recs[1].Status != "cancelled" || recs[0].Status != "filled" {
		t.Fatalf("only the most recent 500 should be cancelled: %+v", recs)
	}
}
