package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// scriptedModel plays a fixed list of steps, repeating the last one once the
// list runs out, which is what a model that never submits looks like.
type scriptedModel struct {
	steps  []model.Step
	calls  int
	system string
	msgs   []model.Message
	defs   []model.ToolDef
}

func (m *scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) Step(_ context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	m.calls++
	m.system = system
	m.msgs = msgs
	m.defs = defs
	if m.calls <= len(m.steps) {
		return m.steps[m.calls-1], nil
	}
	return m.steps[len(m.steps)-1], nil
}

func toolStep(id, name string, args map[string]any) model.Step {
	raw, _ := json.Marshal(args)
	return model.Step{
		Blocks:     []model.Block{{Type: model.BlockToolUse, ID: id, Name: name, Input: raw}},
		StopReason: model.StopToolUse,
		Usage:      model.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

func submitStep(id string, artifact map[string]any) model.Step {
	return toolStep(id, SubmitTool, artifact)
}

func textStep(text string) model.Step {
	return model.Step{
		Blocks:     []model.Block{{Type: model.BlockText, Text: text}},
		StopReason: model.StopEndTurn,
		Usage:      model.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

func stubTool(name, out string, err error) tools.Tool {
	return tools.Tool{
		Name:        name,
		Description: "a stub",
		Schema:      tools.ObjectSchema(map[string]any{"force": map[string]any{"type": "string"}}, "force"),
		Call: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			if err != nil {
				return nil, err
			}
			return json.RawMessage(out), nil
		},
	}
}

func caps() Caps {
	return Caps{MaxRounds: 6, MaxTokensPerQuestion: 20000, MemoryTTL: 10 * time.Minute, QuestionsPerPlayerPerHour: 20}
}

func player(index int) *int { return &index }

// The whole path with the scripted fake model: one tool call, then an
// artifact carrying what the tool returned.
func TestAnswerSubmitsThroughFakeModel(t *testing.T) {
	ts := []tools.Tool{stubTool("ai-agent-bridge-tools__list_forces", `{"forces":[{"name":"player","player_count":2}]}`, nil)}
	a := New(fake.New(), caps())

	res, err := a.Answer(context.Background(), Question{ID: 1, Text: "what forces are there", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary {
		t.Fatalf("shape = %q, want summary: %+v", res.Artifact.Shape, res.Artifact)
	}
	if !strings.Contains(res.Artifact.Line(), "player") {
		t.Errorf("answer does not carry the tool result: %q", res.Artifact.Line())
	}
	if res.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", res.Rounds)
	}
	if res.Usage.InputTokens != 200 || res.Usage.OutputTokens != 40 {
		t.Errorf("usage = %+v, want 200 in and 40 out over two steps", res.Usage)
	}
	if res.CostUSD != 0 {
		t.Errorf("cost = %v, want 0 for a model with no price", res.CostUSD)
	}
}

// A tool that fails is reported to the model as a failed tool result, and the
// question still answers.
func TestAnswerReportsToolErrorToTheModel(t *testing.T) {
	ts := []tools.Tool{stubTool("ai-agent-bridge-tools__list_forces", "", errors.New("provider_error: boom from provider"))}
	a := New(fake.New(), caps())

	res, err := a.Answer(context.Background(), Question{ID: 2, Text: "what forces are there", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(res.Artifact.Line(), "boom from provider") {
		t.Errorf("the tool error never reached the model: %q", res.Artifact.Line())
	}
}

// A model that calls a tool the server does not have gets told so, rather
// than the question failing.
func TestAnswerReportsUnknownTool(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		toolStep("t1", "not_a_tool", map[string]any{}),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 3, Text: "hello", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "done" {
		t.Fatalf("artifact = %+v", res.Artifact)
	}
	results := m.msgs[len(m.msgs)-1]
	if len(results.Blocks) != 1 || !results.Blocks[0].IsError {
		t.Fatalf("expected one failed tool result, got %+v", results.Blocks)
	}
	if !strings.Contains(results.Blocks[0].Content, "not_a_tool") {
		t.Errorf("error result does not name the tool: %q", results.Blocks[0].Content)
	}
}

// A model that never submits runs out of rounds and answers with a notice.
func TestAnswerExhaustsRounds(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{toolStep("t1", "probe", map[string]any{})}}
	c := caps()
	c.MaxRounds = 3
	a := New(m, c)

	res, err := a.Answer(context.Background(), Question{ID: 4, Text: "loop forever", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || !strings.Contains(res.Artifact.Text, "rounds") {
		t.Fatalf("artifact = %+v, want a notice about rounds", res.Artifact)
	}
	if res.Rounds != 3 || m.calls != 3 {
		t.Errorf("rounds = %d and model calls = %d, want 3 and 3", res.Rounds, m.calls)
	}
}

// The per-question token budget ends a question that keeps spending.
func TestAnswerStopsOnTokenBudget(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{toolStep("t1", "probe", map[string]any{})}}
	c := caps()
	c.MaxTokensPerQuestion = 20 // two steps of 12 tokens each
	a := New(m, c)

	res, err := a.Answer(context.Background(), Question{ID: 5, Text: "spend", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || !strings.Contains(res.Artifact.Text, "token budget") {
		t.Fatalf("artifact = %+v, want a notice about the token budget", res.Artifact)
	}
	if m.calls != 2 {
		t.Errorf("model calls = %d, want 2", m.calls)
	}
}

// Over quota, the model is never called at all.
func TestAnswerEnforcesQuota(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	a := New(m, c)
	q := Question{ID: 6, Text: "again", PlayerIndex: player(7)}

	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	res, err := a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || !strings.Contains(res.Artifact.Text, "allowance") {
		t.Fatalf("artifact = %+v, want the quota notice", res.Artifact)
	}
	if m.calls != 1 {
		t.Errorf("model calls = %d, want 1: the second question must not reach the model", m.calls)
	}
	if res.Rounds != 0 || res.CostUSD != 0 {
		t.Errorf("a refused question cost something: %+v", res)
	}
}

// An oversized artifact is clipped to the caps rather than refused.
func TestAnswerClipsTheArtifact(t *testing.T) {
	long := strings.Repeat("x", 400)
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{
		"shape": "summary",
		"lines": []string{long, long, long, long, long},
	})}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 7, Text: "long", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(res.Artifact.Lines) != MaxSummaryLines {
		t.Fatalf("lines = %d, want %d", len(res.Artifact.Lines), MaxSummaryLines)
	}
	for i, line := range res.Artifact.Lines {
		if len([]rune(line)) != MaxCellChars {
			t.Errorf("line %d is %d characters, want %d", i, len([]rune(line)), MaxCellChars)
		}
	}
}

// An artifact that does not validate comes back as a failed tool result, so
// the model can send a good one.
func TestAnswerRejectsABadArtifactAndRetries(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		submitStep("t1", map[string]any{"shape": "chart", "lines": []string{"nope"}}),
		submitStep("t2", map[string]any{"shape": "notice", "text": "all good", "level": "confirmation"}),
	}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 8, Text: "shape", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || res.Artifact.Text != "all good" {
		t.Fatalf("artifact = %+v", res.Artifact)
	}
	if res.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", res.Rounds)
	}
}

// A model that stops talking without submitting still answers: its text is
// wrapped as a summary.
func TestAnswerWrapsPlainTextAsASummary(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{textStep("Two forces are playing.")}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 9, Text: "forces", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary || res.Artifact.Lines[0] != "Two forces are playing." {
		t.Fatalf("artifact = %+v", res.Artifact)
	}
}

// The model failing is the one ending that is an error, so the caller can say
// so in the game rather than pretending it answered.
func TestAnswerReturnsTheModelError(t *testing.T) {
	a := New(&failingModel{}, caps())
	if _, err := a.Answer(context.Background(), Question{ID: 10, Text: "x", PlayerIndex: player(1)}, nil); err == nil {
		t.Fatal("expected the model error to surface")
	}
}

type failingModel struct{}

func (failingModel) Name() string { return "failing" }
func (failingModel) Step(context.Context, string, []model.Message, []model.ToolDef) (model.Step, error) {
	return model.Step{}, fmt.Errorf("no route to host")
}

// The next question from the same player carries the last exchange.
func TestAnswerRemembersTheLastExchange(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"42 plates a minute"}})}}
	a := New(m, caps())
	q := Question{ID: 11, Text: "iron plate rate", PlayerIndex: player(3)}

	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	q.ID, q.Text = 12, "and copper"
	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("second answer: %v", err)
	}

	first := m.msgs[0].Blocks[0].Text
	for _, want := range []string{"iron plate rate", "42 plates a minute", "Question: and copper"} {
		if !strings.Contains(first, want) {
			t.Errorf("the follow-up prompt is missing %q:\n%s", want, first)
		}
	}
}

