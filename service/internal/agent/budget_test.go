package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func marshalledSize(t *testing.T, a Artifact) int {
	t.Helper()
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return len(out)
}

func maxedTable(cell string) Artifact {
	columns := make([]string, 0, MaxTableColumns)
	for range MaxTableColumns {
		columns = append(columns, cell)
	}
	rows := make([][]string, 0, MaxTableRows)
	for range MaxTableRows {
		row := make([]string, 0, MaxTableColumns)
		for range MaxTableColumns {
			row = append(row, cell)
		}
		rows = append(rows, row)
	}
	return Artifact{Shape: ShapeTable, Title: cell, Columns: columns, Rows: rows}
}

// A table at every shape cap marshals to well over the 12000-byte budget.
// Trailing rows go until it fits, and the cells that stay are untouched: the
// rows a model wrote first are the ones it thought mattered.
func TestByteBudgetDropsRowsOfAMaxedTable(t *testing.T) {
	got, err := validate(maxedTable(strings.Repeat("x", MaxCellChars)))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	size := marshalledSize(t, got)
	if size > MaxArtifactBytes {
		t.Fatalf("artifact is %d bytes, over the %d budget", size, MaxArtifactBytes)
	}
	if len(got.Rows) < 2 || len(got.Rows) >= MaxTableRows {
		t.Fatalf("kept %d rows, want some dropped and several kept", len(got.Rows))
	}
	if len(got.Columns) != MaxTableColumns {
		t.Errorf("columns = %d, want %d: columns are not rows", len(got.Columns), MaxTableColumns)
	}
	for i, row := range got.Rows {
		for j, cell := range row {
			if len([]rune(cell)) != MaxCellChars {
				t.Fatalf("row %d cell %d is %d runes: cells were shortened when dropping rows was enough", i, j, len([]rune(cell)))
			}
		}
	}
}

// Cells of four-byte runes blow the budget even one row at a time, so the
// shortening pass runs. One row survives, every cell is shorter than the
// per-cell cap and ends with the ellipsis, and nothing is cut mid-character.
func TestByteBudgetShortensCellsWhenOneRowIsStillTooBig(t *testing.T) {
	wide := strings.Repeat("\U0002000B", MaxCellChars) // a four-byte rune
	got, err := validate(maxedTable(wide))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	size := marshalledSize(t, got)
	if size > MaxArtifactBytes {
		t.Fatalf("artifact is %d bytes, over the %d budget", size, MaxArtifactBytes)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("kept %d rows, want 1", len(got.Rows))
	}
	for _, cell := range got.Rows[0] {
		runes := len([]rune(cell))
		if runes >= MaxCellChars || runes < minCellRunes {
			t.Fatalf("cell is %d runes, want it shortened but not emptied", runes)
		}
		kept, cut := strings.CutSuffix(cell, ellipsis)
		if !cut {
			t.Fatalf("a shortened cell does not say it was cut: %q", cell)
		}
		if !strings.HasPrefix(wide, kept) {
			t.Fatal("a cell was cut mid-character")
		}
	}
}

// fit shortens with the same cut validate makes: between words, ending with
// the ellipsis. Words of four-byte emoji make one row alone too big for the
// budget, so the shortening pass has to run, and every cell it shortened
// still ends on a whole word.
func TestByteBudgetShortensCellsBetweenWords(t *testing.T) {
	word := strings.Repeat("\U0001F3ED", 9) // a factory emoji, four bytes, not a spaceless script
	cellText := strings.TrimSpace(strings.Repeat(word+" ", 32))
	if n := len([]rune(cellText)); n > MaxCellChars {
		t.Fatalf("test cell is %d runes, over the cell cap: fix the test", n)
	}
	got, err := validate(maxedTable(cellText))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if size := marshalledSize(t, got); size > MaxArtifactBytes {
		t.Fatalf("artifact is %d bytes, over the %d budget", size, MaxArtifactBytes)
	}
	shortened := 0
	for _, cell := range append(append([]string{got.Title}, got.Columns...), got.Rows[0]...) {
		kept, cut := strings.CutSuffix(cell, ellipsis)
		if !cut {
			t.Fatalf("a cell came through whole when one row cannot fit: %d runes", len([]rune(cell)))
		}
		shortened++
		if rest := cellText[len(kept):]; !strings.HasPrefix(cellText, kept) || !strings.HasPrefix(rest, " ") {
			t.Fatalf("fit cut a cell mid-word: %q", cell)
		}
	}
	if shortened == 0 {
		t.Fatal("nothing was shortened: the test no longer reaches the shortening pass")
	}
}

