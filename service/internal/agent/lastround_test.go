package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// A2 (review findings). The system prompt states the turn cap, and it is the
// same text for every question under one config, so the cache prefix holds.
func TestTheSystemPromptStatesTheTurnCap(t *testing.T) {
	run := func(rounds int, id int64) string {
		m := &scriptedModel{steps: []model.Step{submitStep("s1", map[string]any{"shape": "summary", "lines": []string{"ok"}})}}
		c := caps()
		c.MaxRounds = rounds
		if _, err := New(m, c).Answer(context.Background(), Question{ID: id, Text: "q", PlayerIndex: player(int(id))}, nil); err != nil {
			t.Fatalf("answer: %v", err)
		}
		return m.system
	}
	four, fourAgain, six := run(4, 1), run(4, 2), run(6, 3)
	if !strings.Contains(four, "at most 4 turns") || !strings.Contains(four, "one turn is left") {
		t.Errorf("system prompt does not state the cap of 4 turns and the last-turn line:\n%s", four)
	}
	if four != fourAgain {
		t.Error("two questions under the same caps got different system prompts: the cache prefix is broken")
	}
	if !strings.Contains(six, "at most 6 turns") {
		t.Error("the turn cap in the prompt does not follow the caps")
	}
}

// lastText is the text of the last block of the newest user message the
// model was handed, "" when that block is not text.
func lastText(msgs []model.Message) string {
	last := msgs[len(msgs)-1]
	b := last.Blocks[len(last.Blocks)-1]
	if b.Type != model.BlockText {
		return ""
	}
	return b.Text
}

// The round before the last one ends with a line after the tool results
// saying one turn is left.
func TestTheModelIsToldWhenOneTurnIsLeft(t *testing.T) {
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
	var seen [][]model.Message
	m := &watchingModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{"n": 1})),
		round(use("t2", "probe", map[string]any{"n": 2})),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}, seen: &seen}
	c := caps()
	c.MaxRounds = 3
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 5201, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("model saw %d rounds, want 3", len(seen))
	}
	if got := lastText(seen[1]); got != "" {
		t.Errorf("round 2 was told %q, want nothing: two turns were left", got)
	}
	got := lastText(seen[2])
	if !strings.Contains(got, "One turn is left") || !strings.Contains(got, SubmitTool) {
		t.Errorf("round 3 was told %q, want the one-turn-left line naming %s", got, SubmitTool)
	}
	// The results still come first: a tool result after text is refused by
	// the Anthropic API.
	blocks := seen[2][len(seen[2])-1].Blocks
	if len(blocks) != 2 || blocks[0].Type != model.BlockToolResult {
		t.Errorf("last user message = %+v, want the tool result then the line", blocks)
	}
}

// The same line comes when the token budget is nearly spent, whatever round
// it is.
func TestTheModelIsToldWhenTheTokenBudgetIsNearlySpent(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	heavy := round(use("t1", "probe", map[string]any{"n": 1}))
	heavy.Usage = model.Usage{InputTokens: 100, OutputTokens: 650, CacheReadTokens: 10}
	var seen [][]model.Message
	m := &watchingModel{steps: []model.Step{heavy, submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"done"}})}, seen: &seen}
	c := caps()
	c.MaxTokensPerQuestion = 1000
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 5202, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got := lastText(seen[1]); !strings.Contains(got, "One turn is left") {
		t.Errorf("round 2 was told %q, want the one-turn-left line at 75%% of the budget", got)
	}
}

