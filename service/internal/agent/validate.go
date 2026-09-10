// Validation and clipping of a submitted artifact. An error here is handed
// straight back to the model as a failed tool result, so every message is
// written to tell it what to send instead.

package agent

import (
	"fmt"
	"strings"
)

// validate clips an artifact to the caps and returns the clipped copy, or an
// error the model can act on.
func validate(a Artifact) (Artifact, error) {
	out := Artifact{Shape: a.Shape, Title: cell(a.Title)}
	switch a.Shape {
	case ShapeSummary:
		out.Lines = cells(a.Lines, MaxSummaryLines)
		if len(out.Lines) == 0 {
			return Artifact{}, fmt.Errorf("a summary needs at least one entry in lines")
		}
	case ShapeNotice:
		out.Text = cell(a.Text)
		out.Level = a.Level
		if out.Text == "" {
			return Artifact{}, fmt.Errorf("a notice needs text")
		}
		if out.Level != "" && out.Level != LevelWarning && out.Level != LevelConfirmation {
			return Artifact{}, fmt.Errorf("level %q is not a level, use warning or confirmation or leave it out", a.Level)
		}
	case ShapeList:
		out.Items = cells(a.Items, MaxListItems)
		if len(out.Items) == 0 {
			return Artifact{}, fmt.Errorf("a list needs at least one entry in items")
		}
	case ShapeTable:
		out.Columns = cells(a.Columns, MaxTableColumns)
		if len(out.Columns) == 0 {
			return Artifact{}, fmt.Errorf("a table needs at least one column name in columns")
		}
		out.Rows = tableRows(a.Rows, len(out.Columns))
		if len(out.Rows) == 0 {
			return Artifact{}, fmt.Errorf("a table needs at least one row, each row an array of strings")
		}
	case ShapeComparison:
		out.Columns = cells(a.Columns, 2)
		if len(out.Columns) != 2 {
			return Artifact{}, fmt.Errorf("a comparison needs exactly two column names in columns")
		}
		out.Pairs = pairs(a.Pairs)
		if len(out.Pairs) == 0 {
			return Artifact{}, fmt.Errorf("a comparison needs at least one row of {label, a, b}")
		}
	default:
		return Artifact{}, fmt.Errorf("shape %q is not a shape, use summary, notice, list, table or comparison", a.Shape)
	}
	return out, nil
}

// tableRows clips to the row cap and squares every row off against the
// columns, padding short rows so the companion never indexes past the end.
func tableRows(rows [][]string, columns int) [][]string {
	if len(rows) > MaxTableRows {
		rows = rows[:MaxTableRows]
	}
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		square := make([]string, columns)
		for i := 0; i < columns; i++ {
			if i < len(row) {
				square[i] = cell(row[i])
			}
		}
		out = append(out, square)
	}
	return out
}

func pairs(in []Pair) []Pair {
	if len(in) > MaxComparisonRows {
		in = in[:MaxComparisonRows]
	}
	out := make([]Pair, 0, len(in))
	for _, p := range in {
		out = append(out, Pair{Label: cell(p.Label), A: cell(p.A), B: cell(p.B)})
	}
	return out
}

func cells(in []string, max int) []string {
	if len(in) > max {
		in = in[:max]
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, cell(s))
	}
	return out
}

// cell trims one string to the per-cell cap, counted in runes so a clip never
// splits a character, and flattens any newline a model wrote into a space.
func cell(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	r := []rune(s)
	if len(r) > MaxCellChars {
		return string(r[:MaxCellChars])
	}
	return s
}
