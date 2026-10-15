package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/ledger"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// fanTarget is one shard account the order can be routed to, with the weight used
// to size its slice (free balance for opening orders, held position size for
// reduce-only orders).
type fanTarget struct {
	idx    int
	acct   hl.Account
	weight float64
}

// placeOrderFanOut spreads a single order across the user's funded shard accounts
// so the position is never concentrated on one account (which would re-link the
// shards by behaviour). This is the Layer 4 execution path, used whenever split
// mode is on. The degenerate case (only shard 0 funded) places the whole order on
// shard 0, identical to the single-account path.
//
// Sizing:
//   - opening orders: each shard's slice is proportional to its free HL balance.
//   - reduce-only orders: proportional to the position each shard actually holds in
//     the coin (so a close unwinds every shard's share of the position).
//
// Limit orders are placed on the single largest shard only, so they keep one HL
// order id that the frontend can cancel; market orders (the common path) fan out.
// Sub-orders that HL rejects (e.g. a slice below the ~$10 min) have their size
// re-placed on the largest shard in one fallback pass, so a small order collapses
// onto one shard rather than partially failing.
func (e *Extension) placeOrderFanOut(a teetypes.Action, df *instruction.DataFixed, user common.Address, req types.PlaceOrderRequest) teetypes.ActionResult {
	ctx := context.Background()

	targets, err := e.fanTargets(ctx, user, req)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	if len(targets) == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("no funded shard accounts available to trade"))
	}
	// Largest weight first; the largest shard also absorbs the exact-sum remainder
	// and any re-placed slices from rejected sub-orders.
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].weight > targets[j].weight })

	// Limit orders stay on one shard so their order id stays cancellable.
	if req.OrderType == "limit" {
		targets = targets[:1]
	}

	var totalWeight float64
	for _, t := range targets {
		totalWeight += t.weight
	}
	if totalWeight <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("no tradable balance across shards"))
	}

	// Slice the size proportionally; the largest shard (index 0) gets the remainder
	// so the sub-orders sum to exactly req.Size.
	sizes := make([]float64, len(targets))
	var allocatedToOthers float64
	for i := 1; i < len(targets); i++ {
		sizes[i] = req.Size * targets[i].weight / totalWeight
		allocatedToOthers += sizes[i]
	}
	sizes[0] = req.Size - allocatedToOthers

	var (
		results    []hl.OrderResult
		raws       []json.RawMessage
		placedSize float64
		failedSize float64
	)
	for i, t := range targets {
		if sizes[i] <= 0 {
			continue
		}
		res, err := e.placeOnShard(ctx, t.acct, req, sizes[i])
		if err != nil {
			logger.Warnf("fan-out: shard %d sub-order failed (%.6f %s): %v", t.idx, sizes[i], req.Coin, err)
			failedSize += sizes[i]
			continue
		}
		results = append(results, res)
		raws = append(raws, json.RawMessage(res.Raw))
		placedSize += sizes[i]
	}

	// Fallback: re-place any rejected size on the largest shard in one pass.
	if failedSize > 0 && len(results) > 0 {
		res, err := e.placeOnShard(ctx, targets[0].acct, req, failedSize)
		if err != nil {
			logger.Warnf("fan-out: fallback re-place of %.6f %s on shard %d failed: %v", failedSize, req.Coin, targets[0].idx, err)
		} else {
			results = append(results, res)
			raws = append(raws, json.RawMessage(res.Raw))
			placedSize += failedSize
			failedSize = 0
		}
	}

	if len(results) == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("fan-out order failed on all shards"))
	}

	// Aggregate into one logical order: the first fill's id/status represents it.
	status := results[0].Status
	if status == "" {
		status = "submitted"
	}
	e.led.AddOrder(user, ledger.OrderRecord{
		InstructionID: df.InstructionID.Hex(), OrderID: results[0].OrderID,
		Coin: req.Coin, Market: req.Market,
		IsBuy: req.IsBuy, Size: placedSize, Status: status,
	})
	logger.Infof("fan-out: placed %.6f %s across %d shard orders for %s", placedSize, req.Coin, len(results), user.Hex())

	detail, _ := json.Marshal(raws)
	resp := types.OrderResponse{Accepted: true, OrderID: results[0].OrderID, Detail: detail}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// fanTargets lists the shards an order should route to, with sizing weights. For
