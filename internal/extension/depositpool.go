package extension

import (
	"math/rand"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// DepositPool holds escrowed deposits between the DEPOSIT instruction and the
// actual Hyperliquid funding. It is the unlinkability engine for private deposits:
// an entry is enqueued with a randomized future fundAfter time and funded only in a
// BATCH with >= BatchMin other matured entries, from the shared treasury, in a
// uniform denomination. That breaks the amount+timing correlation between a user's
// deposit and the HL account that later gets funded.
//
// Entries are keyed by (user, shardIndex). In single-account mode shardIndex is
// always 0 (one entry per user, the original behavior). With HL_SPLIT_DEPOSITS a
// deposit enqueues several entries - one per shard account - so the funding spreads
// across multiple standalone accounts. In-memory, like the ledger.
type DepositPool struct {
	mu       sync.Mutex
	items    map[shardKey]*pendingFunding
	minDelay time.Duration
	maxDelay time.Duration
	rnd      *rand.Rand
}

// shardKey identifies one pending funding: a user's shard-index slot.
type shardKey struct {
	User  common.Address
	Shard int
}

type fundingState int

const (
	statePending fundingState = iota // waiting for fundAfter / a full batch
	stateFunding                     // selected into a batch, funding in progress
)

type pendingFunding struct {
	User        common.Address
	Shard       int
	Denom       float64
	DepositedAt time.Time
	FundAfter   time.Time
	State       fundingState
}

// NewDepositPool builds a pool with the given randomized delay window. seed makes
// the jitter deterministic for tests; pass 0 for time-seeded randomness.
func NewDepositPool(minDelay, maxDelay time.Duration, seed int64) *DepositPool {
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &DepositPool{
		items:    map[shardKey]*pendingFunding{},
		minDelay: minDelay,
		maxDelay: maxDelay,
		rnd:      rand.New(rand.NewSource(seed)),
	}
}

// Enqueue records a deposit chunk awaiting private funding for (user, shard).
// Returns false if that exact shard slot already has an in-flight funding (so the
// caller can pick a different free shard). FundAfter is randomized in
// [minDelay, maxDelay] from now, decorrelating funding from deposit.
func (p *DepositPool) Enqueue(user common.Address, shard int, denom float64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := shardKey{user, shard}
	if _, exists := p.items[key]; exists {
		return false
	}
	now := time.Now()
	jitter := p.maxDelay - p.minDelay
	delay := p.minDelay
	if jitter > 0 {
		delay += time.Duration(p.rnd.Int63n(int64(jitter) + 1))
	}
	p.items[key] = &pendingFunding{
		User:        user,
		Shard:       shard,
		Denom:       denom,
		DepositedAt: now,
		FundAfter:   now.Add(delay),
		State:       statePending,
	}
	return true
}

// EstimatedDelay returns how long until the user's LAST chunk is eligible to fund
// (0 if none / all due). Used for the "funded privately in ~N" UX; with several
// shards it reflects when the whole deposit should have landed.
func (p *DepositPool) EstimatedDelay(user common.Address) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	var max time.Duration
	now := time.Now()
	for k, it := range p.items {
		if k.User != user {
			continue
		}
		if d := it.FundAfter.Sub(now); d > max {
			max = d
		}
	}
	return max
}

// DueBatch returns all matured (FundAfter<=now, statePending) entries, but ONLY
// when at least k of them are due (the anonymity-set floor). Returned entries are
// marked stateFunding so a concurrent tick won't re-select them. Returns nil when
// fewer than k are due (the caller waits for the next tick).
func (p *DepositPool) DueBatch(k int) []pendingFunding {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var due []*pendingFunding
	for _, it := range p.items {
		if it.State == statePending && !it.FundAfter.After(now) {
			due = append(due, it)
		}
	}
	if len(due) < k {
		return nil
	}
	out := make([]pendingFunding, 0, len(due))
	for _, it := range due {
		it.State = stateFunding
		out = append(out, *it)
	}
	return out
}

// InFlight reports whether the user has ANY shard awaiting or undergoing funding.
// Reconciliation uses this to skip users during the intentional funding delay, so
// it never credits the ledger early (which would collapse the delay and re-link).
func (p *DepositPool) InFlight(user common.Address) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.items {
		if k.User == user {
			return true
		}
	}
	return false
}

// FreeShards returns the lowest `need` shard indices in [0, maxShards) that are not
// currently in flight for the user, so a deposit can place its chunks on free
// slots (reusing shards freed by earlier completed deposits).
func (p *DepositPool) FreeShards(user common.Address, maxShards, need int) []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, 0, need)
	for shard := 0; shard < maxShards && len(out) < need; shard++ {
		if _, busy := p.items[shardKey{user, shard}]; !busy {
			out = append(out, shard)
		}
	}
	return out
}

// Complete removes a (user, shard) entry after a successful funding+credit.
func (p *DepositPool) Complete(user common.Address, shard int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, shardKey{user, shard})
}

// Fail returns a (user, shard) entry to statePending so a later tick retries it.
func (p *DepositPool) Fail(user common.Address, shard int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if it, ok := p.items[shardKey{user, shard}]; ok {
		it.State = statePending
	}
}

// Expire drops entries pending longer than maxAge (funding kept failing, e.g. the
// treasury could not fund them) and returns the removed entries. Self-heal so an
// un-fundable deposit/withdraw never blocks the account forever. The caller handles
// compensation (the withdraw funder refunds the ledger, since a withdraw debits up
// front).
func (p *DepositPool) Expire(maxAge time.Duration) []pendingFunding {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var dropped []pendingFunding
	for key, it := range p.items {
		if now.Sub(it.DepositedAt) > maxAge {
			dropped = append(dropped, *it)
			delete(p.items, key)
		}
	}
	return dropped
}

// PendingTotal is the sum of all enqueued (not-yet-credited) denominations across
// all users/shards. The deposit cap check adds this so pending escrow still counts.
func (p *DepositPool) PendingTotal() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var t float64
	for _, it := range p.items {
		t += it.Denom
	}
	return t
}

// Len is the number of entries currently in the pool.
func (p *DepositPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items)
}
