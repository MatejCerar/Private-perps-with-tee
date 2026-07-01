package extension

import (
	"bytes"
	"encoding/json"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func envelope(t *testing.T, user common.Address, payload any) []byte {
	t.Helper()
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{{Type: addrTy}, {Type: bytesTy}}
	pj, _ := json.Marshal(payload)
	packed, err := args.Pack(user, pj)
	if err != nil {
		t.Fatal(err)
	}
	return packed
}

func actionFor(msg []byte) (teetypes.Action, *instruction.DataFixed) {
	df := &instruction.DataFixed{OriginalMessage: msg}
	return teetypes.Action{}, df
}

func TestProcessDepositFundsAndCredits(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := NewWith(0, 0, &hl.FakeFactory{}, treasury, "0xTREASURY", seed)

	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	msg := envelope(t, user, types.DepositRequest{UsdAmount: 100})
	a, df := actionFor(msg)

	res := e.processDeposit(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(treasury.SentUSD) != 1 || treasury.SentUSD[0].Amount != 100 {
		t.Fatalf("treasury transfer wrong: %+v", treasury.SentUSD)
	}
	if e.led.Balance(user) != 100 {
		t.Fatalf("ledger balance = %v", e.led.Balance(user))
	}
	var resp types.DepositResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BalanceUsd != 100 {
		t.Fatalf("resp balance = %v", resp.BalanceUsd)
	}
}

func TestProcessDepositRejectsOverCap(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	user := common.HexToAddress("0x2222222222222222222222222222222222222222")
	msg := envelope(t, user, types.DepositRequest{UsdAmount: 1e9})
	a, df := actionFor(msg)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got status %d", res.Status)
	}
}

func TestProcessPerpOrderSetsLeverageSweepsSpotToPerpAndPlaces(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x3333333333333333333333333333333333333333")

	// Pre-derive the account so we can inspect the fake afterwards.
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	fa.SpotBal = 30 // deposited USDC lands in spot; a perp order sweeps it to perp

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "BTC", IsBuy: true, OrderType: "market", Size: 0.01, Leverage: 5,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(fa.Leverages) != 1 || fa.Leverages[0] != 5 {
		t.Fatalf("leverage not set: %+v", fa.Leverages)
	}
	if len(fa.Transfers) != 1 || fa.Transfers[0] != 30 || !fa.TransfersToPerp[0] {
		t.Fatalf("expected spot->perp sweep of 30, got amounts=%v toPerp=%v", fa.Transfers, fa.TransfersToPerp)
	}
	if len(fa.Orders) != 1 || fa.Orders[0].Coin != "BTC" {
		t.Fatalf("order not placed: %+v", fa.Orders)
	}
}

func TestProcessPerpOrderNoSweepWhenSpotEmpty(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x3a33333333333333333333333333333333333333")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	fa.SpotBal = 0 // nothing in spot to sweep (funds already in perp)

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "market", Size: 0.01,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(fa.Transfers) != 0 {
		t.Fatalf("expected no transfer when spot empty, got %+v", fa.Transfers)
	}
}

func TestProcessSpotBuySweepsPerpToSpot(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x4444444444444444444444444444444444444444")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	fa.PerpBal = 20 // free perp margin is swept back to spot for a spot buy

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "spot", Coin: "PURR/USDC", IsBuy: true, OrderType: "limit", Size: 10, LimitPx: 2,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(fa.Transfers) != 1 || fa.Transfers[0] != 20 || fa.TransfersToPerp[0] {
		t.Fatalf("expected perp->spot sweep of 20, got amounts=%v toPerp=%v", fa.Transfers, fa.TransfersToPerp)
	}
}

func TestProcessCancel(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x5555555555555555555555555555555555555555")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)

	msg := envelope(t, user, types.CancelRequest{Coin: "BTC", OrderID: 999})
	a, df := actionFor(msg)
	res := e.processCancel(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d", res.Status)
	}
	if len(fa.Cancels) != 1 || fa.Cancels[0] != 999 {
		t.Fatalf("cancel not recorded: %+v", fa.Cancels)
	}
}

func TestProcessWithdrawDebitsAndReturnsToTreasury(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x6666666666666666666666666666666666666666")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xTREASURY"}, "0xTREASURY", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	e.led.Credit(user, 200)

	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: 80})
	a, df := actionFor(msg)
	res := e.processWithdraw(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if e.led.Balance(user) != 120 {
		t.Fatalf("balance = %v", e.led.Balance(user))
	}
	if len(fa.SentUSD) != 1 || fa.SentUSD[0].Dest != "0xTREASURY" || fa.SentUSD[0].Amount != 80 {
		t.Fatalf("treasury return wrong: %+v", fa.SentUSD)
	}
}

func TestProcessWithdrawRejectsOverBalance(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x7777777777777777777777777777777777777777")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xTREASURY"}, "0xTREASURY", seed)
	e.led.Credit(user, 10)
	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: 50})
	a, df := actionFor(msg)
	res := e.processWithdraw(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
}
