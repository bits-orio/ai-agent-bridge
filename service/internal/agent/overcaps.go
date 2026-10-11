// What in a submitted answer is over a cap, in words the model can act on.
//
// The service used to cut an over-long cell at its cap without a word to the
// model, so the player read an answer that stopped mid-sentence (questions 37
// and 38 on the live server, 2026-10-11). Now an answer over a cap goes back
// to the model with this list while it still has a round to rewrite in, at
// most twice (agent.go, runCalls), and is cut after that.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxListedOver bounds how many problems one refusal names. A table of forty
// long cells would otherwise come back as a wall of text the model pays for
// on every later round; it fixes what it is told and hears about the rest
// next time.
const maxListedOver = 6

// overCaps lists every way an artifact is over a cap, each as one sentence:
// what is over, by how much, and what to do instead. A cell is measured as
// the player would read it, on one line. Empty means the artifact is within
// every cap. Call it on an artifact validate accepts: a shape validate
// refuses has nothing here to measure.
func overCaps(a Artifact) []string {
	var over []string
	note := func(format string, args ...any) {
		over = append(over, fmt.Sprintf(format, args...))
	}
	if n := longCell(a.Title); n > 0 {
		note("The title is %d characters, the cap is %d: shorten it.", n, MaxCellChars)
	}
	switch a.Shape {
	case ShapeSummary:
		if len(a.Lines) > MaxSummaryLines {
			note("This summary has %d lines, the cap is %d: keep the %d that matter, or send a list, which holds %d.",
				len(a.Lines), MaxSummaryLines, MaxSummaryLines, MaxListItems)
		}
		for i, line := range a.Lines {
			if n := longCell(line); n > 0 {
				note("Line %d is %d characters, the cap is %d: rewrite it shorter or use another line, at most %d lines.",
					i+1, n, MaxCellChars, MaxSummaryLines)
			}
		}
	case ShapeNotice:
		if n := longCell(a.Text); n > 0 {
			note("The notice is %d characters, the cap is %d: rewrite it shorter.", n, MaxCellChars)
		}
	case ShapeList:
		if len(a.Items) > MaxListItems {
			note("This list has %d items, the cap is %d: keep the %d that matter most.", len(a.Items), MaxListItems, MaxListItems)
		}
		for i, item := range a.Items {
			if n := longCell(item); n > 0 {
				note("Item %d is %d characters, the cap is %d: rewrite it shorter or split it into another item, at most %d items.",
					i+1, n, MaxCellChars, MaxListItems)
			}
		}
	case ShapeTable:
		over = append(over, tableOver(a)...)
	case ShapeComparison:
		over = append(over, comparisonOver(a)...)
	}
	if sized, err := clipToCaps(a); err == nil && !within(sized) {
		if len(over) > 0 {
			note("Even cut to those caps it would be %d bytes, the cap is %d: shorten the longest cells or send fewer rows.",
				marshalledBytes(sized), MaxArtifactBytes)
		} else {
			note("The whole answer is %d bytes, the cap is %d: shorten the longest cells or send fewer rows.",
				marshalledBytes(sized), MaxArtifactBytes)
		}
	}
	return over
}

// tableOver is overCaps for a table: the column and row counts, then every
// column name and every cell that sits under a column.
func tableOver(a Artifact) []string {
	var over []string
	if len(a.Columns) > MaxTableColumns {
		over = append(over, fmt.Sprintf("This table has %d columns, the cap is %d: drop or merge columns.",
			len(a.Columns), MaxTableColumns))
	}
	over = append(over, columnNamesOver(a.Columns)...)
	if len(a.Rows) > MaxTableRows {
		over = append(over, fmt.Sprintf("This table has %d rows, the cap is %d: keep the %d that matter most and say how many you left out.",
			len(a.Rows), MaxTableRows, MaxTableRows))
	}
	for i, row := range a.Rows {
		for j, c := range row {
			if j >= len(a.Columns) {
				break
			}
			if n := longCell(c); n > 0 {
				over = append(over, fmt.Sprintf("Row %d, column %d is %d characters, the cap is %d: rewrite it shorter.",
					i+1, j+1, n, MaxCellChars))
			}
		}
	}
	return over
}

// comparisonOver is overCaps for a comparison: a third column, the row count,
// the column names and each row's label, a and b.
func comparisonOver(a Artifact) []string {
	var over []string
	if len(a.Columns) > 2 {
		over = append(over, fmt.Sprintf("A comparison sets exactly two things side by side and this one names %d: send a table to show more.",
			len(a.Columns)))
	}
	over = append(over, columnNamesOver(a.Columns)...)
	if len(a.Pairs) > MaxComparisonRows {
		over = append(over, fmt.Sprintf("This comparison has %d rows, the cap is %d: keep the %d that matter most.",
			len(a.Pairs), MaxComparisonRows, MaxComparisonRows))
	}
	for i, p := range a.Pairs {
		for _, field := range []struct{ name, text string }{{"label", p.Label}, {"a", p.A}, {"b", p.B}} {
			if n := longCell(field.text); n > 0 {
				over = append(over, fmt.Sprintf("Row %d's %s is %d characters, the cap is %d: rewrite it shorter.",
					i+1, field.name, n, MaxCellChars))
			}
		}
	}
	return over
}

func columnNamesOver(columns []string) []string {
	var over []string
	for j, name := range columns {
		if n := longCell(name); n > 0 {
			over = append(over, fmt.Sprintf("Column %d's name is %d characters, the cap is %d: shorten it.", j+1, n, MaxCellChars))
		}
	}
	return over
}

// longCell is how many characters s comes to on one line when that is over
// the per-cell cap, and zero when it is not.
func longCell(s string) int {
	if n := utf8.RuneCountInString(flat(s)); n > MaxCellChars {
		return n
	}
	return 0
}

func marshalledBytes(a Artifact) int {
	out, err := json.Marshal(a)
	if err != nil {
		return 0
	}
	return len(out)
}

// listOver joins what is over the caps into one line for the model or the
// log, naming at most maxListedOver of them.
func listOver(over []string) string {
	shown := over
	if len(shown) > maxListedOver {
		shown = shown[:maxListedOver]
	}
	text := strings.Join(shown, " ")
	if more := len(over) - len(shown); more > 0 {
		text += fmt.Sprintf(" And %d more like these.", more)
	}
	return text
}

// overCapsRefusal is what the model reads back when its answer is over a cap
// and it has a round left to rewrite it in: nothing was sent, what is over,
// and to send it again.
func overCapsRefusal(over []string) string {
	return "Not sent, nothing reached the player: the answer is over the caps. " + listOver(over) +
		" Rewrite it within the caps and call " + SubmitTool + " again."
}
