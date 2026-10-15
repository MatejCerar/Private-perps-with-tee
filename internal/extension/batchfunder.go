package extension

import (
	"context"
	"fmt"
	"time"

	"extension-scaffold/internal/config"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// The batch funder is the background half of private deposits. On each tick it asks
// the DepositPool for a matured batch (>= BatchMin due), SHUFFLES it so on-chain
// funding order does not encode deposit order, and funds each user's (seed-derived)
// HL account from the shared treasury in its uniform denomination. Only after HL
// confirms the transfer does it credit the ledger, so a crash mid-fund never
// double-counts (the entry stays pending and retries next tick).

// fundUserAccount funds one user's derived HL account from the shared treasury.
// This is the single funding origin for private deposits, which is what makes them
// unlinkable: the money reaches the account from the common treasury, decorrelated
// in time/amount from the depositor's on-chain deposit. Caller holds the user lock.
func (e *Extension) fundUserAccount(ctx context.Context, user common.Address, shard int, denom float64) error {
	acct, err := e.accountForShard(user, shard)
	if err != nil {
		return fmt.Errorf("deriving account: %w", err)
	}
	if e.treasury == nil {
		return fmt.Errorf("private funding requires a treasury account (funding mode %s)", e.fundingMode)
	}
	// Vault real-bridge mode: bring USDC onto HL into the treasury first.
	if e.fundingMode == "vault" && !e.bridge2Mock {
		if err := e.fundTreasuryViaBridge(ctx, denom); err != nil {
			return fmt.Errorf("bridge2 funding: %w", err)
		}
	}
	if err := e.treasury.SendUSD(ctx, acct.Address(), denom); err != nil {
		return fmt.Errorf("treasury spotSend: %w", err)
	}
	return nil
}

// drainDueBatch funds one matured batch (if any) and credits the ledger per
// success. Unexported but callable from tests to drive a single cycle without the
// ticker.
func (e *Extension) drainDueBatch(ctx context.Context) int {
	if e.depositPool == nil {
		return 0
	}
	// Self-heal: drop deposits that have been un-fundable for too long so they
	// never block the account permanently (e.g. the treasury could not fund them).
	if config.MaxFundingAgeSeconds > 0 {
		for _, it := range e.depositPool.Expire(time.Duration(config.MaxFundingAgeSeconds) * time.Second) {
			logger.Warnf("batch funder: expired un-fundable deposit for %s after %ds (no ledger impact; account freed)", it.User.Hex(), config.MaxFundingAgeSeconds)
		}
	}
	batch := e.depositPool.DueBatch(config.BatchMin)
	if len(batch) == 0 {
		return 0
	}
	// Shuffle so funding order is independent of deposit order.
	e.depositPool.mu.Lock()
	e.depositPool.rnd.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
	e.depositPool.mu.Unlock()

	funded := 0
	for _, it := range batch {
		if e.fundOne(ctx, it.User, it.Shard, it.Denom) {
			funded++
		}
	}
	logger.Infof("batch funder: funded %d/%d matured deposits", funded, len(batch))
	return funded
}

// fundOne funds a single user's account and credits the ledger, holding the user
// lock across fund+credit (so reconciliation TryLock-skips it and the ledger is
// never credited twice). On failure the entry returns to pending for retry.
func (e *Extension) fundOne(ctx context.Context, user common.Address, shard int, denom float64) bool {
	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()
	if err := e.fundUserAccount(ctx, user, shard, denom); err != nil {
		logger.Warnf("batch funder: funding %s shard %d failed (will retry): %v", user.Hex(), shard, err)
		e.depositPool.Fail(user, shard)
		return false
	}
	e.led.Credit(user, denom)
	e.depositPool.Complete(user, shard)
	logger.Infof("batch funder: credited %s shard %d with %.2f (privately funded)", user.Hex(), shard, denom)
	return true
}

// startBatchFunder launches the background funding loop, ticking every
// config.FundTickSeconds. No-op if there is no deposit pool. Exits when Stop closes
// batchFunderStop.
func (e *Extension) startBatchFunder() {
	if e.depositPool == nil || config.FundTickSeconds <= 0 {
		return
	}
	interval := time.Duration(config.FundTickSeconds) * time.Second
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		logger.Infof("batch funder started: tick every %s, batchMin=%d, delay=[%ds,%ds]",
			interval, config.BatchMin, config.FundDelayMinSeconds, config.FundDelayMaxSeconds)
		for {
			select {
			case <-e.batchFunderStop:
				logger.Infof("batch funder stopped")
				return
			case <-ticker.C:
				e.drainDueBatch(context.Background())
			}
		}
	}()
}
