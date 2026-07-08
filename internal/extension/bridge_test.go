package extension

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// fakeArb is a stand-in ArbClient for bridge-mode handler tests. It moves
// USDC/ETH between in-memory balances, records every call, and lets a test
// inject errors or hooks (e.g. simulate HL crediting a deposit, or a withdraw3
// landing on Arbitrum). It is safe for concurrent use.
type fakeArb struct {
	mu   sync.Mutex
	usdc map[common.Address]*big.Int
	eth  map[common.Address]*big.Int

	transferErr error
	sendETHErr   error
	depositErr   error
	waitErr      error

	// depositHook is called after a successful DepositToBridge (e.g. to bump the
	// user's fake HL balance so waitForHLCredit observes the credit).
	depositHook func(units *big.Int)
	// waitHook, if set, overrides WaitForUSDCBalance (used to simulate a
	// withdraw3 landing on the reserve during auto-refill).
	waitHook func(addr common.Address, min *big.Int) (*big.Int, error)

	transfers []xferRec
	sends     []xferRec
	deposits  []xferRec
}

type xferRec struct {
	from, to common.Address
	units    *big.Int
}

func newFakeArb() *fakeArb {
	return &fakeArb{usdc: map[common.Address]*big.Int{}, eth: map[common.Address]*big.Int{}}
}

func (f *fakeArb) bal(m map[common.Address]*big.Int, a common.Address) *big.Int {
	if v, ok := m[a]; ok {
		return new(big.Int).Set(v)
	}
	return big.NewInt(0)
}

func (f *fakeArb) USDCBalance(_ context.Context, _ common.Address, addr common.Address) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bal(f.usdc, addr), nil
}

func (f *fakeArb) ETHBalance(_ context.Context, addr common.Address) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bal(f.eth, addr), nil
}

func (f *fakeArb) TransferUSDC(_ context.Context, from keys.Signer, _ common.Address, to common.Address, units *big.Int) (common.Hash, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.transferErr != nil {
		return common.Hash{}, f.transferErr
	}
	fromAddr := from.Address()
	f.transfers = append(f.transfers, xferRec{fromAddr, to, new(big.Int).Set(units)})
	f.usdc[fromAddr] = new(big.Int).Sub(f.bal(f.usdc, fromAddr), units)
	f.usdc[to] = new(big.Int).Add(f.bal(f.usdc, to), units)
	return common.Hash{0x01}, nil
}

func (f *fakeArb) SendETH(_ context.Context, from keys.Signer, to common.Address, wei *big.Int) (common.Hash, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendETHErr != nil {
		return common.Hash{}, f.sendETHErr
	}
	fromAddr := from.Address()
	f.sends = append(f.sends, xferRec{fromAddr, to, new(big.Int).Set(wei)})
	f.eth[fromAddr] = new(big.Int).Sub(f.bal(f.eth, fromAddr), wei)
	f.eth[to] = new(big.Int).Add(f.bal(f.eth, to), wei)
	return common.Hash{0x02}, nil
}

func (f *fakeArb) DepositToBridge(_ context.Context, from keys.Signer, _, _ common.Address, units *big.Int) (common.Hash, error) {
	f.mu.Lock()
	if f.depositErr != nil {
		f.mu.Unlock()
		return common.Hash{}, f.depositErr
	}
	fromAddr := from.Address()
	f.deposits = append(f.deposits, xferRec{fromAddr, common.Address{}, new(big.Int).Set(units)})
	f.usdc[fromAddr] = new(big.Int).Sub(f.bal(f.usdc, fromAddr), units)
	hook := f.depositHook
	f.mu.Unlock()
	if hook != nil {
		hook(units)
	}
	return common.Hash{0x03}, nil
}

func (f *fakeArb) WaitForUSDCBalance(_ context.Context, _ common.Address, addr common.Address, min *big.Int, _, _ time.Duration) (*big.Int, error) {
	if hook := f.waitHook; hook != nil {
		return hook(addr, min)
	}
	f.mu.Lock()
	b := f.bal(f.usdc, addr)
	f.mu.Unlock()
	if b.Cmp(min) >= 0 {
		return b, nil
	}
	if f.waitErr != nil {
		return b, f.waitErr
	}
	return b, fmt.Errorf("wait: balance %s < min %s", b, min)
}

