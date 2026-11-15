package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// rawUse is a tool_use block with its arguments exactly as written, so a test
// can send the same arguments with the keys in another order.
func rawUse(id, name, args string) model.Block {
	return model.Block{Type: model.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(args)}
}

// countingTool counts its runs and answers from fn.
type countingTool struct {
	mu   sync.Mutex
	runs int
}

func (c *countingTool) tool(name string, fn func(run int) (string, error)) tools.Tool {
	return tools.Tool{
		Name:        name,
		Description: "a counting stub",
		Schema:      tools.ObjectSchema(map[string]any{"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "integer"}}),
		Call: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			c.mu.Lock()
			c.runs++
			run := c.runs
			c.mu.Unlock()
			out, err := fn(run)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(out), nil
		},
	}
}

// F1 (review findings). A read that already answered this question is not
// run again: one question called research_queue four times with the same
// arguments, another six times in six rounds and ended in a notice.
func TestARepeatOfAnAnsweredCallIsNotRunAgain(t *testing.T) {
	dir := t.TempDir()
	ct := &countingTool{}
	ts := []tools.Tool{ct.tool("probe", func(int) (string, error) { return `{"ok":true}`, nil })}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("t1", "probe", `{"a":1,"b":2}`)),
		round(rawUse("t2", "probe", `{"b":2, "a":1}`)),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	if _, err := a.Answer(context.Background(), Question{ID: 5401, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if ct.runs != 1 {
		t.Errorf("the tool ran %d times, want 1", ct.runs)
	}
	got := m.msgs[4].Blocks[0] // round 2's result, read by round 3
	if got.IsError || !strings.Contains(got.Content, "Same call as turn 1: you already have this result above.") {
		t.Errorf("repeat result = %+v, want the same-call text", got)
	}
	if q := findQuestion(t, dir, 5401); q.Lookups != 1 {
		t.Errorf("lookups = %d, want 1: a repeat is not a lookup", q.Lookups)
	}
	rounds := roundsFor(t, dir, 5401)
	if tc := rounds[1].ToolCalls; len(tc) != 1 || tc[0].NotRun != ledger.NotRunRepeat || tc[0].Ms != 0 {
		t.Errorf("round 2 tool calls = %+v, want one marked repeat", tc)
	}
}

// A call that failed may be tried again: the failure may have been the
// game's, not the arguments'.
func TestAFailedCallMayBeRetried(t *testing.T) {
	ct := &countingTool{}
	ts := []tools.Tool{ct.tool("probe", func(run int) (string, error) {
		if run == 1 {
			return "", errors.New("rcon: timeout")
		}
		return `{"ok":true}`, nil
	})}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("t1", "probe", `{"a":1}`)),
		round(rawUse("t2", "probe", `{"a":1}`)),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	if _, err := New(m, caps()).Answer(context.Background(), Question{ID: 5402, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if ct.runs != 2 {
		t.Errorf("the tool ran %d times, want 2: a failed call may be retried", ct.runs)
	}
}

// Different arguments are a different call.
func TestADifferentArgumentIsNotARepeat(t *testing.T) {
	ct := &countingTool{}
	ts := []tools.Tool{ct.tool("probe", func(int) (string, error) { return `{"ok":true}`, nil })}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("t1", "probe", `{"a":1}`)),
		round(rawUse("t2", "probe", `{"a":2}`)),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	if _, err := New(m, caps()).Answer(context.Background(), Question{ID: 5403, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if ct.runs != 2 {
		t.Errorf("the tool ran %d times, want 2", ct.runs)
	}
}

// The same call twice in one turn runs once, and counts once.
func TestTheSameCallTwiceInOneTurnRunsOnce(t *testing.T) {
	dir := t.TempDir()
	ct := &countingTool{}
	ts := []tools.Tool{ct.tool("probe", func(int) (string, error) { return `{"ok":true}`, nil })}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("t1", "probe", `{"a":1}`), rawUse("t2", "probe", `{"a":1}`)),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	if _, err := a.Answer(context.Background(), Question{ID: 5404, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if ct.runs != 1 {
		t.Errorf("the tool ran %d times, want 1", ct.runs)
	}
	if q := findQuestion(t, dir, 5404); q.Lookups != 1 {
		t.Errorf("lookups = %d, want 1", q.Lookups)
	}
	results := m.msgs[2].Blocks
	if len(results) != 2 || results[0].Content != `{"ok":true}` || !strings.Contains(results[1].Content, "Same call as") {
		t.Errorf("results = %+v, want the first answered and the second pointed at it", results)
	}
}

// A submission beside nothing but repeats is not written before any result:
// every result it could need is already in the conversation, so it is taken.
func TestASubmissionBesideOnlyRepeatsIsTaken(t *testing.T) {
	ct := &countingTool{}
	ts := []tools.Tool{ct.tool("probe", func(int) (string, error) { return `{"ok":true}`, nil })}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("t1", "probe", `{"a":1}`)),
		round(rawUse("t2", "probe", `{"a":1}`), use("s2", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"from the first result"}})),
	}}
	res, err := New(m, caps()).Answer(context.Background(), Question{ID: 5405, Text: "q", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "from the first result" || res.Rounds != 2 {
		t.Errorf("artifact = %+v after %d rounds, want the submission in round 2", res.Artifact, res.Rounds)
	}
}

// A9 (review findings). Reads the round's byte budget refused never reached
// the model, so they do not spend lookups: the model can ask again.
func TestReadsTheRoundBudgetRefusedAreRefunded(t *testing.T) {
	dir := t.TempDir()
	ten := `"12345678"` // ten bytes
	var runs sync.Map
	tool := func(name string) tools.Tool {
		return tools.Tool{
			Name: name, Description: "stub", Schema: tools.ObjectSchema(map[string]any{}),
			Call: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				n, _ := runs.LoadOrStore(name, new(int))
				*n.(*int)++
				return json.RawMessage(ten), nil
			},
		}
	}
	ts := []tools.Tool{tool("a"), tool("b"), tool("c")}
	m := &scriptedModel{steps: []model.Step{
		round(rawUse("1", "a", `{}`), rawUse("2", "b", `{}`), rawUse("3", "c", `{}`)),
		round(rawUse("4", "b", `{}`)),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	c := caps()
	c.MaxToolCalls = 3
	c.MaxRoundToolResultBytes = 15
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)
	if _, err := a.Answer(context.Background(), Question{ID: 5406, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if n, _ := runs.Load("b"); n == nil || *n.(*int) != 2 {
		t.Errorf("b ran %v times, want 2: its first result was refused by the round budget, so it was not a repeat and not a spent lookup", n)
	}
	if got := m.msgs[4].Blocks[0]; got.IsError || got.Content != ten {
		t.Errorf("round 2 result = %+v, want b's result", got)
	}
	if q := findQuestion(t, dir, 5406); q.Lookups != 2 {
		t.Errorf("lookups = %d, want 2: a in round 1 and b in round 2", q.Lookups)
	}
	tc := roundsFor(t, dir, 5406)[0].ToolCalls
	if len(tc) != 3 || tc[0].Refunded || !tc[1].Refunded || !tc[2].Refunded {
		t.Errorf("round 1 tool calls = %+v, want b and c marked refunded", tc)
	}
}
