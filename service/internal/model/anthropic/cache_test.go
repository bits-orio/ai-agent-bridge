package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

const breakpoint = `"cache_control":{"type":"ephemeral"}`

// The breakpoint sits on the last block of the last user message and
// nowhere else: on round one that is the question text, on later rounds the
// last tool result. Every earlier block stays unmarked, so the request never
// carries more than the two breakpoints Step adds.
func TestCacheBreakpointMarksOnlyTheLastUserBlock(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: what forces?"}}},
		{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "t1", Name: "list_forces", Input: json.RawMessage(`{}`)}}},
		{Role: model.RoleUser, Blocks: []model.Block{
			{Type: model.BlockToolResult, ID: "t1", Content: `{"forces":[]}`},
			{Type: model.BlockToolResult, ID: "t2", Content: `{"more":true}`},
		}},
	}
	params := cacheLastUserBlock(toMessageParams(msgs))
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(encoded), breakpoint); n != 1 {
		t.Fatalf("want exactly one breakpoint in the messages, got %d: %s", n, encoded)
	}
	last := params[2].Content[1].OfToolResult
	if last == nil || last.CacheControl.Type != "ephemeral" {
		t.Errorf("the last tool result carries no breakpoint: %+v", params[2].Content[1])
	}
	if first := params[2].Content[0].OfToolResult; first.CacheControl.Type != "" {
		t.Errorf("the first tool result must stay unmarked, got %+v", first.CacheControl)
	}
}

// Round one has only the question; the breakpoint goes on that text block.
func TestCacheBreakpointOnTheQuestionText(t *testing.T) {
	msgs := []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: x"}}}}
	params := cacheLastUserBlock(toMessageParams(msgs))
	if txt := params[0].Content[0].OfText; txt == nil || txt.CacheControl.Type != "ephemeral" {
		t.Errorf("the question text carries no breakpoint: %+v", params[0].Content[0])
	}
}

// A conversation with no user message is left alone rather than crashed on.
func TestCacheBreakpointSurvivesNoUserMessage(t *testing.T) {
	if got := cacheLastUserBlock(nil); len(got) != 0 {
		t.Errorf("nil in, %d messages out", len(got))
	}
	only := []sdk.MessageParam{sdk.NewAssistantMessage(sdk.NewTextBlock("hi"))}
	if got := cacheLastUserBlock(only); len(got) != 1 {
		t.Errorf("assistant-only conversation changed shape: %d", len(got))
	}
}

// The system prompt is the other breakpoint. Tools are sent ahead of the
// system prompt in the cached prefix, so marking the system text caches the
// tool definitions with it, and the tools themselves stay unmarked.
func TestSystemPromptCarriesTheBreakpoint(t *testing.T) {
	sys := []sdk.TextBlockParam{{Text: "rules", CacheControl: sdk.NewCacheControlEphemeralParam()}}
	encoded, err := json.Marshal(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), breakpoint) {
		t.Errorf("system block encodes without a breakpoint: %s", encoded)
	}
	tools, err := json.Marshal(toToolParams([]model.ToolDef{{Name: "a", Description: "d", Schema: map[string]any{"type": "object"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tools), breakpoint) {
		t.Errorf("tool params must carry no breakpoint of their own: %s", tools)
	}
}

// The output cap reaches the request, and an unset cap falls back to the
// default rather than to zero, which the API would refuse.
func TestOutputCapDefaultsWhenUnset(t *testing.T) {
	if c := New("k", Options{Model: "m"}); c.opts.MaxOutput != DefaultMaxOutputTokens {
		t.Errorf("MaxOutput = %d, want %d", c.opts.MaxOutput, DefaultMaxOutputTokens)
	}
	if c := New("k", Options{Model: "m", MaxOutput: 1500}); c.opts.MaxOutput != 1500 {
		t.Errorf("MaxOutput = %d, want 1500", c.opts.MaxOutput)
	}
}

func requestJSON(t *testing.T, opts Options) string {
	t.Helper()
	c := New("k", opts)
	msgs := []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: x"}}}}
	defs := []model.ToolDef{{Name: "a", Description: "d", Schema: map[string]any{"type": "object"}}}
	encoded, err := json.Marshal(c.params("rules", msgs, defs))
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Thinking is sent as disabled by default, adaptive on request, and not at
// all when the operator leaves it to the model. Effort is only ever sent
// when set.
func TestThinkingAndEffortFollowTheOptions(t *testing.T) {
	off := requestJSON(t, Options{Model: "m", Thinking: "off"})
	if !strings.Contains(off, `"thinking":{"type":"disabled"}`) {
		t.Errorf("thinking off did not send disabled: %s", off)
	}
	if strings.Contains(off, `"output_config"`) {
		t.Errorf("effort was sent without being set: %s", off)
	}
	adaptive := requestJSON(t, Options{Model: "m", Thinking: "adaptive", Effort: "low"})
	if !strings.Contains(adaptive, `"thinking":{"type":"adaptive"}`) {
		t.Errorf("thinking adaptive did not send adaptive: %s", adaptive)
	}
	if !strings.Contains(adaptive, `"output_config":{"effort":"low"}`) {
		t.Errorf("effort low did not reach the request: %s", adaptive)
	}
	left := requestJSON(t, Options{Model: "m", Thinking: "model"})
	if strings.Contains(left, `"thinking"`) {
		t.Errorf("thinking model still sent a thinking field: %s", left)
	}
}

// The rules-and-tools breakpoint carries the operator's TTL; the per-round
// breakpoint on the last user block always stays on the five-minute
// lifetime, which is also what an unset TTL means.
func TestCacheTTLReachesOnlyTheSystemBreakpoint(t *testing.T) {
	hour := requestJSON(t, Options{Model: "m", CacheTTL: "1h"})
	if n := strings.Count(hour, `"ttl":"1h"`); n != 1 {
		t.Fatalf("want the 1h ttl once, got %d: %s", n, hour)
	}
	// The one 1h marker sits in the system block, after the messages in
	// this encoding, and the question's marker before it carries no ttl.
	if strings.Index(hour, `"ttl":"1h"`) < strings.Index(hour, `"system":`) {
		t.Errorf("the 1h ttl is not on the system breakpoint: %s", hour)
	}
	five := requestJSON(t, Options{Model: "m"})
	if strings.Contains(five, `"ttl"`) {
		t.Errorf("unset ttl still sent one: %s", five)
	}
	if n := strings.Count(five, breakpoint); n != 2 {
		t.Errorf("want two plain breakpoints (system and the question), got %d: %s", n, five)
	}
}
