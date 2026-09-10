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
	"ai-agent-bridge-tools__research_queue",
	"ai-agent-bridge-tools__tech_status",
	"ai-agent-bridge-tools__logistics_summary",
	"ai-agent-bridge-tools__entity_count",
	"ai-agent-bridge-tools__evolution",
	"ai-agent-bridge-tools__rockets",
	"ai-agent-bridge-tools__game_time",
	"ai-agent-bridge-tools__pollution",
	"ai-agent-bridge-tools__production_since",
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
		{"what's in the queue", "ai-agent-bridge-tools__research_queue"},
		{"what is in the research queue", "ai-agent-bridge-tools__research_queue"},
		{"technology automation-2", "ai-agent-bridge-tools__tech_status"},
		{"tech status of automation-2", "ai-agent-bridge-tools__tech_status"},
		{"logistic network status", "ai-agent-bridge-tools__logistics_summary"},
		{"how many turrets are there", "ai-agent-bridge-tools__entity_count"},
		{"what is the evolution factor", "ai-agent-bridge-tools__evolution"},
		{"rockets launched so far", "ai-agent-bridge-tools__rockets"},
		{"what is the game time", "ai-agent-bridge-tools__game_time"},
		{"how long have we played", "ai-agent-bridge-tools__game_time"},
		{"how much pollution is there", "ai-agent-bridge-tools__pollution"},
		{"iron plate production since the start", "ai-agent-bridge-tools__production_since"},
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

// "bots" routes to logistics_summary even though the question also matches
// "how many": the addendum lists logistic/bots ahead of the generic
// entity_count rule, so a robot-shaped question keeps its dedicated tool.
func TestBotsBeatsGenericEntityCount(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how many bots do we have"), everyTool)
	if got := firstBlock(t, step).Name; got != "ai-agent-bridge-tools__logistics_summary" {
		t.Errorf("called %q, want logistics_summary", got)
	}
}

func TestTechStatusArguments(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"technology automation-2", "automation-2"},
		{"tech status of automation-2", "automation-2"},
		{"tech logistics-2", "logistics-2"},
		{"what is the tech status for mining-productivity-1", "mining-productivity-1"},
	} {
		step, _ := New().Step(context.Background(), "", ask(tc.question), everyTool)
		args := input(t, firstBlock(t, step))
		if got := args["tech"]; got != tc.want {
			t.Errorf("%q: tech = %v, want %v", tc.question, got, tc.want)
		}
	}
}

func TestEntityCountArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how many turrets are there"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["name"] != "turrets" || args["surface"] != "nauvis" {
		t.Errorf("entity_count args = %v", args)
	}
}

// logistics_summary, evolution and pollution all take a required surface
// that the fake model has no way to read out of the question, so all three
// default to nauvis, matching item_rate's existing default.
func TestSurfaceDefaultsToNauvis(t *testing.T) {
	for _, tc := range []struct{ question, tool string }{
		{"logistic network status", "ai-agent-bridge-tools__logistics_summary"},
		{"what is the evolution factor", "ai-agent-bridge-tools__evolution"},
		{"how much pollution is there", "ai-agent-bridge-tools__pollution"},
	} {
		step, _ := New().Step(context.Background(), "", ask(tc.question), everyTool)
		block := firstBlock(t, step)
		if block.Name != tc.tool {
			t.Fatalf("%q called %q, want %q", tc.question, block.Name, tc.tool)
		}
		if got := input(t, block)["surface"]; got != "nauvis" {
			t.Errorf("%q: surface = %v, want nauvis", tc.question, got)
		}
	}
}

func TestProductionSinceArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("iron plate production since the start"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["surface"] != "nauvis" || args["item"] != "iron-plate" {
		t.Errorf("default production_since args = %v", args)
	}
	if got := args["since_tick"]; got != float64(0) {
		t.Errorf("since_tick = %v, want 0", got)
	}

	step, _ = New().Step(context.Background(), "", ask("production of copper-plate since the start"), everyTool)
	if got := input(t, firstBlock(t, step))["item"]; got != "copper-plate" {
		t.Errorf("item = %v, want copper-plate", got)
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
