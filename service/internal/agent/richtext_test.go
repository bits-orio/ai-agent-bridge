package agent

import (
	"strings"
	"testing"
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

// A cut that falls inside "[/color]" must not keep the torn "[/col" and must
// still close the colour that was open.
func TestClipRichTextRepairsACutInsideACloser(t *testing.T) {
	got := clipRichText("see [color=yellow]Team 19[/color] now", 26)
	want := "see [color=yellow]Team 19[/color]"
	if got != want {
		t.Errorf("clipRichText = %q, want %q", got, want)
	}
}

// Nested spans close innermost first, the order Factorio expects.
func TestClipRichTextClosesNestedSpansInOrder(t *testing.T) {
	got := clipRichText("[color=red][font=default-bold]warning text that runs long", 30)
	if !strings.HasSuffix(got, "[/font][/color]") {
		t.Errorf("nested spans not closed innermost first: %q", got)
	}
}

// Plain text and text within the limit are exactly what clipRunes gave.
func TestClipRichTextIsAPlainClipWhenNoTagIsHarmed(t *testing.T) {
	if got := clipRichText("short", 10); got != "short" {
		t.Errorf("within limit changed: %q", got)
	}
	if got, want := clipRichText("a plain sentence with no tags in it", 7), "a plain"; got != want {
		t.Errorf("plain clip = %q, want %q", got, want)
	}
	// A gps tag has no closer and must not gain one.
	got := clipRichText("[gps=1,2,nauvis] and more words here", 16)
	if strings.Contains(got, "[/") {
		t.Errorf("a single tag was given a closer: %q", got)
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
