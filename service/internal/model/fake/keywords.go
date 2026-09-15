// The keyword rules that decide which tool the scripted model calls, and the
// small text helpers they need. Order matters: the first rule that matches
// wins, so a question mentioning two subjects always resolves the same way.

package fake

import (
	"encoding/json"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// resultChars is how much of a tool result the artifact carries.
const resultChars = 160

type call struct {
	tool string
	args map[string]any
}

// firstCall picks the tool for the first turn, or nil to answer at once.
// Tool names are matched loosely: the catalog mangles a provider function
// into "<iface>__<fn>", so a bare function name is enough to find it.
func firstCall(text string, defs []model.ToolDef) *call {
	switch {
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
