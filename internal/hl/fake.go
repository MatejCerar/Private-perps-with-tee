package hl

import (
	"context"
	"crypto/ecdsa"
)

type USDSend struct {
	Dest   string
	Amount float64
}

type FakeAccount struct {
	Addr        string
	SpotBal       float64
	PerpBal       float64
	PosSnapshot   PositionsSnapshot
	OpenOrdersList []OpenOrder
	OrderErr      error
	WithdrawErr   error
	NextOrderID   int64 // OrderID returned by PlaceOrder (defaults to 12345 when 0)

	SentUSD         []USDSend
	Withdrawals     []USDSend
	Orders          []OrderReq
	Cancels         []int64
	Transfers       []float64
	TransfersToPerp []bool
	Leverages       []int
	Abstractions    []string
}

func (f *FakeAccount) Address() string { return f.Addr }

func (f *FakeAccount) PlaceOrder(_ context.Context, req OrderReq) (OrderResult, error) {
	if f.OrderErr != nil {
		return OrderResult{}, f.OrderErr
	}
	f.Orders = append(f.Orders, req)
	oid := f.NextOrderID
	if oid == 0 {
		oid = 12345
	}
	// Mirror the real adapter: market orders fill immediately, limit orders rest.
	status := "filled"
	if req.OrderType == "limit" {
		status = "resting"
	}
	return OrderResult{OrderID: oid, Status: status}, nil
}

func (f *FakeAccount) Cancel(_ context.Context, _ string, oid int64) error {
	f.Cancels = append(f.Cancels, oid)
	return nil
}

func (f *FakeAccount) SendUSD(_ context.Context, dest string, amount float64) error {
	f.SentUSD = append(f.SentUSD, USDSend{Dest: dest, Amount: amount})
	return nil
}

func (f *FakeAccount) Withdraw(_ context.Context, dest string, amount float64) error {
	f.Withdrawals = append(f.Withdrawals, USDSend{Dest: dest, Amount: amount})
	return f.WithdrawErr
}

func (f *FakeAccount) ClassTransfer(_ context.Context, amount float64, toPerp bool) error {
	f.Transfers = append(f.Transfers, amount)
	f.TransfersToPerp = append(f.TransfersToPerp, toPerp)
	return nil
}

func (f *FakeAccount) SpotUSDC(_ context.Context) (float64, error) { return f.SpotBal, nil }

func (f *FakeAccount) PerpWithdrawable(_ context.Context) (float64, error) { return f.PerpBal, nil }

func (f *FakeAccount) SetLeverage(_ context.Context, _ string, lev int, _ bool) error {
	f.Leverages = append(f.Leverages, lev)
	return nil
}

func (f *FakeAccount) SetAbstraction(_ context.Context, mode string) error {
	f.Abstractions = append(f.Abstractions, mode)
	return nil
}

func (f *FakeAccount) Positions(_ context.Context) (PositionsSnapshot, error) {
	return f.PosSnapshot, nil
}

func (f *FakeAccount) AccountSnapshot(_ context.Context) (AccountSnapshot, error) {
	return AccountSnapshot{
		SpotUSDC:            f.PosSnapshot.SpotUSDC,
		PerpAccountValueUSD: f.PosSnapshot.PerpAccountValueUSD,
		Withdrawable:        f.PerpBal,
		Perp:                f.PosSnapshot.Perp,
		OpenOrders:          f.OpenOrdersList,
	}, nil
}

type FakeFactory struct {
	ByAddr map[string]Account
	Last   Account
}

func (f *FakeFactory) Account(_ *ecdsa.PrivateKey, accountAddr string) Account {
	if a, ok := f.ByAddr[accountAddr]; ok {
		f.Last = a
		return a
	}
	a := &FakeAccount{Addr: accountAddr}
	if f.ByAddr == nil {
		f.ByAddr = map[string]Account{}
	}
	f.ByAddr[accountAddr] = a
	f.Last = a
	return a
}
