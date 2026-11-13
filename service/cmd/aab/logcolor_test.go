package main

import (
	"strings"
	"testing"
)

func TestRichToANSI(t *testing.T) {
	got := richToANSI("[font=aab-mono][color=1,0.63,0.259]bits-orio[/color] has [img=item.iron-plate] at [gps=-90,-287,nauvis][/font]")
	want := "\x1b[38;2;255;161;66mbits-orio" + ansiFg + " has " + ansiYellow + "‹iron-plate›" + ansiFg + " at " + ansiCyan + "@-90,-287,nauvis" + ansiFg
	if got != want {
		t.Errorf("richToANSI:\n got %q\nwant %q", got, want)
	}
}

func TestColorEntryGroupsAnAnswer(t *testing.T) {
	got := colorEntry("2026/10/11 21:04:12 answer 124 rendered:\n    first | cell\n    second\n")
	for _, part := range []string{"A124", "│" + ansiReset + " first | cell\n", "│" + ansiReset + " second\n"} {
		if !strings.Contains(got, part) {
			t.Errorf("colorEntry missing %q in %q", part, got)
		}
	}
	if q := colorEntry("2026/10/11 21:04:05 question 124 from bits-orio (player 1, force team-4): Hi?\n"); !strings.HasPrefix(q, "\n") || !strings.Contains(q, "Q124") {
		t.Errorf("a question should open a new block: %q", q)
	}
}
