package extension

import (
	"context"
	"math"
	"time"

	"extension-scaffold/internal/config"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// ReconcileResult is the outcome of reconciling one user.
type ReconcileResult struct {
	User    common.Address
	Book    float64 // ledger balance before reconciliation
	Real    float64 // real HL equity (perp value + spot USDC)
	Delta   float64 // applied adjustment (0 if in tolerance or skipped)
	Skipped bool    // true if the user lock was held by an in-flight op
	Err     error
}

// Reconcile aligns one user's ledger balance with their real HL equity, closing
// drift from late deposit credits, trading P&L, and the withdraw3 fee. It
// TryLocks the per-user mutex and skips (Skipped=true, Delta=0) if an op is
// in flight for that user, so a stuck deposit never blocks the reconciler.
//
// Drift handling:
//   - real > book (delta>0): late deposit credit or P&L gain -> Credit.
//   - real < book (delta<0): P&L loss or withdraw fee -> Adjust (clamps at 0).
//   - |delta| <= ReconcileToleranceUSD: float dust, leave the ledger alone.
//
// Returns the applied delta (0 if in tolerance or skipped).
func (e *Extension) Reconcile(ctx context.Context, user common.Address) (float64, error) {
	// Private deposit: never reconcile a user whose deposit is in the pool awaiting
	// delayed funding. The HL account may already hold the funds (funding in
	// progress) while the ledger is intentionally not yet credited; crediting here
	// would collapse the funding delay and re-link the user by timing. The batch
	// funder is the sole authority that credits a pooled deposit.
	if e.depositPool != nil && e.depositPool.InFlight(user) {
		return 0, nil
	}
	// Same for a withdraw awaiting its delayed/batched HL reclaim: the ledger is
	// already debited but the HL funds are still on the account until the payout
	// batcher unwinds them; reconciling here would credit the gap back and undo the
	// debit. The payout batcher is the sole authority for an in-flight withdraw.
	if e.withdrawPool != nil && e.withdrawPool.InFlight(user) {
		return 0, nil
	}

	mu := e.userMutex(user)
	if !mu.TryLock() {
		return 0, nil // busy: an in-flight op owns this user; next tick will catch it
	}
	defer mu.Unlock()

	acct, err := e.accountFor(user)
	if err != nil {
		return 0, err
	}
	real, err := hlTotalUSD(ctx, acct)
	if err != nil {
		return 0, err
	}
	book := e.led.Balance(user)
	delta := real - book
	if math.Abs(delta) <= config.ReconcileToleranceUSD {
		return 0, nil
	}
	if delta > 0 {
		e.led.Credit(user, delta)
	} else {
		e.led.Adjust(user, delta) // negative; clamps at 0
	}
	logger.Infof("reconcile %s: book=%.4f real=%.4f delta=%+.4f", user.Hex(), book, real, delta)
	return delta, nil
}

// ReconcileAll reconciles every cached user. It snapshots the account cache
// under the existing RLock, then reconciles each user independently.
func (e *Extension) ReconcileAll(ctx context.Context) []ReconcileResult {
	e.mu.RLock()
	users := make([]common.Address, 0, len(e.accounts))
	for u := range e.accounts {
		users = append(users, u)
	}
	e.mu.RUnlock()

	results := make([]ReconcileResult, 0, len(users))
	for _, u := range users {
		book := e.led.Balance(u)
		delta, err := e.Reconcile(ctx, u)
		results = append(results, ReconcileResult{
			User:  u,
			Book:  book,
			Delta: delta,
			Err:   err,
		})
	}
	return results
}

// startReconciler launches the background reconciliation loop, ticking every
// config.ReconcileIntervalSeconds. No-op if the interval is <= 0. The goroutine
// exits when Stop closes reconcileStop. Only the production constructor (New)
// calls this; test constructors drive Reconcile/ReconcileAll directly.
func (e *Extension) startReconciler() {
	if config.ReconcileIntervalSeconds <= 0 {
		return
	}
	interval := time.Duration(config.ReconcileIntervalSeconds) * time.Second
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		logger.Infof("reconciler started: tick every %s", interval)
		for {
			select {
			case <-e.reconcileStop:
				logger.Infof("reconciler stopped")
				return
			case <-ticker.C:
				if res := e.ReconcileAll(context.Background()); len(res) > 0 {
					for _, r := range res {
						if r.Err != nil {
							logger.Warnf("reconcile %s: %v", r.User.Hex(), r.Err)
						}
					}
				}
			}
		}
	}()
}