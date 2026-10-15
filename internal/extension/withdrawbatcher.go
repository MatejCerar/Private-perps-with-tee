package extension

import (
	"context"
	"fmt"
	"time"

	"extension-scaffold/internal/config"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// The payout batcher is the mirror of the batch funder, on the way OUT. A WITHDRAW
// debits the ledger immediately and enqueues into withdrawPool; this batcher
// unwinds the user's HL account back to the shared treasury after a randomized,
// batched delay. Decorrelating the HL reclaim from the on-chain withdraw request is
// what keeps the exit unlinkable (same anonymity-set logic as deposit: batch size +
// delay). The ledger was already debited at request time, so a payout only moves HL
// funds; on failure it refunds the ledger and retries.

// reclaimToTreasury unwinds one of a user's shard HL accounts back to the shared
// treasury (the exit's funding origin). Shard 0 is the primary account. Caller
// holds the user lock.
func (e *Extension) reclaimToTreasury(ctx context.Context, user common.Address, shard int, denom float64) error {
	acct, err := e.accountForShard(user, shard)
	if err != nil {
		return fmt.Errorf("deriving account: %w", err)
	}
	if e.treasuryAddr == "" {
		return fmt.Errorf("private withdraw requires a treasury account (funding mode %s)", e.fundingMode)
	}
	// Sweep any free perp margin to spot first so the spotSend can cover it.
	if free, perr := acct.PerpWithdrawable(ctx); perr == nil && free > 1.0 {
		_ = acct.ClassTransfer(ctx, free, false)
	}
	if err := acct.SendUSD(ctx, e.treasuryAddr, denom); err != nil {
		return fmt.Errorf("reclaim to treasury: %w", err)
	}
	return nil
}

// drainDuePayouts processes one matured batch of withdrawals (if any). Unexported
// but callable from tests to drive a single cycle without the ticker.
func (e *Extension) drainDuePayouts(ctx context.Context) int {
	if e.withdrawPool == nil {
		return 0
	}
	// Self-heal: drop withdrawals that stayed un-fundable too long so they never
	// block the account forever. A withdraw debits the ledger up front, so refund
	// it on expiry (the HL reclaim never happened).
	if config.MaxFundingAgeSeconds > 0 {
		for _, it := range e.withdrawPool.Expire(time.Duration(config.MaxFundingAgeSeconds) * time.Second) {
			e.led.Credit(it.User, it.Denom)
			logger.Warnf("payout batcher: expired un-fundable withdraw for %s after %ds; refunded %.2f to the ledger", it.User.Hex(), config.MaxFundingAgeSeconds, it.Denom)
		}
	}
	batch := e.withdrawPool.DueBatch(config.BatchMin)
	if len(batch) == 0 {
		return 0
	}
	// Shuffle so reclaim order is independent of withdraw-request order.
	e.withdrawPool.mu.Lock()
	e.withdrawPool.rnd.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
	e.withdrawPool.mu.Unlock()

	done := 0
	for _, it := range batch {
		if e.payoutOne(ctx, it.User, it.Shard, it.Denom) {
			done++
		}
	}
	logger.Infof("payout batcher: settled %d/%d matured withdrawals", done, len(batch))
	return done
}

// payoutOne unwinds a single user's HL account to the treasury, holding the user
// lock. The ledger was debited at request time; on success the entry is cleared, on
// failure the ledger is refunded and the entry returns to pending for retry.
func (e *Extension) payoutOne(ctx context.Context, user common.Address, shard int, denom float64) bool {
	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()
	if err := e.reclaimToTreasury(ctx, user, shard, denom); err != nil {
		logger.Warnf("payout batcher: reclaim for %s shard %d failed (will retry): %v", user.Hex(), shard, err)
		e.withdrawPool.Fail(user, shard)
		return false
	}
	e.withdrawPool.Complete(user, shard)
	logger.Infof("payout batcher: settled withdraw for %s shard %d (%.2f reclaimed privately)", user.Hex(), shard, denom)
	return true
}

// startPayoutBatcher launches the background payout loop, ticking every
// config.FundTickSeconds. No-op if there is no withdraw pool. Exits when Stop closes
// payoutBatcherStop.
func (e *Extension) startPayoutBatcher() {
	if e.withdrawPool == nil || config.FundTickSeconds <= 0 {
		return
	}
	interval := time.Duration(config.FundTickSeconds) * time.Second
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		logger.Infof("payout batcher started: tick every %s, batchMin=%d, delay=[%ds,%ds]",
			interval, config.BatchMin, config.FundDelayMinSeconds, config.FundDelayMaxSeconds)
		for {
			select {
			case <-e.payoutBatcherStop:
				logger.Infof("payout batcher stopped")
				return
			case <-ticker.C:
				e.drainDuePayouts(context.Background())
			}
		}
	}()
}
