// The keyword rules that decide which tool the scripted model calls, and the
// small text helpers they need. Order matters: the first rule that matches
// wins, so a question mentioning two subjects always resolves the same way.

package fake

import (
	"encoding/json"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// resultChars is how many runes of a tool result the artifact carries, and of
// a question it echoes. It was 160, which left an end-to-end answer no longer
// than two chat lines; 300 lets it run on the way a real answer does. It stays
// under the service's 320-character cell (agent.MaxCellChars) with room for
// the few words the echo and the recall put in front of the cut text. A line
// over the cell goes back to the model to be rewritten, and the fake can only
// send the same line again.
const resultChars = 300

type call struct {
	tool string
	args map[string]any
}

// firstCall picks the tool for the first turn, or nil to answer at once.
// Tool names are matched loosely: the catalog mangles a provider function
// into "<iface>__<fn>", so a bare function name is enough to find it.
func firstCall(text string, defs []model.ToolDef) *call {
	switch {
	// What it takes to make something (docs/design/recipe-needs-spec.md,
	// part 1). First, so no broader word in the same question shadows it.
	// The arguments are fixed, 60 red science a minute, so the end-to-end
	// harness can check the figures the game works out for itself.
	case has(text, "recipe needs"):
		return resolve(defs, "recipe_needs", map[string]any{
			"items":   []string{"automation-science-pack"},
			"per_min": []float64{60},
		})
	// Pirion's question (recipe-needs-spec.md, stage 4): "I have 48 blue
	// science, 24 red, 24 green", read as machine counts and asked in one
	// call, as the tool's desc teaches a real model to, on the surface the
	// asker stands on. Second, beside the other recipe_needs rule, so "how
	// many" or "rate" in the same question cannot shadow it. The answer
	// quotes the plate rows (recipe.go).
	case has(text, scienceBelts):
		return resolve(defs, "recipe_needs", map[string]any{
			"items":    []string{"chemical-science-pack", "automation-science-pack", "logistic-science-pack"},
			"machines": []float64{48, 24, 24},
			"surface":  "nauvis",
		})
	// Chat history, for the harness's privacy scenarios: which lines a
	// question may read depends on the scope it was asked in, and the
	// answer carries what recent_chat returned. Second, so "what did the
	// players say in chat" or "where was that chat" still reads the chat.
	// No question any other scenario asks says chat.
	case has(text, "chat"):
		return resolve(defs, "recent_chat", nil)
	case has(text, "where"):
		return resolve(defs, "find_entities", map[string]any{"surface": "nauvis", "name": "lab"})
	case has(text, "queue"):
		return resolve(defs, "research_queue", nil)
	case has(text, "research"):
		return resolve(defs, "current_research", nil)
	case has(text, "rate"):
		return resolve(defs, "item_rate", map[string]any{
			"surface": "nauvis",
			"item":    itemOf(text),
			"window":  "one_minute",
		})
	// A metric another mod declares (docs/design/phase5-sweep.md,
	// "Provider-declared metrics"): the harness's test provider exposes
	// standings with a sweep block, and the companion's sweep must find it
	// without the companion, or this model, ever naming that mod.
	case has(text, "standings"):
		return resolve(defs, "sweep", map[string]any{"metric": "standings"})
	// Stage 5's own sweep metrics (docs/design/phase5-sweep.md staging
	// table, stage 5): kills delegates to a tool that stage adds
	// (companion-mod/scripts/tools/kills.lua), item_made to the existing
	// production_since. Proven end to end in tests/e2e/scenarios.py.
	case has(text, "kills"):
		return resolve(defs, "sweep", map[string]any{"metric": "kills"})
	case has(text, "forces"), has(text, "teams"):
		return resolve(defs, "list_forces", nil)
	case has(text, "players"):
		return resolve(defs, "list_players", nil)
	// "die" also covers "died" and "dies", which is what a player types.
	case has(text, "die"), has(text, "death"):
		return resolve(defs, "last_event", map[string]any{"event": "player_died"})
	case has(text, "hello"):
		if c := resolve(defs, "hello", map[string]any{"name": "rig"}); c != nil {
			return c
		}
		return resolve(defs, "list_forces", nil)
	case has(text, "surfaces"):
		return resolve(defs, "list_surfaces", nil)
	case has(text, "technology"), has(text, "tech "):
		return resolve(defs, "tech_status", map[string]any{"tech": techOf(text)})
	case has(text, "logistic"), has(text, "bots"):
		return resolve(defs, "logistics_summary", map[string]any{"surface": "nauvis"})
	case has(text, "how many"):
		return resolve(defs, "entity_count", map[string]any{
			"surface": "nauvis",
			"name":    entityNameOf(text),
		})
	case has(text, "evolution"):
		return resolve(defs, "evolution", map[string]any{"surface": "nauvis"})
	case has(text, "rocket"):
		return resolve(defs, "rockets", nil)
	case has(text, "time"), has(text, "how long"):
		return resolve(defs, "game_time", nil)
	case has(text, "pollution"):
		return resolve(defs, "pollution", map[string]any{"surface": "nauvis"})
	// "which item have we made the most of" ranks ITEMS within one force,
	// which is top_items, and it would otherwise fall into the each-team rule
	// below on the word "which".
	case has(text, "which item"), has(text, "what item"):
		return resolve(defs, "top_items", map[string]any{"surface": "nauvis", "window": "one_minute"})
	// "made" alone is a common past participle that entity_count ("how many
	// turrets have we made") answers, and that rule sits above this one so it
	// wins. "iron plate made since the start" reaches production_since below
	// because it has no "each" or "which", not because of position: this
	// rule wants the shape of an each-team question as well as the word.
	case has(text, "made") && (has(text, "each") || has(text, "which")):
		return resolve(defs, "sweep", map[string]any{"metric": "item_made", "subject": itemOf(text)})
	case has(text, "since"):
		return resolve(defs, "production_since", map[string]any{
			"surface":    "nauvis",
			"item":       itemOf(text),
			"since_tick": 0,
		})
	default:
		return nil
	}
}

// resolve finds the catalog name for a bare function name. A tool the server
// does not expose answers nil, and the model submits an answer instead of
// calling something that is not there.
func resolve(defs []model.ToolDef, fn string, args map[string]any) *call {
	if args == nil {
		args = map[string]any{}
	}
	for _, d := range defs {
		if d.Name == fn {
			return &call{tool: d.Name, args: args}
		}
	}
	for _, d := range defs {
		if strings.HasSuffix(d.Name, "__"+fn) {
			return &call{tool: d.Name, args: args}
		}
	}
	return nil
}

// itemOf, techOf and entityNameOf, which read a tool argument out of the
// question text, live in params.go.

func has(text, word string) bool {
	return strings.Contains(strings.ToLower(text), word)
}

// compact squeezes a tool result onto one line so it fits in an artifact cell.
func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > resultChars {
		return string(r[:resultChars])
	}
	return s
}

func mustJSON(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
