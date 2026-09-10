// submit_answer: the one tool that ends the loop. It is not a tools.Tool
// because it never calls anything, it hands the loop the artifact and stops.

package agent

import (
	"fmt"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// SubmitTool is the name the model calls to answer.
const SubmitTool = "submit_answer"

const submitDescription = "Send the answer to the player and end your turn. Call this exactly once, as your last action. " +
	"Pick the shape that fits the answer: summary for prose, notice for a single warning or confirmation, " +
	"list for one line per thing, table for rows of values, comparison for two forces or two items side by side. " +
	"Fill only the fields that shape uses. The game renders it, so send plain values and no formatting of your own."

// submitDef is the tool definition the model sees. Rows carries two different
// shapes, so it is described rather than typed: an array of arrays of strings
// for a table, an array of {label, a, b} objects for a comparison.
func submitDef() model.ToolDef {
	props := map[string]any{
		"shape": map[string]any{
			"type":        "string",
			"enum":        []string{ShapeSummary, ShapeNotice, ShapeList, ShapeTable, ShapeComparison},
			"description": "Which answer shape to render.",
		},
		"title": map[string]any{
			"type":        "string",
			"description": "Optional heading for summary, list, table and comparison.",
		},
		"lines": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": fmt.Sprintf("summary: up to %d lines of at most %d characters.", MaxSummaryLines, MaxCellChars),
		},
		"text": map[string]any{
			"type":        "string",
			"description": "notice: the single line to show.",
		},
		"level": map[string]any{
			"type":        "string",
			"enum":        []string{LevelWarning, LevelConfirmation},
			"description": "notice: what kind of notice this is.",
		},
		"items": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": fmt.Sprintf("list: up to %d one-line entries.", MaxListItems),
		},
		"columns": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": fmt.Sprintf("table: up to %d column names. comparison: exactly two names.", MaxTableColumns),
		},
		"rows": map[string]any{
			"type": "array",
			"description": fmt.Sprintf(
				"table: up to %d rows, each an array of strings, one per column. comparison: up to %d objects with label, a and b.",
				MaxTableRows, MaxComparisonRows),
		},
	}
	return model.ToolDef{
		Name:        SubmitTool,
		Description: submitDescription,
		Schema:      tools.ObjectSchema(props, "shape"),
	}
}

// defsFor is the full tool list one question runs with: everything the
// catalog and history offer, plus submit_answer.
func defsFor(ts []tools.Tool) []model.ToolDef {
	defs := make([]model.ToolDef, 0, len(ts)+1)
	for _, t := range ts {
		defs = append(defs, model.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return append(defs, submitDef())
}
