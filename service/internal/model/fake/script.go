// Scripted turns for the end-to-end harness's loop scenarios: questions whose
// point is how the agent loop handles what the model does, not which tool it
// calls. Each keyword fixes the model's behaviour turn by turn, read back out
// of the conversation like everything else here, so a run is the same every
// time.
//
//	loop repeat   calls list_forces on turns 1 and 2 with the same arguments,
//	              then submits what the last result said (the repeat note)
//	loop salvage  submits a summary whose prose is in text and has no lines
//	loop empty    submits a summary with nothing in it, every turn
//	unknown kind  asks count_events for an event kind no map records

package fake

import "github.com/bits-orio/ai-agent-bridge/service/internal/model"

const (
	loopRepeat  = "loop repeat"
	loopSalvage = "loop salvage"
	loopEmpty   = "loop empty"
	unknownKind = "unknown kind"

	// SalvageText is the prose the loop salvage question submits in the text
	// field, so a harness can look for it in the answer the player gets.
	SalvageText = "Salvaged by the loop from the text field."

	// UnknownKindEvent is the event kind the unknown kind question asks
	// count_events for.
	UnknownKindEvent = "restart"
)

// scripted is the model's turn for a loop keyword, or nil when the question
// carries none. turn counts from 1.
func scripted(text string, turn int, defs []model.ToolDef, msgs []model.Message) *call {
	switch {
	case has(text, loopRepeat):
		if turn <= 2 {
			return resolve(defs, "list_forces", nil)
		}
		return &call{tool: "submit_answer", args: artifactOf(text, lastToolResult(msgs))}
	case has(text, loopSalvage):
		return &call{tool: "submit_answer", args: map[string]any{"shape": "summary", "text": SalvageText}}
	case has(text, loopEmpty):
		return &call{tool: "submit_answer", args: map[string]any{"shape": "summary"}}
	case has(text, unknownKind):
		if turn == 1 {
			return resolve(defs, "count_events", map[string]any{"event": UnknownKindEvent})
		}
		return &call{tool: "submit_answer", args: artifactOf(text, lastToolResult(msgs))}
	}
	return nil
}