// reduce-only orders the weight is each shard's held position in the coin (shards
// with no position are excluded); for opening orders it is each shard's free HL
// balance. Shards below the dust floor are skipped.
func (e *Extension) fanTargets(ctx context.Context, user common.Address, req types.PlaceOrderRequest) ([]fanTarget, error) {
	depth := e.shardDepth(ctx, user)
	var targets []fanTarget
	for i := 0; i < depth; i++ {
		acct, err := e.accountForShard(user, i)
		if err != nil {
			return nil, err
		}
		if req.ReduceOnly {
			snap, err := acct.Positions(ctx)
			if err != nil {
				return nil, err
			}
			held := positionSize(snap, req.Coin)
			if held > shardDustUSD/1e6 { // any real position
				targets = append(targets, fanTarget{idx: i, acct: acct, weight: held})
			}
			continue
		}
		bal, err := hlTotalUSD(ctx, acct)
		if err != nil {
			return nil, err
		}
		if bal > shardDustUSD {
			targets = append(targets, fanTarget{idx: i, acct: acct, weight: bal})
		}
	}
	// Reduce-only with no detected positions: fall back to balance-weighted targets
	// so a close still attempts across funded shards.
	if req.ReduceOnly && len(targets) == 0 {
		for i := 0; i < depth; i++ {
			acct, err := e.accountForShard(user, i)
			if err != nil {
				return nil, err
			}
			if bal, err := hlTotalUSD(ctx, acct); err == nil && bal > shardDustUSD {
				targets = append(targets, fanTarget{idx: i, acct: acct, weight: bal})
			}
		}
	}
	return targets, nil
}

// placeOnShard sets leverage, sweeps collateral to the side the order needs
// (treasury/standard mode only, mirroring the single-account path), and places one
// sub-order of the given size on a shard account.
func (e *Extension) placeOnShard(ctx context.Context, acct hl.Account, req types.PlaceOrderRequest, size float64) (hl.OrderResult, error) {
	if req.Market == "perp" && req.Leverage > 0 {
		if err := acct.SetLeverage(ctx, req.Coin, req.Leverage, req.IsCross); err != nil {
			return hl.OrderResult{}, fmt.Errorf("set leverage: %w", err)
		}
	}
	if e.fundingMode != "bridge" {
		const dustUSD = 1.0
		if req.Market == "perp" {
			spot, err := acct.SpotUSDC(ctx)
			if err != nil {
				return hl.OrderResult{}, fmt.Errorf("read spot balance: %w", err)
			}
			if spot > dustUSD {
				if err := acct.ClassTransfer(ctx, spot, true); err != nil {
					return hl.OrderResult{}, fmt.Errorf("spot->perp transfer: %w", err)
				}
			}
		} else if req.IsBuy {
			perp, err := acct.PerpWithdrawable(ctx)
			if err != nil {
				return hl.OrderResult{}, fmt.Errorf("read perp balance: %w", err)
			}
			if perp > dustUSD {
				if err := acct.ClassTransfer(ctx, perp, false); err != nil {
					return hl.OrderResult{}, fmt.Errorf("perp->spot transfer: %w", err)
				}
			}
		}
	}
	return acct.PlaceOrder(ctx, hl.OrderReq{
		Coin: req.Coin, Market: req.Market, IsBuy: req.IsBuy,
		OrderType: req.OrderType, Size: size, LimitPx: req.LimitPx,
		ReduceOnly: req.ReduceOnly, Leverage: req.Leverage, IsCross: req.IsCross,
	})
}

// positionSize returns the absolute perp position size a snapshot holds in coin.
func positionSize(snap hl.PositionsSnapshot, coin string) float64 {
	for _, p := range snap.Perp {
		if p.Coin == coin {
			return math.Abs(p.Szi)
		}
	}
	return 0
}

