package model

import "testing"

// The budget follows the bill: a cached input token counts a tenth, so a
// prefix re-read every round does not spend the budget the way fresh tokens do.
func TestBudgetedWeighsCacheReads(t *testing.T) {
	u := Usage{InputTokens: 300, OutputTokens: 200, CacheWriteTokens: 100, CacheReadTokens: 5000}
	if got := u.Total(); got != 5600 {
		t.Errorf("Total = %d, want 5600", got)
	}
	if got := u.Budgeted(); got != 1100 {
		t.Errorf("Budgeted = %d, want 1100", got)
	}
}
