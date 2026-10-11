package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The exact shape of question 91's first line: a cut that lands inside a
// colour tag. Whatever survives must render, which means no dangling "[" and
// every opened span closed.
func TestClipRichTextNeverLeavesATagOpenOrTornAtTheCut(t *testing.T) {
	line := "20 team slots total: 15 claimed and 5 empty ([color=yellow]Team 10[/color], [color=yellow]Team 17[/color], [color=yellow]Team 19[/color])"

	for limit := 20; limit < len([]rune(line)); limit++ {
		got := clipRichText(line, limit)
		if open := strings.LastIndex(got, "["); open > strings.LastIndex(got, "]") {
			t.Fatalf("limit %d: a tag is torn open at the cut: %q", limit, got)
		}
		opens := strings.Count(got, "[color=")
		closes := strings.Count(got, "[/color]")
		if opens != closes {
			t.Fatalf("limit %d: %d colour tags opened, %d closed: %q", limit, opens, closes, got)
		}
	}
}

// A cut whose rune count falls inside "[/color]" keeps no torn "[/col",
// still closes the colour that was open, and still fits the limit with the
// closer it had to add.
func TestClipRichTextRepairsACutInsideACloser(t *testing.T) {
	// Rune 30 of this line is inside "[/color]".
	got := clipRichText("see [color=yellow]Team 19[/color] now", 31)
	want := "see [color=yellow]Team…[/color]"
	if got != want {
		t.Errorf("clipRichText = %q, want %q", got, want)
	}
}

// A word cut never lands inside an ordinary tag, since a tag has no space in
// it, but a colour written with spaces does, [color=1, 0.5, 0]. A cut there
// drops the torn tag and everything after it rather than leave a "[" that
// makes Factorio print the whole line raw.
func TestClipRichTextDropsATagTheCutLandsInside(t *testing.T) {
	got := clipRichText("Alert: [color=1, 0.5, 0]warning text[/color] and more after it", 20)
	if want := "Alert…"; got != want {
		t.Errorf("clipRichText = %q, want %q", got, want)
	}
}

// Nested spans close innermost first, the order Factorio expects.
func TestClipRichTextClosesNestedSpansInOrder(t *testing.T) {
	got := clipRichText("[color=red][font=default-bold]warning text that runs long past the cap of this line", 60)
	if want := "[color=red][font=default-bold]warning text…[/font][/color]"; got != want {
		t.Errorf("nested spans not closed innermost first: %q, want %q", got, want)
	}
}

// Text within the limit is left exactly as it was, with no ellipsis; text
// over it is cut after the last word that fits, with room for the ellipsis.
func TestClipRichTextCutsPlainTextBetweenWords(t *testing.T) {
	if got := clipRichText("short", 10); got != "short" {
		t.Errorf("within limit changed: %q", got)
	}
	line := "a plain sentence with no tags in it"
	if got := clipRichText(line, len([]rune(line))); got != line {
		t.Errorf("exactly at the limit changed: %q", got)
	}
	for _, tc := range []struct {
		limit int
		want  string
	}{
		// "a plain" is seven runes, which leaves no room for the ellipsis, and
		// the boundary after "a" keeps one rune of seven: under half, so the
		// cut falls at the rune.
		{7, "a plai…"},
		{16, "a plain…"},
		{17, "a plain sentence…"},
	} {
		if got := clipRichText(line, tc.limit); got != tc.want {
			t.Errorf("limit %d: clip = %q, want %q", tc.limit, got, tc.want)
		}
	}
	// A gps tag has no closer and must not gain one.
	got := clipRichText("[gps=1,2,nauvis] and more words here", 16)
	if strings.Contains(got, "[/") {
		t.Errorf("a single tag was given a closer: %q", got)
	}
}

// The rule the cut exists for, at every limit a real line can meet: in a
// sentence of ordinary words a boundary is always near enough, so the cut
// never splits a word, ends with the ellipsis, and fits the limit with
// everything it adds.
func TestClipRichTextNeverCutsAWordInHalf(t *testing.T) {
	line := "Research: team-4 is 77% through rocket-silo, and your force is 12% through automation-2, so team-4 finishes first by a wide margin"
	// From 10 up the first word, "Research", fits with its ellipsis. Below
	// that no word fits at all, and the rune cut is the only cut there is
	// (TestClipRichTextCutsSpacelessScriptsBetweenCharacters).
	for limit := 10; limit < len([]rune(line)); limit++ {
		got := clipRichText(line, limit)
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("limit %d: %q is %d runes", limit, got, n)
		}
		kept, ok := strings.CutSuffix(got, ellipsis)
		if !ok {
			t.Fatalf("limit %d: %q does not end with the ellipsis", limit, got)
		}
		if !strings.HasPrefix(line, kept) {
			t.Fatalf("limit %d: %q is not the start of the line", limit, kept)
		}
		if rest := line[len(kept):]; kept != "" && strings.IndexAny(rest[:1], " ,:") < 0 {
			t.Fatalf("limit %d: cut mid-word, %q then %q", limit, kept, rest)
		}
	}
}