// bridgeTestEnv wires a bridge-mode Extension against fakes and returns the
// extension, the fake arb client, the reserve's fake HL account, and the
// reserve address. The reserve is given ample ETH; its USDC balance is left at
// 0 so each test sets it explicitly.
func bridgeTestEnv(t *testing.T) (*Extension, *fakeArb, *hl.FakeAccount, common.Address) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x01}, 32)
	reserveKey, err := crypto.HexToECDSA(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	reserveAddr := crypto.PubkeyToAddress(reserveKey.PublicKey)
	fac := &hl.FakeFactory{}
	arc := newFakeArb()
	arc.eth[reserveAddr] = big.NewInt(1e18) // reserve has gas
	e := NewWithBridge(0, 0, fac, seed, arc, reserveKey, common.HexToAddress("0x01"), common.HexToAddress("0x02"))
	reserveHL := fac.ByAddr[reserveAddr.Hex()].(*hl.FakeAccount)
	return e, arc, reserveHL, reserveAddr
}

// userFake materializes the user's fake HL account (via accountFor) and returns
// it along with the Flare-side user address used in envelopes.
func userFake(t *testing.T, e *Extension) (common.Address, *hl.FakeAccount) {
	t.Helper()
	user := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	acct, err := e.accountFor(user)
	if err != nil {
		t.Fatal(err)
	}
	return user, acct.(*hl.FakeAccount)
}

func depositMsg(t *testing.T, user common.Address, amt float64) (teetypes.Action, *instruction.DataFixed) {
	t.Helper()
	msg := envelope(t, user, types.DepositRequest{UsdAmount: amt})
	return actionFor(msg)
}

func withdrawMsg(t *testing.T, user common.Address, amt float64) (teetypes.Action, *instruction.DataFixed) {
	t.Helper()
	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: amt})
	return actionFor(msg)
}

// shortCreditTimeout shortens the HL credit wait so the timeout path tests run
// instantly instead of waiting minutes. Restored on cleanup.
func shortCreditTimeout(t *testing.T) {
	t.Helper()
	prev := hlCreditTimeout
	hlCreditTimeout = 150 * time.Millisecond
	prevPoll := hlCreditPoll
	hlCreditPoll = 20 * time.Millisecond
	t.Cleanup(func() {
		hlCreditTimeout = prev
		hlCreditPoll = prevPoll
	})
}

func TestBridgeDepositHappyPathFake(t *testing.T) {
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, userFA := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000) // reserve funds the deposit
	arc.depositHook = func(units *big.Int) {
		userFA.PosSnapshot.PerpAccountValueUSD += arb.UnitsToUSD(units) // HL credits the user
	}

	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 1 {
		t.Fatalf("status=%d log=%s", res.Status, res.Log)
	}
	if e.led.Balance(user) != 20 {
		t.Fatalf("ledger=%v want 20", e.led.Balance(user))
	}
	if len(arc.deposits) != 1 {
		t.Fatalf("expected 1 bridge deposit, got %d", len(arc.deposits))
	}
	if userFA.PosSnapshot.PerpAccountValueUSD != 20 {
		t.Fatalf("HL credit=%v want 20", userFA.PosSnapshot.PerpAccountValueUSD)
	}
}

func TestBridgeDepositRejectsBelowMin(t *testing.T) {
	e, arc, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	// Force a small min for the test, restored after.
	prev := config.MinDepositUSD
	config.MinDepositUSD = 5
	t.Cleanup(func() { config.MinDepositUSD = prev })

	a, df := depositMsg(t, user, 2)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0, got %v", e.led.Balance(user))
	}
	if len(arc.transfers) != 0 || len(arc.sends) != 0 || len(arc.deposits) != 0 {
		t.Fatalf("no reserve txs expected on below-min rejection: %+v %+v %+v", arc.transfers, arc.sends, arc.deposits)
	}
}

func TestBridgeDepositReserveEmptyNoRefillDoesNotStrandGas(t *testing.T) {
	// Reserve USDC = 0, auto-refill OFF: the handler must fail BEFORE sending
	// any gas to the user, so no ETH is stranded at the user's address.
	e, arc, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	// reserve USDC left at 0; auto-refill defaults off.
	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0, got %v", e.led.Balance(user))
	}
	if len(arc.sends) != 0 {
		t.Fatalf("must NOT send gas when reserve is empty (would strand ETH); sends=%+v", arc.sends)
	}
	if len(arc.transfers) != 0 || len(arc.deposits) != 0 {
		t.Fatalf("no USDC txs expected: %+v %+v", arc.transfers, arc.deposits)
	}
}

func TestBridgeDepositReserveOutOfGas(t *testing.T) {
	// Reserve has USDC but SendETH fails (reserve out of gas): fail cleanly,
	// ledger unchanged, no deposit attempted.
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	arc.sendETHErr = fmt.Errorf("estimate gas: out of gas")
	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0, got %v", e.led.Balance(user))
	}
	if len(arc.deposits) != 0 {
		t.Fatalf("no deposit expected when gas funding fails: %+v", arc.deposits)
	}
}

