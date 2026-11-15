// What prints as something. The companion refuses an artifact whose title
// and every line, item or cell would print as nothing (artifact_check.lua,
// says_nothing and visible), so the service applies the same rule before it
// accepts a submission: a refusal here comes while the model still has a
// round to fix it, and one from the companion comes after every round was
// paid for and ends in a generic notice.

package agent

import (
	"regexp"
	"strings"
	"unicode"
)

// emptyTag is a tag naming nothing, [img=] or [item= ], which the renderer
// closes up.
var emptyTag = regexp.MustCompile(`^[A-Za-z0-9-]+=\s*$`)

// drawsNothing is the companion's draws_nothing: a colour or font span's
// ends, or a tag naming nothing.
func drawsNothing(tag string) bool {
	return strings.HasPrefix(tag, "color=") || strings.HasPrefix(tag, "font=") ||
		tag == "/color" || tag == "/font" || emptyTag.MatchString(tag)
}

// visible reports whether s shows the reader anything once spaces, control
// characters and the tags that draw nothing are gone. Go's notion of a space
// is wider than Lua's %s (it includes a no-break space), which only ever
// makes this stricter than the companion, never looser.
func visible(s string) bool {
	// richTag (voice.go) is the same pattern the companion's visible()
	// strips with: a bracket, anything but a bracket, a bracket.
	bare := richTag.ReplaceAllStringFunc(s, func(m string) string {
		if drawsNothing(m[1 : len(m)-1]) {
			return ""
		}
		return m
	})
	for _, r := range bare {
		if !unicode.IsSpace(r) && !unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// shown keeps the entries that print something, in order.
func shown(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if visible(s) {
			out = append(out, s)
		}
	}
	return out
}

// dropBlank is the artifact without the entries that would print as
// nothing: summary lines, list items, table rows whose every cell is blank,
// comparison rows whose label and both values are. Blank cells inside a row
// that says something stay, since they hold a column's place. Art keeps its
// blank lines, which are part of the drawing. Dropping happens before any
// count or cap is checked, so a blank entry never takes a line a real one
// needed and never makes an empty answer look full.
func dropBlank(a Artifact) Artifact {
	switch a.Shape {
	case ShapeSummary:
		a.Lines = shown(a.Lines)
	case ShapeList:
		a.Items = shown(a.Items)
	case ShapeTable:
		width := min(len(a.Columns), MaxTableColumns)
		rows := make([][]string, 0, len(a.Rows))
		for _, row := range a.Rows {
			// Only the cells under a column print, as the companion reads it.
			if len(shown(row[:min(len(row), width)])) > 0 {
				rows = append(rows, row)
			}
		}
		a.Rows = rows
	case ShapeComparison:
		pairs := make([]Pair, 0, len(a.Pairs))
		for _, p := range a.Pairs {
			if visible(p.Label) || visible(p.A) || visible(p.B) {
				pairs = append(pairs, p)
			}
		}
		a.Pairs = pairs
	}
	return a
}