// A boundary is worth losing up to half the limit for and no more. One long
// word after a short one would otherwise throw the whole line away and keep
// "ok…"; past half, the cut falls at the rune and keeps what fits.
func TestClipRichTextCutsAtTheRuneWhenTheBoundaryKeepsUnderHalf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{"one long word after a short one", "ok " + strings.Repeat("y", 100), 40, "ok " + strings.Repeat("y", 36) + ellipsis},
		{"a boundary keeping exactly half is taken", "abcdefghi " + strings.Repeat("z", 30), 20, "abcdefghi" + ellipsis},
		{"a boundary one rune short of half is not", "abcdefgh " + strings.Repeat("z", 30), 20, "abcdefgh " + strings.Repeat("z", 10) + ellipsis},
		{"half of an odd limit rounds up", "abcdefghi " + strings.Repeat("z", 30), 21, "abcdefghi " + strings.Repeat("z", 10) + ellipsis},
		{"and is taken when reached", "abcdefghij " + strings.Repeat("z", 30), 21, "abcdefghij" + ellipsis},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clipRichText(tc.text, tc.limit)
			if got != tc.want {
				t.Errorf("clipRichText(%d) = %q, want %q", tc.limit, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > tc.limit {
				t.Errorf("%q is %d runes, over %d", got, n, tc.limit)
			}
		})
	}
}

// Clipping is idempotent: a cell cut once and checked again, the way finish
// re-validates an answer runCalls already read, comes back unchanged.
func TestClipRichTextOfItsOwnOutputChangesNothing(t *testing.T) {
	line := "20 team slots total: 15 claimed and 5 empty ([color=yellow]Team 10[/color], [color=yellow]Team 17[/color], [color=yellow]Team 19[/color])"
	for limit := 1; limit < len([]rune(line)); limit++ {
		once := clipRichText(line, limit)
		if twice := clipRichText(once, limit); twice != once {
			t.Fatalf("limit %d: %q clipped again became %q", limit, once, twice)
		}
	}
}

// Chinese and Japanese put no space between words, so a cut between any two
// of their characters is between words. One unbroken word longer than the
// cap, a run of letters with no space at all, has nowhere else to go and is
// cut at the rune.
func TestClipRichTextCutsSpacelessScriptsBetweenCharacters(t *testing.T) {
	han := "team-4 " + strings.Repeat("研究进度", 20)
	got := clipRichText(han, 30)
	if want := "team-4 " + strings.Repeat("研究进度", 5) + "研究" + ellipsis; got != want {
		t.Errorf("Han text cut = %q, want %q: a cut between ideographs is a word boundary, so it must not fall back to the space after team-4", got, want)
	}
	word := strings.Repeat("x", 50)
	if got, want := clipRichText(word, 10), strings.Repeat("x", 9)+ellipsis; got != want {
		t.Errorf("one long word = %q, want %q", got, want)
	}
}

// The wiring, not the function: question 91's answer went through validate
// and the byte-budget shrinker, and it was the shrinker's cut that tore the
// tag. Build that answer, run it through the same path, and require every
// surviving line to render.
func TestValidateNeverTearsARichTextTagWhenItShrinksAnAnswer(t *testing.T) {
	var teams []string
	for i := 1; i <= 20; i++ {
		teams = append(teams, "[color=yellow]Team "+string(rune('0'+i%10))+string(rune('0'+i/10))+"[/color]")
	}
	long := "20 team slots total: 15 claimed and 5 empty (" + strings.Join(teams, ", ") + ")"
	a := Artifact{Shape: ShapeSummary, Lines: []string{long, "So yes, 5 empty slots, not 8. The other zero-player forces ([color=gray]enemy[/color], [color=gray]neutral[/color], [color=gray]player[/color]) are engine forces and " + strings.Repeat("padding ", 40)}}

	got, err := validate(a)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(got.Lines) == 0 {
		t.Fatal("validate dropped every line")
	}
	for i, line := range got.Lines {
		if open := strings.LastIndex(line, "["); open > strings.LastIndex(line, "]") {
			t.Errorf("line %d is torn at a tag and would render raw: %q", i, line)
		}
		if strings.Count(line, "[color=") != strings.Count(line, "[/color]") {
			t.Errorf("line %d leaves a colour span open: %q", i, line)
		}
	}
	if !within(got) {
		t.Errorf("shrunken answer still over the byte budget")
	}
}
