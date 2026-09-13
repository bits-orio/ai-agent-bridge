package ledger

import (
	"strings"
	"testing"
)

// TestClipLeavesShortStringsAlone: nothing at or under the limit is
// touched, byte for byte, and no note is appended.
func TestClipLeavesShortStringsAlone(t *testing.T) {
	s := "a short error"
	if got := clip(s, maxClipBytes); got != s {
		t.Errorf("clip(%q, %d) = %q, want it unchanged", s, maxClipBytes, got)
	}
	exact := strings.Repeat("x", 10)
	if got := clip(exact, 10); got != exact {
		t.Errorf("clip(%q, 10) = %q, want it unchanged at exactly the limit", exact, got)
	}
}

// TestClipAppendsNoteOnlyWhenItCuts: one byte over the limit is enough to
// trigger a cut, and the note is only ever appended when a cut actually
// happened.
func TestClipAppendsNoteOnlyWhenItCuts(t *testing.T) {
	s := strings.Repeat("x", 10)
	got := clip(s, 9)
	if got == s {
		t.Fatalf("clip(%q, 9) = %q, want it cut", s, got)
	}
	if !strings.HasSuffix(got, clipNote) {
		t.Errorf("clip(%q, 9) = %q, want it to end with the clip note", s, got)
	}
}

// TestClipCutsAtRuneBoundary: a multi-byte rune sitting right at the cut
// point is never split; clip backs off to the rune boundary before it.
func TestClipCutsAtRuneBoundary(t *testing.T) {
	s := strings.Repeat("€", 10) // euro sign, 3 bytes each: 30 bytes total
	got := clip(s, 29)           // 29 lands mid-rune on the 10th euro sign
	if !strings.HasSuffix(got, clipNote) {
		t.Fatalf("clip(%q, 29) = %q, want it to end with the clip note", s, got)
	}
	body := strings.TrimSuffix(got, clipNote)
	if !strings.Contains(s, body) {
		t.Fatalf("clip(%q, 29) body %q is not a prefix of the input, rune got split", s, body)
	}
	if len(body) != 27 {
		t.Errorf("clipped body is %d bytes, want 27 (backed off from 29 to the rune boundary)", len(body))
	}
}