// The caps the companion mirrors (the shared contract, item 3): a cell is
// 320 characters, an answer 12000 bytes, the counts unchanged. The companion
// clips at the same numbers, so changing one here without the other leaves
// the game cutting what the service let through.
func TestCapsMatchTheCompanionContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want int
	}{
		{"MaxCellChars", MaxCellChars, 320},
		{"MaxArtifactBytes", MaxArtifactBytes, 12000},
		{"MaxSummaryLines", MaxSummaryLines, 3},
		{"MaxListItems", MaxListItems, 10},
		{"MaxTableColumns", MaxTableColumns, 5},
		{"MaxTableRows", MaxTableRows, 8},
		{"MaxComparisonRows", MaxComparisonRows, 5},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// A summary at every cap fits the budget whole: three full lines and a full
// title are an ordinary answer now, not one for fit to shrink.
func TestByteBudgetKeepsASummaryAtItsCapsWhole(t *testing.T) {
	full := strings.Repeat("y", MaxCellChars)
	in := Artifact{Shape: ShapeSummary, Title: full, Lines: []string{full, full, full}}
	got, err := validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(got.Lines) != MaxSummaryLines || got.Title != full {
		t.Fatalf("a summary at its caps was shrunk: %d lines, title %d runes", len(got.Lines), len([]rune(got.Title)))
	}
	for i, line := range got.Lines {
		if line != full {
			t.Errorf("line %d was cut: %d runes", i, len([]rune(line)))
		}
	}
}

// Every shape the model can submit comes back inside the budget, whatever it
// fills the caps with.
func TestByteBudgetHoldsForEveryShape(t *testing.T) {
	cell := strings.Repeat("\U0002000B", MaxCellChars)
	lines := []string{cell, cell, cell}
	items := make([]string, 0, MaxListItems)
	for range MaxListItems {
		items = append(items, cell)
	}
	pairs := make([]Pair, 0, MaxComparisonRows)
	for range MaxComparisonRows {
		pairs = append(pairs, Pair{Label: cell, A: cell, B: cell})
	}

	for _, a := range []Artifact{
		{Shape: ShapeSummary, Title: cell, Lines: lines},
		{Shape: ShapeNotice, Title: cell, Text: cell, Level: LevelWarning},
		{Shape: ShapeList, Title: cell, Items: items},
		maxedTable(cell),
		{Shape: ShapeComparison, Title: cell, Columns: []string{cell, cell}, Pairs: pairs},
	} {
		got, err := validate(a)
		if err != nil {
			t.Fatalf("validate %s: %v", a.Shape, err)
		}
		if size := marshalledSize(t, got); size > MaxArtifactBytes {
			t.Errorf("%s is %d bytes, over the %d budget", a.Shape, size, MaxArtifactBytes)
		}
	}
}

// An answer the size a real one is stays exactly as the model wrote it.
func TestByteBudgetLeavesAnOrdinaryAnswerAlone(t *testing.T) {
	in := Artifact{
		Shape:   ShapeTable,
		Title:   "Players",
		Columns: []string{"player", "force"},
		Rows:    [][]string{{"alice", "player"}, {"bob", "enemy"}},
	}
	got, err := validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(got.Rows) != 2 || got.Rows[1][0] != "bob" || got.Title != "Players" {
		t.Fatalf("an ordinary answer was shrunk: %+v", got)
	}
}
