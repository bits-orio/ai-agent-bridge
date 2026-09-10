package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// Nothing in this package has run against the live API, so what these tests
// can check is that the request bodies the SDK builds from neutral blocks say
// what the Messages API expects.
func TestMessageParamsCarryEveryBlockKind(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: what forces are there"}}},
		{Role: model.RoleAssistant, Blocks: []model.Block{{
			Type: model.BlockToolUse, ID: "toolu_1", Name: "tools__list_forces",
			Input: json.RawMessage(`{"force":"player"}`),
		}}},
		{Role: model.RoleUser, Blocks: []model.Block{{
			Type: model.BlockToolResult, ID: "toolu_1", Content: `{"forces":[]}`, IsError: true,
		}}},
	}

	body, err := json.Marshal(toMessageParams(msgs))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"role":"user"`, `"role":"assistant"`,
		`"type":"text"`, `"type":"tool_use"`, `"type":"tool_result"`,
		`"id":"toolu_1"`, `"name":"tools__list_forces"`, `"force":"player"`,
		`"tool_use_id":"toolu_1"`, `"is_error":true`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("request body is missing %s:\n%s", want, body)
		}
	}
}

// An empty turn must not become an empty message: the API rejects those.
func TestEmptyTurnsAreDropped(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockText, Text: ""}}},
		{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "hello"}}},
	}
	if got := toMessageParams(msgs); len(got) != 1 {
		t.Fatalf("built %d messages, want 1", len(got))
	}
}

// The whole JSON Schema has to reach the API, including the parts
// ToolInputSchemaParam does not name directly.
func TestToolParamsCarryTheWholeSchema(t *testing.T) {
	def := model.ToolDef{
		Name:        "tools__item_rate",
		Description: "Production rate of one item.",
		Schema: tools.ObjectSchema(map[string]any{
			"force": map[string]any{"type": "string"},
			"item":  map[string]any{"type": "string"},
		}, "force", "item"),
	}

	body, err := json.Marshal(toToolParams([]model.ToolDef{def}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"name":"tools__item_rate"`,
		`"description":"Production rate of one item."`,
		`"required":["force","item"]`,
		`"additionalProperties":false`,
		`"type":"object"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("tool body is missing %s:\n%s", want, body)
		}
	}
}

func TestRequiredNamesAcceptsBothShapes(t *testing.T) {
	if got := requiredNames([]string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Errorf("[]string: %v", got)
	}
	if got := requiredNames([]any{"a", 2}); len(got) != 1 || got[0] != "a" {
		t.Errorf("[]any: %v", got)
	}
	if got := requiredNames(nil); got != nil {
		t.Errorf("nil: %v", got)
	}
}

func TestDecodeInputAlwaysBuildsAnObject(t *testing.T) {
	if got := decodeInput(nil); got == nil || len(got) != 0 {
		t.Errorf("nil input became %v, want an empty object", got)
	}
	if got := decodeInput(json.RawMessage(`{"a":1}`)); got["a"] != float64(1) {
		t.Errorf("decoded %v", got)
	}
}
