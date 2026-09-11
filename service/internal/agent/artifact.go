// The typed answer shapes (ADR 0003). The model fills one of these and the
// companion renders it, so formatting never reaches the model and text a
// player typed can never change the layout of an answer.
//
// Sizes are enforced twice on purpose: here, before the artifact goes over
// RCON, and again in the companion when it renders. This end clips rather
// than refuses, except where a shape would be left with nothing to show.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The v1 shapes (PLAN.md, Artifacts).
const (
	ShapeSummary    = "summary"
	ShapeNotice     = "notice"
	ShapeList       = "list"
	ShapeTable      = "table"
	ShapeComparison = "comparison"
)

// Notice levels.
const (
	LevelWarning      = "warning"
	LevelConfirmation = "confirmation"
)

// Caps, one per shape (docs/design/phase1-2-spec.md).
const (
	MaxSummaryLines   = 3
	MaxCellChars      = 160
	MaxListItems      = 10
	MaxTableColumns   = 5
	MaxTableRows      = 8
	MaxComparisonRows = 5
)

// Pair is one row of a comparison: a label and the value in each column.
type Pair struct {
	Label string `json:"label"`
	A     string `json:"a"`
	B     string `json:"b"`
}

// Artifact is one answer. It is flat on the wire: shape picks the fields that
// matter and the companion ignores the rest. Rows are two different things
// depending on the shape, so they live in two fields here and share one JSON
// name.
type Artifact struct {
	Shape   string
	Title   string
	Lines   []string // summary
	Text    string   // notice
	Level   string   // notice
	Items   []string // list
	Columns []string // table, comparison
	Rows    [][]string
	Pairs   []Pair
	Session *SessionMark // set by the loop: which session answered, and whether it is new
	ToAsker bool         // print to the asker alone: a refusal is nobody else's business
}

// refusal is a warning notice the asker alone will see.
func refusal(text string) Artifact {
	a := Notice(LevelWarning, text)
	a.ToAsker = true
	return a
}

// Rendered is the artifact as the companion prints it, line for line: the
// title first, then the shape's own lines, a table as its column names and
// one row per line with " | " between cells. It is what a session keeps,
// so the model later reads exactly what the players read.
func (a Artifact) Rendered() []string {
	var out []string
	if a.Title != "" {
		out = append(out, a.Title)
	}
	switch a.Shape {
	case ShapeNotice:
		out = append(out, a.Text)
	case ShapeList:
		for _, item := range a.Items {
			out = append(out, "- "+item)
		}
	case ShapeTable:
		out = append(out, strings.Join(a.Columns, " | "))
		for _, row := range a.Rows {
			out = append(out, strings.Join(row, " | "))
		}
	case ShapeComparison:
		for _, p := range a.Pairs {
			out = append(out, fmt.Sprintf("- %s: %s vs %s", p.Label, p.A, p.B))
		}
	default:
		out = append(out, a.Lines...)
	}
	return out
}

// Plain is Rendered joined with newlines.
func (a Artifact) Plain() string { return strings.Join(a.Rendered(), "\n") }

// Summary builds the plainest artifact there is, used whenever the loop has
// to answer for itself.
func Summary(lines ...string) Artifact {
	return Artifact{Shape: ShapeSummary, Lines: lines}
}

// Notice is the shape the loop returns when it has something to say about
// itself rather than about the game: over quota, out of rounds, out of
// tokens.
func Notice(level, text string) Artifact {
	return Artifact{Shape: ShapeNotice, Level: level, Text: text}
}

// Line is the one-line form of an artifact, used for the log and for what the
// next question remembers about this one.
func (a Artifact) Line() string {
	switch a.Shape {
	case ShapeNotice:
		return a.Text
	case ShapeList:
		if len(a.Items) > 0 {
			return a.Items[0]
		}
	case ShapeTable:
		if len(a.Rows) > 0 {
			return strings.Join(a.Rows[0], " ")
		}
	case ShapeComparison:
		if len(a.Pairs) > 0 {
			p := a.Pairs[0]
			return fmt.Sprintf("%s: %s vs %s", p.Label, p.A, p.B)
		}
	default:
		if len(a.Lines) > 0 {
			return a.Lines[0]
		}
	}
	return a.Title
}

func (a Artifact) MarshalJSON() ([]byte, error) {
	out := map[string]any{"shape": a.Shape}
	if a.Title != "" {
		out["title"] = a.Title
	}
	switch a.Shape {
	case ShapeNotice:
		out["text"] = a.Text
		if a.Level != "" {
			out["level"] = a.Level
		}
	case ShapeList:
		out["items"] = orEmpty(a.Items)
	case ShapeTable:
		out["columns"] = orEmpty(a.Columns)
		rows := a.Rows
		if rows == nil {
			rows = [][]string{}
		}
		out["rows"] = rows
	case ShapeComparison:
		out["columns"] = orEmpty(a.Columns)
		pairs := a.Pairs
		if pairs == nil {
			pairs = []Pair{}
		}
		out["rows"] = pairs
	default:
		out["lines"] = orEmpty(a.Lines)
	}
	if a.Session != nil {
		out["session"] = a.Session
	}
	if a.ToAsker {
		out["to_asker"] = true
	}
	return json.Marshal(out)
}

func (a *Artifact) UnmarshalJSON(b []byte) error {
	var raw struct {
		Shape   string          `json:"shape"`
		Title   string          `json:"title"`
		Lines   []string        `json:"lines"`
		Text    string          `json:"text"`
		Level   string          `json:"level"`
		Items   []string        `json:"items"`
		Columns []string        `json:"columns"`
		Rows    json.RawMessage `json:"rows"`
		Session *SessionMark    `json:"session"`
		ToAsker bool            `json:"to_asker"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*a = Artifact{
		Shape: raw.Shape, Title: raw.Title, Lines: raw.Lines,
		Text: raw.Text, Level: raw.Level, Items: raw.Items, Columns: raw.Columns,
		Session: raw.Session, ToAsker: raw.ToAsker,
	}
	if len(raw.Rows) == 0 {
		return nil
	}
	if raw.Shape == ShapeComparison {
		return json.Unmarshal(raw.Rows, &a.Pairs)
	}
	return json.Unmarshal(raw.Rows, &a.Rows)
}

// orEmpty keeps an empty slice out of the JSON as [] rather than null, which
// a Lua decoder handles far more gracefully.
func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