func TestBridgeDepositTransferFails(t *testing.T) {
	// Reserve funded + gas sent ok, but the USDC transfer reverts: fail cleanly,
	// ledger unchanged. (Gas was sent; documented as acceptable dust loss.)
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	arc.transferErr = fmt.Errorf("transfer reverted")
	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0, got %v", e.led.Balance(user))
	}
	if len(arc.deposits) != 0 {
		t.Fatalf("no deposit expected when USDC transfer fails: %+v", arc.deposits)
	}
}

func TestBridgeDepositDepositTxReverts(t *testing.T) {
	// Everything funded, but the user's Bridge2 deposit tx reverts: fail cleanly,
	// ledger unchanged (USDC stayed at the user's address in the fake).
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	arc.depositErr = fmt.Errorf("bridge deposit reverted")
	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger should be 0, got %v", e.led.Balance(user))
	}
}

func TestBridgeDepositCreditTimeoutLeavesLedgerUncerdited(t *testing.T) {
	// The deposit tx succeeds (USDC leaves the user) but HL never credits within
	// the timeout: the handler fails and does NOT credit the ledger. The USDC is
	// on its way to HL and may credit late (drift) — reconciliation (Task 1.5)
	// closes that. Here we assert no double-spend: ledger stays 0.
	shortCreditTimeout(t)
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	// No depositHook: HL balance never rises, so waitForHLCredit times out.
	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected timeout rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger must NOT be credited on timeout (would double-credit on late HL credit), got %v", e.led.Balance(user))
	}
	if len(arc.deposits) != 1 {
		t.Fatalf("deposit tx should have been submitted, got %d", len(arc.deposits))
	}
}

func TestBridgeDepositAutoRefillTopsUpFromHL(t *testing.T) {
	// Reserve USDC = 0, auto-refill ON, reserve HL funded: the handler attempts a
	// refill (ClassTransfer + Withdraw) and the fake simulates the withdraw3
	// landing on Arbitrum, so the deposit proceeds.
	config.ReserveAutoRefill = true
	t.Cleanup(func() { config.ReserveAutoRefill = false })
	e, arc, reserveHL, reserveAddr := bridgeTestEnv(t)
	user, userFA := userFake(t, e)
	reserveHL.SpotBal = 200 // reserve HL balance to withdraw from
	// Simulate withdraw3 landing: when the handler waits for the reserve top-up,
	// credit the reserve's Arbitrum USDC to the requested minimum.
	arc.waitHook = func(addr common.Address, min *big.Int) (*big.Int, error) {
		if addr == reserveAddr {
			// Simulate the withdraw3 landing: credit the reserve to the target.
			arc.usdc[reserveAddr] = new(big.Int).Set(min)
			return min, nil
		}
		// Default behavior for any other address (e.g. the user being funded).
		arc.mu.Lock()
		b := arc.bal(arc.usdc, addr)
		arc.mu.Unlock()
		if b.Cmp(min) >= 0 {
			return b, nil
		}
		return b, fmt.Errorf("wait: balance %s < min %s", b, min)
	}
	arc.depositHook = func(units *big.Int) {
		userFA.PosSnapshot.PerpAccountValueUSD += arb.UnitsToUSD(units)
	}

	a, df := depositMsg(t, user, 20)
	res := e.processDeposit(a, df)
	if res.Status != 1 {
		t.Fatalf("status=%d log=%s", res.Status, res.Log)
	}
	// Refill was attempted: spot->perp transfer + withdraw3 recorded.
	if len(reserveHL.Transfers) == 0 || len(reserveHL.Withdrawals) == 0 {
		t.Fatalf("expected reserve refill (ClassTransfer + Withdraw), got transfers=%v withdrawals=%v", reserveHL.Transfers, reserveHL.Withdrawals)
	}
	if e.led.Balance(user) != 20 {
		t.Fatalf("ledger=%v want 20", e.led.Balance(user))
	}
}

