package extension

import (
	"math/rand"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// DepositPool holds escrowed deposits between the on-chain DEPOSIT instruction and
// the actual Hyperliquid funding. It is the unlinkability engine for private
// deposits: a deposit is enqueued with a randomized future fundAfter time and is
// funded only in a BATCH with >= BatchMin other matured deposits, from the shared
// treasury, in a uniform denomination. That breaks the amount+timing correlation
// between a user's on-chain deposit and the (seed-derived) HL account that later
// gets funded. The account model itself is unchanged.
//
// One in-flight funding per user keeps the per-account amount uniform (a second
// deposit while one is pending is rejected upstream). In-memory, like the ledger.
type DepositPool struct {
	mu       sync.Mutex
	items    map[common.Address]*pendingFunding
	minDelay time.Duration
	maxDelay time.Duration
	rnd      *rand.Rand
}

type fundingState int

const (
	statePending fundingState = iota // waiting for fundAfter / a full batch
	stateFunding                     // selected into a batch, funding in progress
)

type pendingFunding struct {
	User        common.Address
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
		items:    map[common.Address]*pendingFunding{},
		minDelay: minDelay,
		maxDelay: maxDelay,
		rnd:      rand.New(rand.NewSource(seed)),
	}
}

// Enqueue records a deposit awaiting private funding. Returns false if the user
// already has an in-flight funding (one at a time keeps amounts uniform). FundAfter
// is randomized in [minDelay, maxDelay] from now, decorrelating funding from deposit.
func (p *DepositPool) Enqueue(user common.Address, denom float64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.items[user]; exists {
		return false
	}
	now := time.Now()
	jitter := p.maxDelay - p.minDelay
	delay := p.minDelay
	if jitter > 0 {
		delay += time.Duration(p.rnd.Int63n(int64(jitter) + 1))
	}
	p.items[user] = &pendingFunding{
		User:        user,
		Denom:       denom,
		DepositedAt: now,
		FundAfter:   now.Add(delay),
		State:       statePending,
	}
	return true
}

// EstimatedDelay returns how long until the user's deposit is eligible to fund
// (0 if already due or unknown). Used for the "funded privately in ~N" UX.
func (p *DepositPool) EstimatedDelay(user common.Address) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	it, ok := p.items[user]
	if !ok {
		return 0
	}
	d := time.Until(it.FundAfter)
	if d < 0 {
		return 0
	}
	return d
}

// DueBatch returns all matured (FundAfter<=now, statePending) deposits, but ONLY
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

// InFlight reports whether the user has a deposit awaiting or undergoing funding.
// Reconciliation uses this to skip users during the intentional funding delay, so
// it never credits the ledger early (which would collapse the delay and re-link).
func (p *DepositPool) InFlight(user common.Address) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.items[user]
	return ok
}

// Complete removes a user's entry after a successful funding+credit.
func (p *DepositPool) Complete(user common.Address) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, user)
}

// Fail returns a user's entry to statePending so a later tick retries it.
func (p *DepositPool) Fail(user common.Address) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if it, ok := p.items[user]; ok {
		it.State = statePending
	}
}

// PendingTotal is the sum of all enqueued (not-yet-credited) denominations. The
// deposit cap check adds this so pending escrow still counts against the cap.
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
