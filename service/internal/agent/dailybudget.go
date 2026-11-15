// The daily budget: USD the service may spend on questions in a rolling
// day, whoever asks. The last blanket guard on cost: per-player and
// server-wide quotas bound the count, this bounds the bill when a model
// turns out dearer than expected.
//
// The spend outlives the process: the runner restores it at start and saves
// a snapshot after every spend (DailySpend, RestoreDailySpend,
// OnDailySpend), so a crash loop cannot start the day's cap from zero on
// every restart. The quotas stay in memory: they bound how often, and a
// restart costs at most one hour's allowance.

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
	// onSpend, when set, gets the rolling day's spend after every spend
	// that changed it, outside the lock.
	onSpend func([]SpendEntry)
}

type spend struct {
	at  time.Time
	usd float64
}

// SpendEntry is one spend as it is saved and restored: when, and how many
// USD.
type SpendEntry struct {
	At  time.Time `json:"at"`
	USD float64   `json:"usd"`
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
	b.spent = append(b.spent, spend{at: now, usd: usd})
	b.total(now)
	snap, hook := b.entriesLocked(), b.onSpend
	b.mu.Unlock()
	if hook != nil {
		hook(snap)
	}
}

// snapshot is the rolling day's spend as of now.
func (b *budget) snapshot(now time.Time) []SpendEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total(now)
	return b.entriesLocked()
}

// restore puts back spend saved by an earlier process, dropping whatever is
// older than the window as of now. It replaces what the budget holds.
func (b *budget) restore(entries []SpendEntry, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent = b.spent[:0:0]
	for _, e := range entries {
		if e.USD > 0 && now.Sub(e.At) < budgetWindow {
			b.spent = append(b.spent, spend{at: e.At, usd: e.USD})
		}
	}
}

// entriesLocked copies the spend out. Callers hold the lock.
func (b *budget) entriesLocked() []SpendEntry {
	out := make([]SpendEntry, len(b.spent))
	for i, s := range b.spent {
		out[i] = SpendEntry{At: s.at, USD: s.usd}
	}
	return out
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

// DailySpend is the rolling day's spend, for saving.
func (a *Agent) DailySpend() []SpendEntry { return a.budget.snapshot(time.Now()) }

// RestoreDailySpend puts back the spend an earlier process saved, entries
// older than a day dropped. Call it before the first question.
func (a *Agent) RestoreDailySpend(entries []SpendEntry) {
	a.budget.restore(entries, time.Now())
}

// OnDailySpend asks for fn to get the rolling day's spend after every spend,
// to save it. fn runs on the question's goroutine; set it before the first
// question.
func (a *Agent) OnDailySpend(fn func([]SpendEntry)) {
	a.budget.mu.Lock()
	defer a.budget.mu.Unlock()
	a.budget.onSpend = fn
}
