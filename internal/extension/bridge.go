package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// Bridge-funding tunables. The timeouts are vars (not consts) so tests can
// shorten them; the tolerance is a true constant.
const (
	depositCreditTolerance = 1.0 // USDC; a deposit may credit slightly less
)

var (
	hlCreditTimeout    = 4 * time.Minute // HL finalizes a deposit in ~1 min
	reserveFundTimeout = 2 * time.Minute
	hlCreditPoll       = 10 * time.Second // waitForHLCredit poll interval
)

// Gas dust sent to a user's Arbitrum address so it can pay for its own Bridge2
// deposit tx (only when its balance is below minUserGasWei). go-ethereum copies
// these into each tx, so sharing the package-level values is safe.
var (
	userGasWei    = big.NewInt(1_000_000_000_000_000) // 0.001 ETH
	minUserGasWei = big.NewInt(200_000_000_000_000)   // 0.0002 ETH
)

// processDepositBridge funds a user's Hyperliquid account through the real
// Arbitrum bridge: the reserve positions native USDC (and gas) at the user's own
// Arbitrum address, that address deposits to Bridge2 (which credits the sending
// address on HL), and we wait for the HL credit before crediting the ledger.
func (e *Extension) processDepositBridge(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.DepositRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding deposit: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	if req.UsdAmount < config.MinDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f below HL minimum %.2f (sub-min deposits are not credited and lost)", req.UsdAmount, config.MinDepositUSD))
	}
	if req.UsdAmount > config.MaxDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f exceeds per-deposit cap %.2f", req.UsdAmount, config.MaxDepositUSD))
	}
	if e.led.Total()+req.UsdAmount > config.MaxTotalDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit exceeds global cap"))
	}

	// Serialize same-user ops: prevent the user's own Arbitrum nonce from
	// racing across concurrent deposits, and keep the ledger stable against
	// background reconciliation (which TryLocks and skips a busy user).
	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	priv, err := keys.DeriveUserKey(e.masterSeed, user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving key: %w", err))
	}
	userAddr := crypto.PubkeyToAddress(priv.PublicKey)
	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}

	ctx := context.Background()
	units := arb.USDToUnits(req.UsdAmount)

	// 1. Ensure the reserve holds the USDC to fund this deposit BEFORE spending
	//    any gas on the user, so a short reserve fails cleanly with no ETH
	//    stranded at the user's address. Refills from HL when auto-refill is on.
	if err := e.ensureReserveFor(ctx, req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, err)
	}

	// 2. Ensure the user's Arbitrum address can pay for its own deposit tx.
	uEth, err := e.arb.ETHBalance(ctx, userAddr)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading user gas: %w", err))
	}
	if uEth.Cmp(minUserGasWei) < 0 {
		if _, err := e.arb.SendETH(ctx, e.reserveKey, userAddr, userGasWei); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("funding user gas: %w", err))
		}
	}

	// 3. Reserve positions the deposit USDC at the user's own Arbitrum address
	//    (Bridge2 credits the sender, so it must originate from the user).
	if _, err := e.arb.TransferUSDC(ctx, e.reserveKey, e.usdcAddr, userAddr, units); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("funding user from reserve: %w", err))
	}
	if _, err := e.arb.WaitForUSDCBalance(ctx, e.usdcAddr, userAddr, units, 5*time.Second, reserveFundTimeout); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("waiting for reserve funding: %w", err))
	}

	// 4. Deposit to Bridge2 and wait for HL to credit the user's account.
	before, err := hlTotalUSD(ctx, acct)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading HL balance: %w", err))
	}
	if _, err := e.arb.DepositToBridge(ctx, priv, e.usdcAddr, e.bridgeAddr, units); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("bridge deposit: %w", err))
	}
	if err := waitForHLCredit(ctx, acct, before+req.UsdAmount-depositCreditTolerance, hlCreditTimeout); err != nil {
		// The USDC has already left the user's address for Bridge2; HL may still
		// credit it after we return this error. The ledger is NOT credited, so a
		// retry can double-credit once the late deposit lands — reconciliation
		// (Task 1.5) must reconcile the ledger against real HL balances to close
		// this drift. Fail loudly for now.
		return buildResult(a, df, nil, 0, fmt.Errorf("waiting for HL credit (USDC may still credit late; ledger not debited/credited): %w", err))
	}

	// 5. Switch the account to HL unified mode so spot and perp orders both draw
	//    from one collateral pool (no class-transfer sweep). Must run AFTER the
	//    first credit: HL rejects userSetAbstraction on a never-deposited account
	//    ("Must deposit before performing actions" — observed live). Best-effort
	//    and once per account; a failure is retried on the next deposit.
	e.ensureUnified(ctx, user, acct)

	e.led.Credit(user, req.UsdAmount)
	resp := types.DepositResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// processWithdrawBridge returns funds via HL's withdraw3 to the Arbitrum reserve
