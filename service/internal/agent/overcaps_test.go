package agent

import (
	"fmt"
	"strings"
	"testing"
)

// overCaps names each thing over a cap the way the model needs to hear it:
// which line, item, row or cell, how long, the cap, and what to do instead.
func TestOverCapsNamesWhatIsOverInEveryShape(t *testing.T) {
	long := strings.Repeat("y", 410)
	fine := "ok"
	for _, tc := range []struct {
		name     string
		artifact Artifact
		want     []string
	}{
		{
			"the live case, a summary line",
			Artifact{Shape: ShapeSummary, Lines: []string{fine, long}},
			[]string{fmt.Sprintf("Line 2 is 410 characters, the cap is %d: rewrite it shorter or use another line, at most %d lines.", MaxCellChars, MaxSummaryLines)},
		},
		{
			"one character over the cap",
			Artifact{Shape: ShapeSummary, Lines: []string{strings.Repeat("y", MaxCellChars+1)}},
			[]string{fmt.Sprintf("Line 1 is %d characters, the cap is %d", MaxCellChars+1, MaxCellChars)},
		},
		{
			"too many summary lines",
			Artifact{Shape: ShapeSummary, Lines: []string{fine, fine, fine, fine, fine}},
			[]string{fmt.Sprintf("This summary has 5 lines, the cap is %d", MaxSummaryLines), fmt.Sprintf("send a list, which holds %d", MaxListItems)},
		},
		{
			"a long title on any shape",
			Artifact{Shape: ShapeList, Title: long, Items: []string{fine}},
			[]string{fmt.Sprintf("The title is 410 characters, the cap is %d: shorten it.", MaxCellChars)},
		},
		{
			"a long notice",
			Artifact{Shape: ShapeNotice, Text: long},
			[]string{fmt.Sprintf("The notice is 410 characters, the cap is %d", MaxCellChars)},
		},
		{
			"too many list items and a long one",
			Artifact{Shape: ShapeList, Items: append(make([]string, MaxListItems+2), long)},
			[]string{fmt.Sprintf("This list has %d items, the cap is %d", MaxListItems+3, MaxListItems), fmt.Sprintf("Item %d is 410 characters", MaxListItems+3)},
		},
		{
			"a table too wide and too long, with a long cell and a long column name",
			Artifact{
				Shape:   ShapeTable,
				Columns: []string{"a", long, "c", "d", "e", "f"},
				Rows:    append(make([][]string, MaxTableRows), []string{fine, fine, long}),
			},
			[]string{
				fmt.Sprintf("This table has 6 columns, the cap is %d", MaxTableColumns),
				"Column 2's name is 410 characters",
				fmt.Sprintf("This table has %d rows, the cap is %d", MaxTableRows+1, MaxTableRows),
				fmt.Sprintf("Row %d, column 3 is 410 characters", MaxTableRows+1),
			},
		},
		{
			"a comparison of three things, too long, with a long b",
			Artifact{
				Shape:   ShapeComparison,
				Columns: []string{"team-1", "team-2", "team-3"},
				Pairs:   append(make([]Pair, MaxComparisonRows), Pair{Label: "x", A: "1", B: long}),
			},
			[]string{
				"names 3: send a table to show more",
				fmt.Sprintf("This comparison has %d rows, the cap is %d", MaxComparisonRows+1, MaxComparisonRows),
				fmt.Sprintf("Row %d's b is 410 characters", MaxComparisonRows+1),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(overCaps(tc.artifact), " ")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("overCaps does not say %q:\n%s", want, got)
				}
			}
		})
	}
}

// Within every cap there is nothing to say, at the cap included, and a cell
// is measured on one line, the way the player reads it: whitespace a model
// added is not held against it.
func TestOverCapsIsEmptyWithinEveryCap(t *testing.T) {
	full := strings.Repeat("y", MaxCellChars)
	padded := strings.Repeat("y", MaxCellChars-2) + "\n\n   \t" + "y" // 325 runes raw, 320 on one line
	for _, a := range []Artifact{
		{Shape: ShapeSummary, Title: full, Lines: []string{full, padded, full}},
		{Shape: ShapeNotice, Text: full},
		{Shape: ShapeList, Items: make([]string, MaxListItems)},
		{Shape: ShapeTable, Columns: []string{"a", "b", "c", "d", "e"}, Rows: make([][]string, MaxTableRows)},
		{Shape: ShapeComparison, Columns: []string{"a", "b"}, Pairs: make([]Pair, MaxComparisonRows)},
	} {
		if over := overCaps(a); len(over) != 0 {
			t.Errorf("%s within its caps reported over: %q", a.Shape, over)
		}
	}
}

// A table inside every count and cell cap can still be too big to send: the
// byte budget is a cap like the others and is named with its own numbers.
func TestOverCapsNamesTheByteBudget(t *testing.T) {
	full := strings.Repeat("x", MaxCellChars)
	over := overCaps(maxedTable(full))
	if len(over) != 1 {
		t.Fatalf("overCaps = %q, want only the byte budget", over)
	}
	if !strings.HasPrefix(over[0], "The whole answer is ") || !strings.Contains(over[0], fmt.Sprintf("the cap is %d", MaxArtifactBytes)) {
		t.Errorf("byte budget sentence = %q", over[0])
	}

	long := maxedTable(strings.Repeat("x", MaxCellChars+50))
	got := overCaps(long)
	if last := got[len(got)-1]; !strings.HasPrefix(last, "Even cut to those caps it would be ") {
		t.Errorf("with cells over too, the byte sentence should say the cut copy is still too big: %q", last)
	}
}

// The refusal says nothing was sent, names at most maxListedOver problems
// with a count of the rest, and tells the model to send it again.
func TestOverCapsRefusalIsBoundedAndSaysWhatToDo(t *testing.T) {
	rows := make([][]string, MaxTableRows)
	for i := range rows {
		rows[i] = []string{strings.Repeat("y", 400), strings.Repeat("y", 400)}
	}
	over := overCaps(Artifact{Shape: ShapeTable, Columns: []string{"a", "b"}, Rows: rows})
	if len(over) <= maxListedOver {
		t.Fatalf("test needs more than %d problems, got %d", maxListedOver, len(over))
	}
	text := overCapsRefusal(over)
	if !strings.HasPrefix(text, "Not sent, nothing reached the player") {
		t.Errorf("refusal does not say nothing was sent: %q", text)
	}
	if !strings.HasSuffix(text, "call "+SubmitTool+" again.") {
		t.Errorf("refusal does not ask for the answer again: %q", text)
	}
	if n := strings.Count(text, " characters, the cap is "); n != maxListedOver {
		t.Errorf("refusal names %d problems, want %d", n, maxListedOver)
	}
	if want := fmt.Sprintf("And %d more like these.", len(over)-maxListedOver); !strings.Contains(text, want) {
		t.Errorf("refusal does not count the rest, want %q in %q", want, text)
	}
	if strings.Contains(text, "\n") {
		t.Errorf("refusal spans lines: %q", text)
	}
}
