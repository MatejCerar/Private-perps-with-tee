package extension

import (
	"context"
	"encoding/json"
	"math"
	"net/http"

	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/common"
)

// mergeSnapshots combines per-shard account reads into one aggregate view: summed
// balances/withdrawable, concatenated open orders, and positions merged by coin
// (summed size/PnL/margin/notional, size-weighted entry, notional-weighted
// liquidation estimate). This is the Layer 3 positions-merge: the frontend shows
// one balance and one position per coin even though the funds and positions are
// spread across several shard accounts it never sees.
func mergeSnapshots(snaps []hl.AccountSnapshot) hl.AccountSnapshot {
	var out hl.AccountSnapshot
	type acc struct {
		szi, pnl, marginUsed, positionValue float64
		entryNotional, sumAbsSzi            float64 // for size-weighted entry
		liqNotional                         float64 // for notional-weighted liq
		leverage                            int
	}
	byCoin := map[string]*acc{}
	order := []string{}
	for _, s := range snaps {
		out.SpotUSDC += s.SpotUSDC
		out.PerpAccountValueUSD += s.PerpAccountValueUSD
		out.Withdrawable += s.Withdrawable
		out.OpenOrders = append(out.OpenOrders, s.OpenOrders...)
		for _, p := range s.Perp {
			if p.Szi == 0 {
				continue
			}
			a := byCoin[p.Coin]
			if a == nil {
				a = &acc{}
				byCoin[p.Coin] = a
				order = append(order, p.Coin)
			}
			absSzi := math.Abs(p.Szi)
			a.szi += p.Szi
			a.pnl += p.UnrealizedPnl
			a.marginUsed += p.MarginUsed
			a.positionValue += p.PositionValue
			a.entryNotional += absSzi * p.EntryPx
			a.sumAbsSzi += absSzi
			a.liqNotional += p.PositionValue * p.LiquidationPx
			if p.Leverage > a.leverage {
				a.leverage = p.Leverage
			}
		}
	}
	for _, coin := range order {
		a := byCoin[coin]
		if a.szi == 0 {
			continue // shards net flat on this coin
		}
		var entry, liq float64
		if a.sumAbsSzi > 0 {
			entry = a.entryNotional / a.sumAbsSzi
		}
		if a.positionValue > 0 {
			liq = a.liqNotional / a.positionValue
		}
		out.Perp = append(out.Perp, hl.Position{
			Coin: coin, Szi: a.szi, EntryPx: entry, UnrealizedPnl: a.pnl,
			Leverage: a.leverage, MarginUsed: a.marginUsed,
			PositionValue: a.positionValue, LiquidationPx: liq,
		})
	}
	return out
}

// aggregateAccountSnapshot reads every funded shard of a user and merges them.
func (e *Extension) aggregateAccountSnapshot(ctx context.Context, user common.Address) (hl.AccountSnapshot, error) {
	depth := e.shardDepth(ctx, user)
	snaps := make([]hl.AccountSnapshot, 0, depth)
	for i := 0; i < depth; i++ {
		acct, err := e.accountForShard(user, i)
		if err != nil {
			return hl.AccountSnapshot{}, err
		}
		s, err := acct.AccountSnapshot(ctx)
		if err != nil {
			return hl.AccountSnapshot{}, err
		}
		snaps = append(snaps, s)
	}
	return mergeSnapshots(snaps), nil
}

// aggregateAccountResp is the aggregated view in the shape the frontend's
// lib/hl.ts HlAccount expects, so it can swap its HL-direct read for this one
// endpoint when split mode is on (and never learn the shard addresses).
type aggregateAccountResp struct {
	SpotUsdc         float64        `json:"spotUsdc"`
	PerpAccountValue float64        `json:"perpAccountValue"`
	Withdrawable     float64        `json:"withdrawable"`
	Positions        []hl.Position  `json:"positions"`
	OpenOrders       []hl.OpenOrder `json:"openOrders"`
}

// accountHandler serves GET /account?user=0x.. with the merged aggregate view
// across the user's shards. NOTE: this returns a user's trading activity keyed by
// Flare address and is currently unauthenticated - fine behind the trusted local
// relayer for the demo, but production must gate it to the authenticated caller
// (an EIP-191 signature the enclave recovers to `user`), or it re-leaks the very
// activity the shard split hides.
func (e *Extension) accountHandler(w http.ResponseWriter, r *http.Request) {
	userHex := r.URL.Query().Get("user")
	if userHex == "" {
		http.Error(w, "missing user query param", http.StatusBadRequest)
		return
	}
	user := common.HexToAddress(userHex)
	snap, err := e.aggregateAccountSnapshot(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp := aggregateAccountResp{
		SpotUsdc:         snap.SpotUSDC,
		PerpAccountValue: snap.PerpAccountValueUSD,
		Withdrawable:     snap.Withdrawable,
		Positions:        snap.Perp,
		OpenOrders:       snap.OpenOrders,
	}
	if resp.Positions == nil {
		resp.Positions = []hl.Position{}
	}
	if resp.OpenOrders == nil {
		resp.OpenOrders = []hl.OpenOrder{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
