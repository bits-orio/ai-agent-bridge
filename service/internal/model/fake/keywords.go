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
	case has(text, "research"):
		return resolve(defs, "current_research", nil)
	case has(text, "rate"):
		return resolve(defs, "item_rate", map[string]any{
			"surface": "nauvis",
			"item":    itemOf(text),
			"window":  "one_minute",
		})
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

// itemOf reads the item name out of "rate of iron-plate" or "rate for
// copper-plate", skipping an article on the way.
func itemOf(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	for i, word := range fields {
		if word != "of" && word != "for" {
			continue
		}
		for _, candidate := range fields[i+1:] {
			candidate = strings.Trim(candidate, ".,?!\"'")
			switch candidate {
			case "", "the", "a", "an", "my", "our", "your":
				continue
			}
			return candidate
		}
	}
	return "iron-plate"
}

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