// enqueueSplitWithdraw is the Layer 5 fan-in exit. It decomposes the requested
// amount into denomination chunks and reclaims each from a different funded shard
// back to the treasury, so the exit drains several accounts (mirror of funding)
// instead of concentrating on one. The ledger is debited up front by the amount
// actually placed; any chunk that cannot be packed onto a shard this round is left
// un-withdrawn (reported as the remainder) rather than failing the whole request.
func (e *Extension) enqueueSplitWithdraw(a teetypes.Action, df *instruction.DataFixed, user common.Address, usd float64) teetypes.ActionResult {
	ctx := context.Background()
	minD := smallestDenom(config.Denominations)
	if usd < minD {
		return buildResult(a, df, nil, 0, fmt.Errorf("withdraw %.2f is below the %.0f minimum", usd, minD))
	}
	chunks := splitIntoDenoms(usd, config.Denominations, config.MaxShards, newSplitRand(user))
	// Largest chunks first, matched to the largest-balance shards.
	sort.Sort(sort.Reverse(sort.Float64Slice(chunks)))

	depth := e.shardDepth(ctx, user)
	type shardBal struct {
		idx int
		bal float64
	}
	var shards []shardBal
	for i := 0; i < depth; i++ {
		acct, err := e.accountForShard(user, i)
		if err != nil {
			return buildResult(a, df, nil, 0, err)
		}
		bal, err := hlTotalUSD(ctx, acct)
		if err != nil {
			return buildResult(a, df, nil, 0, err)
		}
		if bal > shardDustUSD {
			shards = append(shards, shardBal{idx: i, bal: bal})
		}
	}
	sort.SliceStable(shards, func(i, j int) bool { return shards[i].bal > shards[j].bal })

	// Pack one chunk per shard: a shard can give a chunk only if it holds at least
	// that much (so the denominated reclaim never overdraws the account).
	type placement struct {
		shard int
		denom float64
	}
	var placements []placement
	used := make(map[int]bool)
	for _, c := range chunks {
		for _, s := range shards {
			if used[s.idx] || s.bal+0.01 < c {
				continue
			}
			placements = append(placements, placement{shard: s.idx, denom: c})
			used[s.idx] = true
			break
		}
	}
	if len(placements) == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("no shard holds enough to withdraw %.2f privately; close positions or withdraw a smaller amount", usd))
	}

	var placedTotal float64
	for _, p := range placements {
		placedTotal += p.denom
	}
	if err := e.led.Debit(user, placedTotal); err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	enqueued := 0
	for _, p := range placements {
		if e.withdrawPool.Enqueue(user, p.shard, p.denom) {
			enqueued++
		} else {
			e.led.Credit(user, p.denom) // shard already settling a withdraw; skip it
		}
	}
	if enqueued == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("a withdraw is already being settled privately for this account; wait for it to complete"))
	}

	est := int(e.withdrawPool.EstimatedDelay(user).Seconds())
	resp := types.WithdrawResponse{
		BalanceUsd:             e.led.Balance(user),
		PendingPayout:          true,
		EstimatedPayoutSeconds: est,
		Shards:                 enqueued,
		FundedUsd:              placedTotal,
		RemainderUsd:           usd - placedTotal,
	}
	logger.Infof("fan-in withdraw: reclaiming %.2f across %d shards for %s", placedTotal, enqueued, user.Hex())
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// cancelFanOut cancels an order id across every funded shard, best-effort: the id
// only exists on the shard that placed it, so "unknown order" errors on the others
// are ignored. Succeeds if the cancel lands on at least one shard.
func (e *Extension) cancelFanOut(ctx context.Context, user common.Address, coin string, oid int64) error {
	depth := e.shardDepth(ctx, user)
	var lastErr error
	cancelled := false
	for i := 0; i < depth; i++ {
		acct, err := e.accountForShard(user, i)
		if err != nil {
			return err
		}
		if err := acct.Cancel(ctx, coin, oid); err != nil {
			lastErr = err
			continue
		}
		cancelled = true
	}
	if cancelled {
		return nil
	}
	return lastErr
}