// (the Arbitrum-side hub that recycles float / later bridges back to Flare). It
// debits the ledger first and refunds on failure.
func (e *Extension) processWithdrawBridge(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.WithdrawRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding withdraw: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	// Serialize same-user ops (see processDepositBridge) so the debit + withdraw3
	// chain is atomic w.r.t. other ops and background reconciliation.
	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	if err := e.led.Debit(user, req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	acct, err := e.accountFor(user)
	if err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on derivation failure
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.Withdraw(context.Background(), e.reserveAddr.Hex(), req.UsdAmount); err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on HL failure
		return buildResult(a, df, nil, 0, fmt.Errorf("withdraw3: %w", err))
	}
	data, _ := json.Marshal(types.WithdrawResponse{BalanceUsd: e.led.Balance(user)})
	return buildResult(a, df, data, 1, nil)
}

// ensureReserveFor ensures the reserve holds at least needUSD of USDC on
// Arbitrum before the handler spends any gas on the user. If it is short and
// ReserveAutoRefill is enabled, it tops up from the reserve's HL balance
// (spot->perp, then withdraw3) and waits for the USDC to land. If it is still
// short after that (or auto-refill is off), it returns an error so the deposit
// fails cleanly with no ETH stranded at the user's address.
func (e *Extension) ensureReserveFor(ctx context.Context, needUSD float64) error {
	bal, err := e.arb.USDCBalance(ctx, e.usdcAddr, e.reserveAddr)
	if err != nil {
		return fmt.Errorf("read reserve USDC: %w", err)
	}
	if arb.UnitsToUSD(bal) >= needUSD {
		return nil
	}
	if !config.ReserveAutoRefill {
		return fmt.Errorf("reserve USDC %.2f < needed %.2f (set HL_RESERVE_AUTO_REFILL=true to top up from the reserve's HL balance)", arb.UnitsToUSD(bal), needUSD)
	}
	target := config.ReserveRefillFloorUSD
	if needUSD+10 > target {
		target = needUSD + 10
	}
	if err := e.refillReserve(ctx, target); err != nil {
		return err
	}
	bal, err = e.arb.USDCBalance(ctx, e.usdcAddr, e.reserveAddr)
	if err != nil {
		return fmt.Errorf("re-read reserve USDC: %w", err)
	}
	if arb.UnitsToUSD(bal) < needUSD {
		return fmt.Errorf("reserve USDC still %.2f < %.2f after refill (fund the reserve's HL account)", arb.UnitsToUSD(bal), needUSD)
	}
	return nil
}

// refillReserve tops up the reserve's Arbitrum USDC to >= targetUSD by moving
// the reserve's HL spot balance to perp and withdraw3-ing to the reserve's
// Arbitrum address. Mirrors the bootstrap in testing/arb_loop_test.go. The
// reserve's HL account must hold the balance; withdraw3 has a flat ~1 USDC fee.
func (e *Extension) refillReserve(ctx context.Context, targetUSD float64) error {
	bal, err := e.arb.USDCBalance(ctx, e.usdcAddr, e.reserveAddr)
	if err != nil {
		return fmt.Errorf("read reserve USDC: %w", err)
	}
	have := arb.UnitsToUSD(bal)
	if have >= targetUSD {
		return nil
	}
	move := targetUSD - have + 3 // +3 covers the flat withdraw fee
	if err := e.reserveHL.ClassTransfer(ctx, move, true); err != nil {
		return fmt.Errorf("reserve spot->perp: %w", err)
	}
	if err := e.reserveHL.Withdraw(ctx, e.reserveAddr.Hex(), move-2); err != nil {
		return fmt.Errorf("reserve withdraw3: %w", err)
	}
	want := arb.USDToUnits(targetUSD - 2)
	if _, err := e.arb.WaitForUSDCBalance(ctx, e.usdcAddr, e.reserveAddr, want, 10*time.Second, 6*time.Minute); err != nil {
		return fmt.Errorf("wait for reserve top-up: %w", err)
	}
	return nil
}

// hlTotalUSD returns the account's total USDC across perp (Core) and spot,
// robust to whether a bridge deposit lands in perp or is reported in spot
// (unified mode).
func hlTotalUSD(ctx context.Context, a hl.Account) (float64, error) {
	pos, err := a.Positions(ctx)
	if err != nil {
		return 0, err
	}
	return pos.PerpAccountValueUSD + pos.SpotUSDC, nil
}

// waitForHLCredit polls the HL account until its total USDC reaches target or
// the timeout elapses.
func waitForHLCredit(ctx context.Context, a hl.Account, target float64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		cur, err := hlTotalUSD(ctx, a)
		if err == nil && cur >= target {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out (target %.2f, last %.2f)", target, cur)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(hlCreditPoll):
		}
	}
}
