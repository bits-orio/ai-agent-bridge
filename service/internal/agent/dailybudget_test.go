package agent

import (
	"testing"
	"time"
)

// S3: the rolling day's spend survives a restart. A snapshot taken after
// each spend, restored into a fresh budget, keeps the cap where it was, and
// entries older than the window are dropped on the way in.
func TestTheDailySpendSurvivesARestart(t *testing.T) {
	now := time.Now()
	b := newBudget(1.0)
	var saved []SpendEntry
	b.onSpend = func(s []SpendEntry) { saved = s }
	b.spend(0.6, now.Add(-time.Hour))
	b.spend(0.5, now)
	if len(saved) != 2 {
		t.Fatalf("the hook saw %d entries, want 2", len(saved))
	}

	fresh := newBudget(1.0)
	fresh.restore(append(saved, SpendEntry{At: now.Add(-25 * time.Hour), USD: 9}), now)
	if !fresh.exhausted(now) {
		t.Error("a restarted budget forgot the day's spend")
	}
	if got := fresh.snapshot(now); len(got) != 2 {
		t.Errorf("restored %d entries, want 2: the one older than a day is dropped", len(got))
	}
	if fresh.exhausted(now.Add(23*time.Hour + 30*time.Minute)) {
		t.Error("the spend never ages out of the window")
	}
}

// The Agent's own hooks reach the budget it charges.
func TestTheAgentExposesItsDailySpend(t *testing.T) {
	a := New(nil, Caps{MaxCostPerDay: 2})
	var saved []SpendEntry
	a.OnDailySpend(func(s []SpendEntry) { saved = s })
	a.RestoreDailySpend([]SpendEntry{{At: time.Now(), USD: 2}})
	if !a.budget.exhausted(time.Now()) {
		t.Error("RestoreDailySpend did not reach the budget")
	}
	a.budget.spend(0.1, time.Now())
	if len(saved) != 2 || len(a.DailySpend()) != 2 {
		t.Errorf("hook saw %d, DailySpend %d, want 2 and 2", len(saved), len(a.DailySpend()))
	}
}
