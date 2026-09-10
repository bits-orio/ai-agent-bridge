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

// A table at every shape cap marshals to well over 6000 bytes. Trailing rows
// go until it fits, and the cells that stay are untouched: the rows a model
// wrote first are the ones it thought mattered.
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
// per-cell cap, and nothing is cut mid-character.
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
		if !strings.HasPrefix(wide, cell) {
			t.Fatal("a cell was cut mid-character")
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