func TestBridgeDepositSetsUnifiedOnce(t *testing.T) {
	// The first bridge deposit switches the account to HL unified mode; a second
	// deposit must NOT switch again (idempotent, tracked in unifiedSet).
	e, arc, _, reserveAddr := bridgeTestEnv(t)
	user, userFA := userFake(t, e)
	arc.usdc[reserveAddr] = arb.USDToUnits(1000)
	arc.depositHook = func(units *big.Int) {
		userFA.PosSnapshot.PerpAccountValueUSD += arb.UnitsToUSD(units)
	}

	a, df := depositMsg(t, user, 20)
	if res := e.processDeposit(a, df); res.Status != 1 {
		t.Fatalf("first deposit status=%d log=%s", res.Status, res.Log)
	}
	if len(userFA.Abstractions) != 1 || userFA.Abstractions[0] != hl.AbstractionUnified {
		t.Fatalf("expected one SetAbstraction(unified), got %+v", userFA.Abstractions)
	}

	a, df = depositMsg(t, user, 20)
	if res := e.processDeposit(a, df); res.Status != 1 {
		t.Fatalf("second deposit status=%d log=%s", res.Status, res.Log)
	}
	if len(userFA.Abstractions) != 1 {
		t.Fatalf("SetAbstraction must run once per account, got %+v", userFA.Abstractions)
	}
}

func TestBridgePerpOrderNoSweep(t *testing.T) {
	// Bridge accounts are unified: a perp order must NOT sweep spot->perp even
	// when the fake reports a spot balance. Leverage is still set; order placed.
	e, _, _, _ := bridgeTestEnv(t)
	user, fa := userFake(t, e)
	fa.SpotBal = 30 // would be swept in treasury mode; must be ignored in bridge mode

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "BTC", IsBuy: true, OrderType: "market", Size: 0.01, Leverage: 5,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status=%d log=%s", res.Status, res.Log)
	}
	if len(fa.Transfers) != 0 {
		t.Fatalf("bridge (unified) perp order must not class-transfer, got %+v", fa.Transfers)
	}
	if len(fa.Leverages) != 1 || fa.Leverages[0] != 5 {
		t.Fatalf("leverage not set: %+v", fa.Leverages)
	}
	if len(fa.Orders) != 1 || fa.Orders[0].Coin != "BTC" {
		t.Fatalf("order not placed: %+v", fa.Orders)
	}
}

func TestBridgeSpotBuyNoSweep(t *testing.T) {
	// Bridge accounts are unified: a spot buy must NOT sweep perp->spot even when
	// the fake reports free perp margin.
	e, _, _, _ := bridgeTestEnv(t)
	user, fa := userFake(t, e)
	fa.PerpBal = 20 // would be swept in treasury mode; must be ignored in bridge mode

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "spot", Coin: "PURR/USDC", IsBuy: true, OrderType: "limit", Size: 10, LimitPx: 2,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status=%d log=%s", res.Status, res.Log)
	}
	if len(fa.Transfers) != 0 {
		t.Fatalf("bridge (unified) spot buy must not class-transfer, got %+v", fa.Transfers)
	}
	if len(fa.Orders) != 1 || fa.Orders[0].Coin != "PURR/USDC" {
		t.Fatalf("order not placed: %+v", fa.Orders)
	}
}

func TestBridgeWithdrawRejectsOverBalance(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	user, _ := userFake(t, e)
	e.led.Credit(user, 10)
	a, df := withdrawMsg(t, user, 50)
	res := e.processWithdraw(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 10 {
		t.Fatalf("ledger should be unchanged at 10, got %v", e.led.Balance(user))
	}
}

func TestBridgeWithdrawHLFailureRefundsLedger(t *testing.T) {
	e, _, _, _ := bridgeTestEnv(t)
	user, userFA := userFake(t, e)
	e.led.Credit(user, 100)
	userFA.WithdrawErr = fmt.Errorf("withdraw3 rejected")
	a, df := withdrawMsg(t, user, 30)
	res := e.processWithdraw(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
	if e.led.Balance(user) != 100 {
		t.Fatalf("ledger should be refunded to 100, got %v", e.led.Balance(user))
	}
}

func TestBridgeConcurrentDepositsNoGasStranded(t *testing.T) {
	// N concurrent deposits against an empty reserve (auto-refill off) must each
	// fail at the pre-check WITHOUT sending gas, and the ledger must stay
	// consistent (no partial credits). Exercises the ledger mutex + the
	// reserve pre-check under concurrency.
	e, arc, _, _ := bridgeTestEnv(t)
	user := common.HexToAddress("0x000000000000000000000000000000000000C0FF")
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			a, df := depositMsg(t, user, 20)
			_ = e.processDeposit(a, df) // expected to fail
		}()
	}
	close(start)
	wg.Wait()
	arc.mu.Lock()
	sends := len(arc.sends)
	arc.mu.Unlock()
	if sends != 0 {
		t.Fatalf("no gas should be sent when reserve is empty; sends=%d", sends)
	}
	if e.led.Balance(user) != 0 {
		t.Fatalf("ledger must stay 0 under concurrent failures, got %v", e.led.Balance(user))
	}
}