// Memory expires: a question after the TTL starts clean.
func TestMemoryExpires(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"first answer"}})}}
	c := caps()
	c.MemoryTTL = time.Minute
	a := New(m, c)
	now := time.Now()
	a.now = func() time.Time { return now }
	q := Question{ID: 13, Text: "first question", PlayerIndex: player(4)}

	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	now = now.Add(2 * time.Minute)
	q.ID, q.Text = 14, "second question"
	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if got := m.msgs[0].Blocks[0].Text; strings.Contains(got, "first answer") {
		t.Errorf("expired memory came back:\n%s", got)
	}
}

// submit_answer is always offered, on top of whatever the server exposes.
func TestSubmitToolIsAlwaysOffered(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"ok"}})}}
	a := New(m, caps())
	if _, err := a.Answer(context.Background(), Question{ID: 15, Text: "x", PlayerIndex: player(1)}, []tools.Tool{stubTool("probe", "{}", nil)}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	names := make([]string, 0, len(m.defs))
	for _, d := range m.defs {
		names = append(names, d.Name)
	}
	if len(names) != 2 || names[1] != SubmitTool {
		t.Fatalf("tool defs = %v, want the server tool then %s", names, SubmitTool)
	}
}

// The system prompt has to say the two things the design rests on: results
// are untrusted, and the answer goes through submit_answer.
func TestSystemPromptWarnsAboutUntrustedResults(t *testing.T) {
	prompt := systemPrompt(Question{Text: "x", Force: "enemy", Asker: "player 1"})
	for _, want := range []string{"untrusted", SubmitTool, "enemy"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt is missing %q:\n%s", want, prompt)
		}
	}
}
