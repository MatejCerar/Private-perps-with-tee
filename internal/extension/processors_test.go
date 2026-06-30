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
