package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// A result under the cap passes through untouched; one over it is cut on a
// rune boundary and says how much is missing.
func TestContentCutsLongResultsOnARuneBoundary(t *testing.T) {
	short := json.RawMessage(`{"ok":true}`)
	if got := content(short, 4096); got != string(short) {
		t.Errorf("short result changed: %q", got)
	}
	if got := content(nil, 4096); got != "null" {
		t.Errorf("empty result = %q, want null", got)
	}

	// Two-byte runes, cut so that a naive slice would land mid-rune.
	long := json.RawMessage(`["` + strings.Repeat("é", 100) + `"]`)
	got := content(long, 101)
	if !utf8.ValidString(got) {
		t.Fatalf("cut result is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "[cut: 100 of 204 bytes shown; ask for fewer rows]") {
		t.Errorf("cut note missing or wrong: %q", got)
	}
	if !strings.HasPrefix(got, `["`+strings.Repeat("é", 49)) {
		t.Errorf("cut kept the wrong prefix: %q", got[:20])
	}
}

// A zero or negative cap means no cut at all, so a caller that leaves the
// caps empty still gets whole results.
func TestContentWithNoCapPassesEverything(t *testing.T) {
	long := json.RawMessage(strings.Repeat("x", 10000))
	if got := content(long, 0); got != string(long) {
		t.Errorf("no-cap content changed the result, len %d", len(got))
	}
	if c := (Caps{}).toolResultBytes(); c != DefaultMaxToolResultBytes {
		t.Errorf("unset cap = %d, want %d", c, DefaultMaxToolResultBytes)
	}
	if c := (Caps{}).roundToolResultBytes(); c != DefaultMaxRoundToolResultBytes {
		t.Errorf("unset round cap = %d, want %d", c, DefaultMaxRoundToolResultBytes)
	}
}
