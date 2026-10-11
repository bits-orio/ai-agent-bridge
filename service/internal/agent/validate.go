// Validation and cutting of a submitted artifact. A shape the game cannot show
// is an error handed straight back to the model as a failed tool result, so
// every message is written to tell it what to send instead.
//
// Length is a different matter. validate never refuses an answer for being
// long: it cuts it to the caps with an ellipsis, between words wherever that
// keeps at least half the cap (clipRichText). Whether the cut copy goes out at
// all is the loop's call (agent.go): while the model has a round left, an
// answer over a cap is sent back to be rewritten, at most twice, with
// overCaps saying what is over (overcaps.go), and only after that does the
// cut copy reach the player.

package agent

import (
	"fmt"
	"strings"
)

// validate checks an artifact's shape and returns the copy that goes to the
// game: every cell on one line, every count and cell cut to its cap, and the
// whole inside the byte budget.
func validate(a Artifact) (Artifact, error) {
	out, err := clipToCaps(a)
	if err != nil {
		return Artifact{}, err
	}
	return fit(out), nil
}

// clipToCaps is validate without the byte budget: the shape checked, every
// cell flattened to one line, and every count and cell cut to its cap.
func clipToCaps(a Artifact) (Artifact, error) {
	out := Artifact{Shape: a.Shape, Title: cell(a.Title), ToAsker: a.ToAsker}
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
		for _, name := range out.Columns {
			if name == "" {
				return Artifact{}, fmt.Errorf("every table column needs a name; one of them is empty")
			}
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
		if out.Columns[0] == "" || out.Columns[1] == "" {
			return Artifact{}, fmt.Errorf("a comparison's two column names must not be empty: name the two things compared, force names for forces")
		}
		out.Pairs = pairs(a.Pairs)
		if len(out.Pairs) == 0 {
			return Artifact{}, fmt.Errorf("a comparison needs at least one row of {label, a, b}")
		}
	case ShapeArt:
		out.Lines = artLines(a.Lines, MaxArtLines)
		if len(out.Lines) == 0 {
			return Artifact{}, fmt.Errorf("art needs at least one line with something on it in lines")
		}
	default:
		return Artifact{}, fmt.Errorf("shape %q is not a shape, use summary, notice, list, table, comparison or art", a.Shape)
	}
	return out, nil
}

// tableRows cuts to the row cap and squares every row off against the
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

// cell is one string on one line, cut to the per-cell cap (clipRichText).
// Runes are counted, not bytes, so a cut never splits a character. The
// companion cuts again at 1280 bytes on a UTF-8 boundary, which is the same
// 320 runes' worth at four bytes a rune.
func cell(s string) string {
	return clipRichText(flat(s), MaxCellChars)
}

// artLines is art as the player sees it: an entry holding newlines split
// into its lines, a tab four spaces, other control characters dropped,
// trailing spaces and blank lines at either end gone, every line cut to
// MaxArtWidth characters and the count to max. Leading and inner spaces are
// the drawing, so they stay.
func artLines(in []string, max int) []string {
	var all []string
	for _, entry := range in {
		for _, l := range strings.Split(strings.ReplaceAll(entry, "\r\n", "\n"), "\n") {
			all = append(all, artLine(l))
		}
	}
	for len(all) > 0 && strings.TrimSpace(all[0]) == "" {
		all = all[1:]
	}
	for len(all) > 0 && strings.TrimSpace(all[len(all)-1]) == "" {
		all = all[:len(all)-1]
	}
	if len(all) > max {
		all = all[:max]
	}
	return all
}

// artLine cleans one art line and cuts it to MaxArtWidth characters.
func artLine(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, " ")
	if r := []rune(s); len(r) > MaxArtWidth {
		s = strings.TrimRight(string(r[:MaxArtWidth]), " ")
	}
	return s
}

// flat is a cell as the player reads it: every newline and run of spaces one
// space, so a row a model wrote across two lines stays one chat line.
func flat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