// On the last round a valid answer beside reads is taken, and the reads are
// never run: no model would ever see their results.
func TestTheLastRoundTakesAnAnswerBesideReadsAndSkipsThem(t *testing.T) {
	dir := t.TempDir()
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{"n": 1})),
		round(use("t2", "probe", map[string]any{"n": 2}), use("s2", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"the answer"}})),
	}}
	c := caps()
	c.MaxRounds = 2
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)
	res, err := a.Answer(context.Background(), Question{ID: 5203, Text: "q", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if res.Artifact.Line() != "the answer" {
		t.Errorf("artifact = %+v, want the answer sent beside the read on the last round", res.Artifact)
	}
	if _, runs := rec.seen(); runs != 1 {
		t.Errorf("the tool ran %d times, want 1: the last round's read has no reader", runs)
	}
	q := findQuestion(t, dir, 5203)
	if q.Lookups != 1 {
		t.Errorf("lookups = %d, want 1", q.Lookups)
	}
	rounds := roundsFor(t, dir, 5203)
	if len(rounds) != 2 || len(rounds[1].ToolCalls) != 1 || rounds[1].ToolCalls[0].NotRun != ledger.NotRunLastRound {
		t.Errorf("round 2 tool calls = %+v, want the read recorded as not run on the last round", rounds[len(rounds)-1].ToolCalls)
	}
}

// A last round of reads alone ends the question at once, without running
// them, with the pending answer when there is one.
func TestTheLastRoundOfReadsAloneRunsNothing(t *testing.T) {
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{"n": 1})),
		round(use("t2", "probe", map[string]any{"n": 2})),
	}}
	c := caps()
	c.MaxRounds = 2
	res, err := New(m, c).Answer(context.Background(), Question{ID: 5204, Text: "q", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Text != roundsNotice {
		t.Errorf("artifact = %+v, want the rounds notice", res.Artifact)
	}
	if _, runs := rec.seen(); runs != 1 {
		t.Errorf("the tool ran %d times, want 1", runs)
	}

	// With an over-cap answer pending, that answer goes out instead.
	rec2 := &recordingTool{}
	ts2 := []tools.Tool{rec2.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
	m2 := &scriptedModel{steps: []model.Step{overlong("s1"), round(use("t2", "probe", map[string]any{"n": 2}))}}
	res2, err := New(m2, c).Answer(context.Background(), Question{ID: 5205, Text: "q", PlayerIndex: player(2)}, ts2)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res2.Artifact.Shape != ShapeSummary {
		t.Errorf("artifact = %+v, want the pending answer, cut", res2.Artifact)
	}
	if _, runs := rec2.seen(); runs != 0 {
		t.Errorf("the tool ran %d times, want 0", runs)
	}
}

// A10. A turn cut off by the output limit may hold a half-written call: none
// of its calls run, and each is sent back to be sent again, shorter.
func TestACallCutOffByTheOutputLimitIsNotRun(t *testing.T) {
	dir := t.TempDir()
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
	cut := round(use("t1", "probe", map[string]any{}), use("s1", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"half"}}))
	cut.StopReason = model.StopMaxTokens
	m := &scriptedModel{steps: []model.Step{cut, submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"whole"}})}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	res, err := a.Answer(context.Background(), Question{ID: 5206, Text: "q", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if _, runs := rec.seen(); runs != 0 {
		t.Errorf("the cut-off call ran %d times, want 0", runs)
	}
	if res.Artifact.Line() != "whole" {
		t.Errorf("artifact = %+v, want the next round's answer, not the cut-off one", res.Artifact)
	}
	for i, b := range m.msgs[2].Blocks {
		if !b.IsError || !strings.Contains(b.Content, "cut off by the output limit") {
			t.Errorf("result %d = %+v, want the cut-off refusal", i, b)
		}
	}
	rounds := roundsFor(t, dir, 5206)
	if len(rounds[0].SubmitRefusals) != 1 || rounds[0].SubmitRefusals[0].Kind != ledger.RefusedCutOff {
		t.Errorf("round 1 refusals = %+v, want one cut_off", rounds[0].SubmitRefusals)
	}
	if len(rounds[0].ToolCalls) != 1 || rounds[0].ToolCalls[0].NotRun != ledger.NotRunCutOff {
		t.Errorf("round 1 tool calls = %+v, want the read recorded as not run", rounds[0].ToolCalls)
	}
	if q := findQuestion(t, dir, 5206); q.Lookups != 0 {
		t.Errorf("lookups = %d, want 0", q.Lookups)
	}
}

// watchingModel is scriptedModel that keeps a copy of every conversation it
// was handed, not just the last.
type watchingModel struct {
	steps []model.Step
	calls int
	seen  *[][]model.Message
}

func (m *watchingModel) Name() string { return "watching" }

func (m *watchingModel) Step(_ context.Context, _ string, msgs []model.Message, _ []model.ToolDef) (model.Step, error) {
	m.calls++
	*m.seen = append(*m.seen, append([]model.Message(nil), msgs...))
	if m.calls <= len(m.steps) {
		return m.steps[m.calls-1], nil
	}
	return m.steps[len(m.steps)-1], nil
}
