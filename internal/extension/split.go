package extension

import (
	"math"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// newSplitRand returns a per-deposit RNG so the random decomposition varies and is
// not shared (avoids racing the pool's RNG). Seeded from time + the user address.
func newSplitRand(user common.Address) *rand.Rand {
	seed := time.Now().UnixNano()
	for i := 0; i < 8 && i < len(user); i++ {
		seed = seed*31 + int64(user[len(user)-1-i])
	}
	return rand.New(rand.NewSource(seed))
}

// smallestDenom returns the minimum denomination (the floor a deposit must meet in
// split mode); 0 if there are none.
func smallestDenom(denoms []float64) float64 {
	m := 0.0
	for _, d := range denoms {
		if d > 0 && (m == 0 || d < m) {
			m = d
		}
	}
	return m
}

// gcdInt is the greatest common divisor (0 is the identity, so gcd(0,x)=x).
func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// sumFloats sums a slice.
func sumFloats(xs []float64) float64 {
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t
}

// splitIntoDenoms splits a deposit into a random number of fixed-denomination
// chunks whose sum is the LARGEST denominable amount <= usd (any sub-denomination
// remainder is left to the caller to snap off). Every chunk is a valid
// denomination, so uniformity is preserved (no chunk is a unique amount an
// observer could subset-sum). The chunk count is random and bounded by maxShards;
// the split is biased toward smaller chunks so a deposit spreads across several
// shard accounts. Returns the per-shard chunk amounts.
//
// Works for ANY denomination set (it reduces to the GCD of the denominations in
// cents, so e.g. {10,25,50,100,500} -> base $5), and never funds more than usd
// (it floors to the GCD grid). Falls back to a single chunk if it can't split.
func splitIntoDenoms(usd float64, denoms []float64, maxShards int, rnd *rand.Rand) []float64 {
	if usd <= 0 || len(denoms) == 0 || maxShards < 1 {
		return []float64{usd}
	}
	// Work in integer cents so fractional/non-multiple denominations are exact.
	cents := make([]int, 0, len(denoms))
	baseCents := 0
	for _, d := range denoms {
		c := int(math.Round(d * 100))
		if c > 0 {
			cents = append(cents, c)
			baseCents = gcdInt(baseCents, c)
		}
	}
	if baseCents <= 0 || len(cents) == 0 {
		return []float64{usd}
	}
	base := float64(baseCents) / 100
	// Floor the deposit onto the GCD grid: never fund more than was deposited.
	units := int(math.Floor((usd*100)/float64(baseCents) + 1e-9))
	if units <= 0 {
		return []float64{usd}
	}
	allowed := make([]int, 0, len(cents))
	seen := map[int]bool{}
	for _, c := range cents {
		u := c / baseCents
		if u >= 1 && !seen[u] {
			allowed = append(allowed, u)
			seen[u] = true
		}
	}
	if len(allowed) == 0 {
		return []float64{usd}
	}

	// minParts[x]/maxParts[x] = fewest/most allowed chunks that sum to x (DP,
	// exact). minParts guarantees the remainder can finish within the shard cap;
	// maxParts lets us force a minimum fan-out (so a big round deposit never lands
	// in a single shard, which would leak the amount 1:1).
	minParts := make([]int, units+1)
	maxParts := make([]int, units+1)
	for x := 1; x <= units; x++ {
		minParts[x] = math.MaxInt32
		maxParts[x] = -1
		for _, p := range allowed {
			if p > x {
				continue
			}
			if minParts[x-p]+1 < minParts[x] {
				minParts[x] = minParts[x-p] + 1
			}
			if maxParts[x-p] >= 0 && maxParts[x-p]+1 > maxParts[x] {
				maxParts[x] = maxParts[x-p] + 1
			}
		}
	}

	// Target a random chunk count in [2, cap] when the amount can be split at all,
	// so the fan-out size is itself unpredictable but always >= 2.
	maxCount := maxShards
	if feasibleMax := maxParts[units]; feasibleMax >= 0 && feasibleMax < maxCount {
		maxCount = feasibleMax
	}
	wantMin := 1
	if maxCount >= 2 {
		wantMin = 2 + rnd.Intn(maxCount-1)
	}

	parts := []int{}
	rem := units
	for rem > 0 {
		c := len(parts)
		slotsLeft := maxShards - c
		// A chunk p is a candidate if what remains can still finish within the shard
		// cap AND the total count can still reach wantMin (neither over- nor
		// under-shoot the target fan-out).
		cand := []int{}
		for _, p := range allowed {
			if p > rem {
				continue
			}
			r2, c2 := rem-p, c+1
			if r2 == 0 {
				if c2 >= wantMin && c2 <= maxShards {
					cand = append(cand, p)
				}
				continue
			}
			if minParts[r2] <= slotsLeft-1 && maxParts[r2] >= wantMin-c2 {
				cand = append(cand, p)
			}
		}
		if len(cand) == 0 {
			// Fallback: take the largest chunk that still finishes within the cap.
			biggest := 0
			for _, p := range allowed {
				if p <= rem && minParts[rem-p] <= slotsLeft-1 && p > biggest {
					biggest = p
				}
			}
			if biggest == 0 {
				for _, p := range allowed {
					if p <= rem && p > biggest {
						biggest = p
					}
				}
			}
			if biggest == 0 {
				break // remainder smaller than the smallest denom (shouldn't happen)
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
		if len(cand) > 1 && rnd.Float64() < 0.6 {
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
