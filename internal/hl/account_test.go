package hl

import (
	"context"
	"testing"
)

func TestFakeAccountRecordsCalls(t *testing.T) {
	f := &FakeAccount{Addr: "0xUSER", SpotBal: 5}
	ctx := context.Background()

	if err := f.SendUSD(ctx, "0xDEST", 100); err != nil {
		t.Fatal(err)
	}
	if len(f.SentUSD) != 1 || f.SentUSD[0].Amount != 100 || f.SentUSD[0].Dest != "0xDEST" {
		t.Fatalf("SendUSD not recorded: %+v", f.SentUSD)
	}

	_, err := f.PlaceOrder(ctx, OrderReq{Coin: "BTC", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Orders) != 1 {
		t.Fatalf("order not recorded")
	}

	bal, _ := f.SpotUSDC(ctx)
	if bal != 5 {
		t.Fatalf("spot bal = %v", bal)
	}
}

func TestFakeFactoryReturnsConfiguredAccount(t *testing.T) {
	fa := &FakeAccount{Addr: "0xABC"}
	ff := &FakeFactory{ByAddr: map[string]Account{"0xABC": fa}}
	got := ff.Account(nil, "0xABC")
	if got.Address() != "0xABC" {
		t.Fatalf("got %s", got.Address())
	}
}
