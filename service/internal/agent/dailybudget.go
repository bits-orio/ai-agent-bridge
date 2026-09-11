// The daily budget: USD the service may spend on questions in a rolling
// day, whoever asks. The last blanket guard on cost: per-player and
// server-wide quotas bound the count, this bounds the bill when a model
// turns out dearer than expected.

package agent

import (
	"sync"
	"time"
)

const budgetWindow = 24 * time.Hour

type budget struct {
	mu    sync.Mutex
	limit float64
	spent []spend
}

type spend struct {
	at  time.Time
	usd float64
}

func newBudget(limit float64) *budget {
	return &budget{limit: limit}
}

// exhausted reports whether the rolling day's spend has reached the limit.
// A limit of zero or less means no cap.
func (b *budget) exhausted(now time.Time) bool {
	if b.limit <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total(now) >= b.limit
}

// spend records one answered question's cost.
func (b *budget) spend(usd float64, now time.Time) {
	if b.limit <= 0 || usd <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent = append(b.spent, spend{at: now, usd: usd})
}

// total is the rolling day's spend; older entries are dropped as it goes.
// Callers hold the lock.
func (b *budget) total(now time.Time) float64 {
	kept := b.spent[:0:0]
	sum := 0.0
	for _, s := range b.spent {
		if now.Sub(s.at) < budgetWindow {
			kept = append(kept, s)
			sum += s.usd
		}
	}
	b.spent = kept
	return sum
}
