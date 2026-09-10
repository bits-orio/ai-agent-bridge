package fake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func defs(names ...string) []model.ToolDef {
	out := make([]model.ToolDef, 0, len(names)+1)
	for _, n := range names {
		out = append(out, model.ToolDef{Name: n})
	}
	return append(out, model.ToolDef{Name: "submit_answer"})
}

func ask(text string) []model.Message {
	return []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: " + text}}}}
}

func firstBlock(t *testing.T, step model.Step) model.Block {
	t.Helper()
	if len(step.Blocks) != 1 {
		t.Fatalf("step produced %d blocks, want 1", len(step.Blocks))
	}
	return step.Blocks[0]
}

func input(t *testing.T, block model.Block) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(block.Input, &out); err != nil {
		t.Fatalf("input is not an object: %v", err)
	}
	return out
}

var everyTool = defs(
	"ai-agent-bridge-tools__current_research",
	"ai-agent-bridge-tools__item_rate",
	"ai-agent-bridge-tools__list_forces",
	"ai-agent-bridge-tools__list_players",
	"ai-agent-bridge-tools__list_surfaces",
	"history__last_event",
	"aab-test-provider__hello",
)

func TestFirstStepPicksAToolByKeyword(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"what is being researched", "ai-agent-bridge-tools__current_research"},
		{"iron plate rate please", "ai-agent-bridge-tools__item_rate"},
		{"what forces are there", "ai-agent-bridge-tools__list_forces"},
		{"table of players", "ai-agent-bridge-tools__list_players"},
		{"which teams are playing", "ai-agent-bridge-tools__list_forces"},
		{"when did I last die", "history__last_event"},
		{"what was my last death", "history__last_event"},
		{"hello", "aab-test-provider__hello"},
		{"what surfaces exist", "ai-agent-bridge-tools__list_surfaces"},
	} {
		step, err := New().Step(context.Background(), "", ask(tc.question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", tc.question, err)
		}
		if got := firstBlock(t, step).Name; got != tc.want {
			t.Errorf("%q called %q, want %q", tc.question, got, tc.want)
		}
	}
}

func TestItemRateArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("what is the rate of copper-plate"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["item"] != "copper-plate" || args["surface"] != "nauvis" || args["window"] != "one_minute" {
		t.Errorf("item_rate args = %v", args)
	}

	step, _ = New().Step(context.Background(), "", ask("what is the production rate"), everyTool)
	if got := input(t, firstBlock(t, step))["item"]; got != "iron-plate" {
		t.Errorf("default item = %v, want iron-plate", got)
	}
}

// A question that matches nothing is answered at once, with the question
// echoed back.
func TestUnmatchedQuestionSubmitsImmediately(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how is the weather"), everyTool)
	block := firstBlock(t, step)
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	if lines := input(t, block)["lines"].([]any); !strings.Contains(lines[0].(string), "how is the weather") {
		t.Errorf("the question was not echoed: %v", lines)
	}
}

// A tool the server does not expose falls back rather than calling something
// that is not there.
func TestMissingToolFallsBack(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("hello"), defs("ai-agent-bridge-tools__list_forces"))
	if got := firstBlock(t, step).Name; got != "ai-agent-bridge-tools__list_forces" {
		t.Errorf("hello without a provider called %q, want the list_forces fallback", got)
	}

	step, _ = New().Step(context.Background(), "", ask("what surfaces exist"), defs())
	if got := firstBlock(t, step).Name; got != "submit_answer" {
		t.Errorf("a missing tool called %q, want submit_answer", got)
	}
}

// The second step submits, carrying the tool result so a test can assert on
// real game data.
func TestSecondStepSubmitsTheToolResult(t *testing.T) {
	msgs := append(ask("table of players"),
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: "ai-agent-bridge-tools__list_players"}}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{
			Type: model.BlockToolResult, ID: "fake-1",
			Content: "{\n  \"players\": [{\"name\": \"rig\"}]\n}",
		}}},
	)
	step, err := New().Step(context.Background(), "", msgs, everyTool)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	block := firstBlock(t, step)
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	args := input(t, block)
	if args["shape"] != "table" {
		t.Errorf("shape = %v, want table", args["shape"])
	}
	rows := args["rows"].([]any)
	cell := rows[0].([]any)[0].(string)
	if !strings.Contains(cell, `"name": "rig"`) {
		t.Errorf("the tool result did not reach the artifact: %q", cell)
	}
	if strings.Contains(cell, "\n") {
		t.Errorf("the tool result was not compacted: %q", cell)
	}
}

func TestShapeByKeyword(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"table of players", "table"},
		{"list the players", "list"},
		{"compare the players", "comparison"},
		{"notice about players", "notice"},
		{"who are the players", "summary"},
	} {
		msgs := append(ask(tc.question),
			model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: "x"}}},
			model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: "fake-1", Content: `{"ok":true}`}}},
		)
		step, _ := New().Step(context.Background(), "", msgs, everyTool)
		if got := input(t, firstBlock(t, step))["shape"]; got != tc.want {
			t.Errorf("%q submitted shape %v, want %v", tc.question, got, tc.want)
		}
	}
}

func TestUsageIsFixed(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("what forces are there"), everyTool)
	if step.Usage.InputTokens != 100 || step.Usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want 100 in and 20 out", step.Usage)
	}
	if step.StopReason != model.StopToolUse {
		t.Errorf("stop reason = %q, want tool_use", step.StopReason)
	}
}
