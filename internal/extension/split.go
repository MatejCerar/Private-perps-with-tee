package extension

import (
	"math"
	"math/rand"
)

// splitIntoDenoms splits a deposit (usd, itself a fixed denomination) into a
// random number of smaller fixed-denomination chunks that sum EXACTLY to usd.
// Every chunk is a valid denomination, so uniformity is preserved (no chunk is a
// unique amount an observer could subset-sum). The chunk count is random and
// bounded by maxShards; the split is biased toward smaller chunks so a deposit
// spreads across several shard accounts. Returns the per-shard chunk amounts.
//
// Requires denoms to be positive multiples of the smallest denom (true for
// 25/100/500). Falls back to a single chunk if it can't split cleanly.
func splitIntoDenoms(usd float64, denoms []float64, maxShards int, rnd *rand.Rand) []float64 {
	if usd <= 0 || len(denoms) == 0 || maxShards < 1 {
		return []float64{usd}
	}
	base := denoms[0]
	for _, d := range denoms {
		if d > 0 && d < base {
			base = d
		}
	}
	if base <= 0 {
		return []float64{usd}
	}
	// Integer units of the smallest denom.
	units := int(math.Round(usd / base))
	if units <= 0 {
		return []float64{usd}
	}
	allowed := make([]int, 0, len(denoms))
	seen := map[int]bool{}
	for _, d := range denoms {
		u := int(math.Round(d / base))
		if u >= 1 && math.Abs(float64(u)*base-d) < 1e-9 && !seen[u] {
			allowed = append(allowed, u)
			seen[u] = true
		}
	}
	if len(allowed) == 0 {
		return []float64{usd}
	}

	// minParts[x] = fewest allowed chunks that sum to x (DP, exact). Used to
	// guarantee the remainder can always be finished within the shard cap.
	minParts := make([]int, units+1)
	for x := 1; x <= units; x++ {
		best := math.MaxInt32
		for _, p := range allowed {
			if p <= x && minParts[x-p]+1 < best {
				best = minParts[x-p] + 1
			}
		}
		minParts[x] = best
	}

	parts := []int{}
	rem := units
	for rem > 0 {
		slotsLeft := maxShards - len(parts)
		// candidates: a chunk p whose remainder can still finish within slotsLeft-1
		cand := []int{}
		for _, p := range allowed {
			if p <= rem && minParts[rem-p] <= slotsLeft-1 {
				cand = append(cand, p)
			}
		}
		if len(cand) == 0 {
			// Can't fit within the cap; take the largest chunk <= rem to finish fast.
			biggest := 0
			for _, p := range allowed {
				if p <= rem && p > biggest {
					biggest = p
				}
			}
			if biggest == 0 {
				break // remainder smaller than the smallest denom (shouldn't happen for valid deposits)
			}
			parts = append(parts, biggest)
			rem -= biggest
			continue
		}
		// Bias toward the smallest candidate so deposits spread across more shards.
		smallest := cand[0]
		for _, p := range cand {
			if p < smallest {
				smallest = p
			}
		}
		p := cand[rnd.Intn(len(cand))]
		if len(cand) > 1 && rnd.Float64() < 0.55 {
			p = smallest
		}
		parts = append(parts, p)
		rem -= p
	}

	out := make([]float64, len(parts))
	for i, p := range parts {
		out[i] = float64(p) * base
	}
	return out
}
