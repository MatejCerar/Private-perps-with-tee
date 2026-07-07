// Package ledger tracks per-user USD balances and recent orders in memory.
package ledger

import (
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

type OrderRecord struct {
	InstructionID string  `json:"instructionId"`
	Coin          string  `json:"coin"`
	Market        string  `json:"market"`
	IsBuy         bool    `json:"isBuy"`
	Size          float64 `json:"size"`
	Status        string  `json:"status"`
}

type Snapshot struct {
	BalanceUSD   float64       `json:"balanceUsd"`
	RecentOrders []OrderRecord `json:"recentOrders"`
}

type Ledger struct {
	mu       sync.RWMutex
	balances map[common.Address]float64
	orders   map[common.Address][]OrderRecord
}

func New() *Ledger {
	return &Ledger{
		balances: map[common.Address]float64{},
		orders:   map[common.Address][]OrderRecord{},
	}
}

func (l *Ledger) Credit(addr common.Address, amt float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[addr] += amt
}

func (l *Ledger) Debit(addr common.Address, amt float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.balances[addr] < amt {
		return fmt.Errorf("insufficient balance: have %.2f, need %.2f", l.balances[addr], amt)
	}
	l.balances[addr] -= amt
	return nil
}

// Adjust applies delta to the balance (may be negative) and clamps the result
// at 0. It returns the new balance. Used by reconciliation for downward
// corrections (P&L loss, withdraw fee) where the delta magnitude is bounded by
// the current balance — real HL equity is never negative, so the clamp only
// guards against float noise. Upward corrections use Credit.
func (l *Ledger) Adjust(addr common.Address, delta float64) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[addr] += delta
	if l.balances[addr] < 0 {
		l.balances[addr] = 0
	}
	return l.balances[addr]
}

func (l *Ledger) Balance(addr common.Address) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[addr]
}

func (l *Ledger) Total() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var t float64
	for _, v := range l.balances {
		t += v
	}
	return t
}

func (l *Ledger) AddOrder(addr common.Address, rec OrderRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	o := append(l.orders[addr], rec)
	if len(o) > 20 {
		o = o[len(o)-20:]
	}
	l.orders[addr] = o
}

func (l *Ledger) SnapshotFor(addr common.Address) Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	recent := make([]OrderRecord, len(l.orders[addr]))
	copy(recent, l.orders[addr])
	return Snapshot{BalanceUSD: l.balances[addr], RecentOrders: recent}
}
