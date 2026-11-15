package fake

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func afterCall(question, tool, result string) []model.Message {
	return append(ask(question),
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: tool}}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: "fake-1", Content: result}}},
	)
}

var scriptTools = defs("ai-agent-bridge-tools__list_forces", "history__count_events")

func stepOf(t *testing.T, msgs []model.Message) model.Block {
	t.Helper()
	step, err := New().Step(context.Background(), "", msgs, scriptTools)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return firstBlock(t, step)
}

// loop repeat calls the same tool with the same arguments on turns 1 and 2,
// and only then submits, quoting the second result.
func TestLoopRepeatCallsTheSameToolTwice(t *testing.T) {
	q := "loop repeat what forces are there"
	first := stepOf(t, ask(q))
	if first.Name != "ai-agent-bridge-tools__list_forces" {
		t.Fatalf("turn 1 called %q, want list_forces", first.Name)
	}
	msgs := afterCall(q, first.Name, "forces result")
	second := stepOf(t, msgs)
	if second.Name != first.Name || string(second.Input) != string(first.Input) {
		t.Fatalf("turn 2 called %q %s, want the same call as turn 1 (%q %s)", second.Name, second.Input, first.Name, first.Input)
	}
	msgs = append(msgs,
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{second}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: second.ID, Content: "Same call as turn 1"}}},
	)
	third := stepOf(t, msgs)
	if third.Name != "submit_answer" {
		t.Fatalf("turn 3 called %q, want submit_answer", third.Name)
	}
	if lines := input(t, third)["lines"].([]any); !strings.Contains(lines[0].(string), "Same call as turn 1") {
		t.Errorf("the answer does not carry the last result: %v", lines)
	}
}

// loop salvage submits a summary with its prose in text and no lines, which
// is what the agent's salvage exists to repair.
func TestLoopSalvageSubmitsProseInText(t *testing.T) {
	block := stepOf(t, ask("loop salvage"))
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	args := input(t, block)
	if args["shape"] != "summary" || args["text"] != SalvageText {
		t.Errorf("args = %v, want a summary with text %q", args, SalvageText)
	}
	if _, has := args["lines"]; has {
		t.Errorf("the submission carries lines, so there is nothing to salvage: %v", args)
	}
}

// loop empty submits the same empty summary on every turn.
func TestLoopEmptySubmitsNothingEveryTurn(t *testing.T) {
	q := "loop empty"
	first := stepOf(t, ask(q))
	args := input(t, first)
	if first.Name != "submit_answer" || args["shape"] != "summary" || len(args) != 1 {
		t.Fatalf("turn 1 = %q %v, want an empty summary", first.Name, args)
	}
	msgs := append(ask(q),
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{first}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: first.ID, Content: "refused", IsError: true}}},
	)
	second := stepOf(t, msgs)
	if second.Name != "submit_answer" || string(second.Input) != string(first.Input) {
		t.Errorf("turn 2 = %q %s, want the same empty summary again", second.Name, second.Input)
	}
}

// unknown kind asks count_events for a kind no map records, then answers with
// what the tool said.
func TestUnknownKindCountsAMissingEvent(t *testing.T) {
	q := "unknown kind of event: how many restarts"
	first := stepOf(t, ask(q))
	if first.Name != "history__count_events" {
		t.Fatalf("called %q, want count_events", first.Name)
	}
	if got := input(t, first)["event"]; got != UnknownKindEvent {
		t.Errorf("event = %v, want %q", got, UnknownKindEvent)
	}
	reply := stepOf(t, afterCall(q, first.Name, `no events of kind "restart"; kinds: question 3`))
	lines := input(t, reply)["lines"].([]any)
	if !strings.Contains(lines[0].(string), "kinds: question 3") {
		t.Errorf("the tool's text did not reach the answer: %v", lines)
	}
}

// A loop keyword with its tool missing falls back to the ordinary script.
func TestLoopKeywordWithoutItsToolFallsBack(t *testing.T) {
	step, err := New().Step(context.Background(), "", ask("unknown kind of event"), defs())
	if err != nil {
		t.Fatal(err)
	}
	if got := firstBlock(t, step).Name; got != "submit_answer" {
		t.Errorf("called %q, want submit_answer", got)
	}
}
