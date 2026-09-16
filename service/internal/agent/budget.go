// The whole-artifact byte budget. The shape caps bound how many rows an answer
// has and how long one cell is; they say nothing about what the two together
// add up to, and a table at its caps marshals to several thousand bytes.
//
// The transport is not the reason for the budget. The rcon client accepts
// 262144 bytes per command, measured on the rig, so a maxed artifact would
// reach the game. The reason is that an answer a player reads in chat is small
// by nature: a few hundred bytes. Anything that needs thousands is a different
// answer, not a bigger one.

package agent

import "encoding/json"

// MaxArtifactBytes is the marshalled size an artifact has to fit in.
const MaxArtifactBytes = 6000

// minCellRunes is as short as shortening a cell ever gets. A cell this short
// carries nothing, so an artifact still over budget here is left over budget
// rather than reduced to punctuation; validate's shape rules guarantee that
// cannot happen to a real answer.
const minCellRunes = 8

// fit shrinks an artifact that has already passed the shape caps until it
// marshals inside the budget. Trailing rows and items go first, one at a time,
// because the rows a model writes first are the ones it thought mattered. Only
// when a single row is still too big do the cells get shorter.
func fit(a Artifact) Artifact {
	if within(a) {
		return a
	}
	for droppable(a) {
		a = dropLast(a)
		if within(a) {
			return a
		}
	}
	for limit := MaxCellChars; limit > minCellRunes; {
		limit = limit * 3 / 4
		a = shorten(a, limit)
		if within(a) {
			return a
		}
	}
	return a
}

func within(a Artifact) bool {
	out, err := json.Marshal(a)
	return err == nil && len(out) <= MaxArtifactBytes
}

// droppable reports whether the shape still has a row to spare. The last one
// always stays: an empty list is not an answer, and validate already refused
// one.
func droppable(a Artifact) bool {
	switch a.Shape {
	case ShapeSummary:
		return len(a.Lines) > 1
	case ShapeList:
		return len(a.Items) > 1
	case ShapeTable:
		return len(a.Rows) > 1
	case ShapeComparison:
		return len(a.Pairs) > 1
	}
	return false
}

func dropLast(a Artifact) Artifact {
	switch a.Shape {
	case ShapeSummary:
		a.Lines = a.Lines[:len(a.Lines)-1]
	case ShapeList:
		a.Items = a.Items[:len(a.Items)-1]
	case ShapeTable:
		a.Rows = a.Rows[:len(a.Rows)-1]
	case ShapeComparison:
		a.Pairs = a.Pairs[:len(a.Pairs)-1]
	}
	return a
}

// shorten clips every cell in the artifact to limit runes, the title and the
// column names included.
func shorten(a Artifact, limit int) Artifact {
	a.Title = clipRichText(a.Title, limit)
	a.Text = clipRichText(a.Text, limit)
	a.Lines = shortenAll(a.Lines, limit)
	a.Items = shortenAll(a.Items, limit)
	a.Columns = shortenAll(a.Columns, limit)

	rows := make([][]string, 0, len(a.Rows))
	for _, row := range a.Rows {
		rows = append(rows, shortenAll(row, limit))
	}
	a.Rows = rows

	pairs := make([]Pair, 0, len(a.Pairs))
	for _, p := range a.Pairs {
		pairs = append(pairs, Pair{
			Label: clipRichText(p.Label, limit),
			A:     clipRichText(p.A, limit),
			B:     clipRichText(p.B, limit),
		})
	}
	a.Pairs = pairs
	return a
}

func shortenAll(in []string, limit int) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, clipRichText(s, limit))
	}
	return out
}

// clipRunes trims a string to limit runes, counted in runes so a clip never
// splits a character in half.
func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit])
	}
	return s
}
