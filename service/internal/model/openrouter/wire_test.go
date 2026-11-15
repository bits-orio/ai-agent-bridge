package openrouter

import (
	"encoding/json"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// The per-round cache breakpoint must sit on the last message of the
// request on every round. From round 2 the last user message holds only tool
// results, which go out as tool messages, so the last one carries the
// breakpoint as a content-parts array; a text block after the results
// becomes a user message and takes the breakpoint instead.
func TestToMessagesPlacesTheBreakpointOnTheLastMessage(t *testing.T) {
	call := func(id string) model.Block {
		return model.Block{Type: model.BlockToolUse, ID: id, Name: "t", Input: json.RawMessage(`{}`)}
	}
	res := func(id, c string) model.Block { return model.Block{Type: model.BlockToolResult, ID: id, Content: c} }
	txt := func(s string) model.Block { return model.Block{Type: model.BlockText, Text: s} }
	asst := model.Message{Role: model.RoleAssistant, Blocks: []model.Block{call("a"), call("b")}}
	q := model.Message{Role: model.RoleUser, Blocks: []model.Block{txt("question")}}

	cases := []struct {
		name string
		user model.Message
		want string // JSON of the messages after the assistant turn
	}{
		{"results only", model.Message{Role: model.RoleUser, Blocks: []model.Block{res("a", "one"), res("b", "two")}},
			`[{"role":"tool","content":"one","tool_call_id":"a"},` +
				`{"role":"tool","content":[{"type":"text","text":"two","cache_control":{"type":"ephemeral"}}],"tool_call_id":"b"}]`},
		{"results then text", model.Message{Role: model.RoleUser, Blocks: []model.Block{res("a", "one"), res("b", "two"), txt("one turn left")}},
			`[{"role":"tool","content":"one","tool_call_id":"a"},{"role":"tool","content":"two","tool_call_id":"b"},` +
				`{"role":"user","content":[{"type":"text","text":"one turn left","cache_control":{"type":"ephemeral"}}]}]`},
		{"text before results still ends on the text", model.Message{Role: model.RoleUser, Blocks: []model.Block{txt("note"), res("a", "one"), res("b", "two")}},
			`[{"role":"tool","content":"one","tool_call_id":"a"},{"role":"tool","content":"two","tool_call_id":"b"},` +
				`{"role":"user","content":[{"type":"text","text":"note","cache_control":{"type":"ephemeral"}}]}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := toMessages([]model.Message{q, asst, c.user})
			if len(out) < 2 {
				t.Fatalf("got %d messages", len(out))
			}
			got, _ := json.Marshal(out[2:])
			// Compare decoded, so key order in the fixture does not matter.
			var g, w any
			_ = json.Unmarshal(got, &g)
			if err := json.Unmarshal([]byte(c.want), &w); err != nil {
				t.Fatal(err)
			}
			gj, _ := json.Marshal(g)
			wj, _ := json.Marshal(w)
			if string(gj) != string(wj) {
				t.Errorf("messages =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}
