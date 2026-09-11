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
	if c := New("k", "m", 0); c.maxOutput != DefaultMaxOutputTokens {
		t.Errorf("maxOutput = %d, want %d", c.maxOutput, DefaultMaxOutputTokens)
	}
	if c := New("k", "m", 1500); c.maxOutput != 1500 {
		t.Errorf("maxOutput = %d, want 1500", c.maxOutput)
	}
}
