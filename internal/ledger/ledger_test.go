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
