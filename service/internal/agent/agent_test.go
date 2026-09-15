package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
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
	return Caps{MaxRounds: 6, MaxTokensPerQuestion: 20000, QuestionsPerPlayerPerHour: 20}
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

// The next question in the same scope carries the last exchange, whoever
// asked it, with the answer as the game rendered it.
func TestAnswerSharesTheSessionAcrossAskers(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"42 plates a minute"}})}}
	a := New(m, caps())
	q := Question{ID: 11, Text: "iron plate rate", PlayerIndex: player(3), PlayerName: "Alice"}

	first, err := a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if !first.Session.Fresh || first.Artifact.Session == nil || !first.Artifact.Session.Fresh {
		t.Errorf("the first question must start the session and say so: %+v", first.Session)
	}
	q = Question{ID: 12, Text: "and copper", PlayerIndex: player(4), PlayerName: "Bob"}
	second, err := a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if second.Session.Fresh {
		t.Error("the second question continues the session")
	}
	got := m.msgs[0].Blocks[0].Text
	for _, want := range []string{"Alice asked: iron plate rate", "Answer: 42 plates a minute", "Question: and copper"} {
		if !strings.Contains(got, want) {
			t.Errorf("the follow-up prompt is missing %q:\n%s", want, got)
		}
	}
}

// A private scope and a named session never see the global session, and
// "new" starts over.
func TestAnswerKeepsScopesAndNamesApart(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"first answer"}})}}
	a := New(m, caps())
	if _, err := a.Answer(context.Background(), Question{ID: 1, Text: "first question", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []Question{
		{ID: 2, Text: "private follow-up", PlayerIndex: player(1), Scope: "team-3"},
		{ID: 3, Text: "#iron named follow-up", PlayerIndex: player(1)},
		{ID: 4, Text: "new fresh follow-up", PlayerIndex: player(1)},
	} {
		res, err := a.Answer(context.Background(), q, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Session.Fresh {
			t.Errorf("question %d should start its own session", q.ID)
		}
		if got := m.msgs[0].Blocks[0].Text; strings.Contains(got, "first answer") {
			t.Errorf("question %d saw the global session:\n%s", q.ID, got)
		}
	}
	if res, _ := a.Answer(context.Background(), Question{ID: 5, Text: "#iron again", PlayerIndex: player(2)}, nil); res.Session.Fresh || res.Session.Name != "iron" {
		t.Errorf("#iron should continue for another asker: %+v", res.Session)
	}
}

// Idle time ends a session: a question after the idle cut starts clean.
func TestSessionIdlesOut(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"first answer"}})}}
	c := caps()
	c.Sessions.Idle = time.Minute
	a := New(m, c)
	now := time.Now()
	a.now = func() time.Time { return now }
	q := Question{ID: 13, Text: "first question", PlayerIndex: player(4)}

	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	now = now.Add(2 * time.Minute)
	q.ID, q.Text = 14, "second question"
	res, err := a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if !res.Session.Fresh {
		t.Error("two minutes of silence must start a new session")
	}
	if got := m.msgs[0].Blocks[0].Text; strings.Contains(got, "first answer") {
		t.Errorf("the ended session came back:\n%s", got)
	}
}

// An ask-back (a notice at level confirmation) widens the session's idle
// window to clarify_idle: a follow-up arriving after the ordinary idle but
// inside the clarify window must still land in the same session, or the
// player's reply opens a conversation with no memory of the question that
// prompted it (docs/design/phase4-spec.md section 12).
func TestAskBackWidensTheSessionIdleWindow(t *testing.T) {
	askBack := submitStep("t1", map[string]any{"shape": "notice", "text": "Which surface, nauvis or the platform?", "level": LevelConfirmation})
	answer := submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"nauvis it is"}})
	m := &scriptedModel{steps: []model.Step{askBack, answer}}
	c := caps()
	c.Sessions.Idle = time.Minute
	c.Sessions.ClarifyIdle = 5 * time.Minute
	a := New(m, c)
	now := time.Now()
	a.now = func() time.Time { return now }

	first, err := a.Answer(context.Background(), Question{ID: 21, Text: "how much iron is left", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if first.Artifact.Shape != ShapeNotice || first.Artifact.Level != LevelConfirmation {
		t.Fatalf("first answer must be the ask-back: %+v", first.Artifact)
	}

	// Two minutes on: past the ordinary one-minute idle, still inside the
	// five-minute clarify window.
	now = now.Add(2 * time.Minute)
	second, err := a.Answer(context.Background(), Question{ID: 22, Text: "nauvis", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if second.Session.Fresh {
		t.Fatal("the clarify window should have kept the session alive past the ordinary idle")
	}
	if got := m.msgs[0].Blocks[0].Text; !strings.Contains(got, "how much iron is left") {
		t.Errorf("the reply lost the question it was answering:\n%s", got)
	}

	// The reply landed, so the window is back to ordinary: two more minutes
	// of silence must end it.
	now = now.Add(2 * time.Minute)
	third, err := a.Answer(context.Background(), Question{ID: 23, Text: "one more", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("third answer: %v", err)
	}
	if !third.Session.Fresh {
		t.Error("the flag must clear on the next question: an ordinary follow-up does not keep the clarify window open")
	}
}

// "sessions" and a bare "new" never reach the model and cost nothing.
func TestSessionCommandsSkipTheModel(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"an answer"}})}}
	a := New(m, caps())
	empty, _ := a.Answer(context.Background(), Question{ID: 1, Text: "sessions", PlayerIndex: player(1)}, nil)
	if empty.Artifact.Shape != ShapeNotice || empty.Rounds != 0 {
		t.Errorf("sessions with nothing live = %+v", empty.Artifact)
	}
	if _, err := a.Answer(context.Background(), Question{ID: 2, Text: "#iron a question", PlayerIndex: player(1), PlayerName: "Alice"}, nil); err != nil {
		t.Fatal(err)
	}
	listed, _ := a.Answer(context.Background(), Question{ID: 3, Text: "sessions", PlayerIndex: player(2)}, nil)
	if listed.Artifact.Shape != ShapeList || len(listed.Artifact.Items) != 1 || !strings.Contains(listed.Artifact.Items[0], "#iron: 1 exchange") || !strings.Contains(listed.Artifact.Items[0], "Alice") {
		t.Errorf("sessions listing = %+v", listed.Artifact)
	}
	if hidden, _ := a.Answer(context.Background(), Question{ID: 4, Text: "sessions", Scope: "team-3", PlayerIndex: player(3)}, nil); hidden.Artifact.Shape != ShapeNotice {
		t.Errorf("a private scope must not list global sessions: %+v", hidden.Artifact)
	}
	reset, _ := a.Answer(context.Background(), Question{ID: 5, Text: "new #iron", PlayerIndex: player(1)}, nil)
	if reset.Artifact.Shape != ShapeNotice || reset.Artifact.Level != LevelConfirmation || !reset.Session.Fresh {
		t.Errorf("new #iron = %+v", reset.Artifact)
	}
	if again, _ := a.Answer(context.Background(), Question{ID: 6, Text: "sessions", PlayerIndex: player(1)}, nil); again.Artifact.Shape != ShapeNotice {
		t.Errorf("after new #iron the listing should be empty: %+v", again.Artifact)
	}
	if m.calls != 1 {
		t.Errorf("the model was called %d times, want once for the one real question", m.calls)
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
	q := Question{Text: "x", Force: "enemy", Asker: "player 1"}
	system := systemPrompt("")
	for _, want := range []string{"untrusted", SubmitTool} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt is missing %q:\n%s", want, system)
		}
	}
	if turn := prompt(q, nil); !strings.Contains(turn, "enemy") {
		t.Errorf("the user turn does not name the asker's force:\n%s", turn)
	}
}

// The briefing's own five Group A trips (Assemble, briefing.go) run outside
// the round loop entirely: they must never be mistaken for a lookup the
// model asked for. A cap of 1 still admits the model's one real call
// untouched by the five trips the briefing just spent, and the round that
// ran it carries a ledger.ToolCall for that one call alone. happyGroupATools
// and askerQuestion come from briefing_test.go, the same package.
func TestBriefingTripsNeverCountAgainstToolCallsOrTheCap(t *testing.T) {
	dir := t.TempDir()
	ts := append(happyGroupATools(), stubTool("probe", `{"ok":true}`, nil))
	m := &scriptedModel{steps: []model.Step{
		toolStep("t1", "probe", map[string]any{}),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	c := caps()
	c.MaxToolCalls = 1 // if the briefing's five trips counted here, this alone would refuse the model's one real lookup
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)
	a.BriefingEnabled = true

	q := askerQuestion()
	q.ID = 2201
	q.Text = "status check"
	res, err := a.Answer(context.Background(), q, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary {
		t.Fatalf("artifact = %+v, want the model's own answer to go through, not a cap refusal", res.Artifact)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 2201)
	if rec.Lookups != 1 {
		t.Fatalf("lookups = %d, want 1: the briefing's five Group A trips must never be counted as lookups", rec.Lookups)
	}

	rounds := roundsFor(t, dir, 2201)
	if len(rounds) != 2 {
		t.Fatalf("round records = %d, want 2", len(rounds))
	}
	if len(rounds[0].ToolCalls) != 1 {
		t.Fatalf("round 1 tool_calls = %+v, want exactly the model's own probe call, none of the briefing's five trips", rounds[0].ToolCalls)
	}
	if tc := rounds[0].ToolCalls[0]; tc.Name != "probe" || !tc.OK {
		t.Errorf("round 1's one tool_call = %+v, want an unrefused probe call", tc)
	}
	if len(rounds[1].ToolCalls) != 0 {
		t.Errorf("round 2 (the submit round) tool_calls = %+v, want none", rounds[1].ToolCalls)
	}
}

// zero_lookup is the free tier's own signature (ledger.go's zeroLookupFor),
// not a function of whether the briefing itself succeeded: a failed
// briefing carries no text into the prompt, so the model must do its own
// lookups the same as if briefing were off, and this must not read as a
// free-tier win.
func TestZeroLookupIsFalseWhenABriefingFailsAndTheModelHasToLookThingsUp(t *testing.T) {
	dir := t.TempDir()
	ts := withoutTool(happyGroupATools(), "game_time") // no game_time: the whole briefing fails
	ts = append(ts, stubTool("probe", `{"ok":true}`, nil))
	m := &scriptedModel{steps: []model.Step{
		toolStep("t1", "probe", map[string]any{}),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	a.BriefingEnabled = true

	q := askerQuestion()
	q.ID = 2301
	q.Text = "status check"
	if _, err := a.Answer(context.Background(), q, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 2301)
	if rec.Briefing != ledger.BriefingFailed {
		t.Fatalf("briefing = %q, want %q", rec.Briefing, ledger.BriefingFailed)
	}
	if rec.Lookups != 1 {
		t.Fatalf("lookups = %d, want 1: the model had to look things up itself once the briefing failed", rec.Lookups)
	}
	if rec.ZeroLookup {
		t.Error("zero_lookup = true, want false: a failed briefing is not the free tier's own signature, and this question needed a real lookup")
	}
}

// ms_rcon is briefing_ms and every round's own tool wall clock, added once
// each (agent.go's Answer: msRCON += briefingMs before the round loop,
// msRCON += roundToolMs inside it). A later change that recomputes it from
// the wrong base, or adds either half twice, would slip past every existing
// test that only ever has one of the two non-zero at a time.
func TestMsRCONSumsBriefingAndRoundToolTimeExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	briefingDelay := 5 * time.Millisecond
	toolDelay := 5 * time.Millisecond
	ts := withTool(happyGroupATools(), "game_time",
		slowTool(catalog.ToolName(engineIface, "game_time"), briefingDelay, `{"tick":1,"hours":1}`))
	ts = append(ts, slowTool("probe", toolDelay, `{"ok":true}`))

	m := &scriptedModel{steps: []model.Step{
		toolStep("t1", "probe", map[string]any{}),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	a.BriefingEnabled = true

	q := askerQuestion()
	q.ID = 2401
	q.Text = "status check"
	if _, err := a.Answer(context.Background(), q, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 2401)
	if rec.BriefingMs <= 0 {
		t.Fatalf("briefing_ms = %d, want > 0", rec.BriefingMs)
	}
	if want := rec.BriefingMs + toolDelay.Milliseconds(); rec.MsRCON < want {
		t.Errorf("ms_rcon = %d, want at least briefing_ms (%d) plus the model's own tool time (%dms), summed once each",
			rec.MsRCON, rec.BriefingMs, toolDelay.Milliseconds())
	}
	// A generous ceiling: if briefing_ms were folded in twice, or the
	// round's own tool time were counted twice, ms_rcon would run well past
	// this rather than sitting just above the sum checked above.
	if ceiling := 2*rec.BriefingMs + 2*toolDelay.Milliseconds(); rec.MsRCON > ceiling {
		t.Errorf("ms_rcon = %d, want at most %d: briefing_ms or the round's own tool time looks double-counted", rec.MsRCON, ceiling)
	}
}

// The catalog names an engine tool <iface>__<fn> and leaves the five history
// tools and two ranking tools bare, so a model that has read nineteen prefixed
// names invents a prefix for the seven that have none. Question 78 on
// 2026-09-15 called "mts-v1__catch_up", got "there is no tool named", then
// called "catch_up" and got its answer: a whole round spent on a naming
// artifact, and the only reason that question got slower rather than faster.
func TestDidYouMeanNamesTheToolTheModelProbablyWanted(t *testing.T) {
	byName := map[string]tools.Tool{
		"catch_up":                       {Name: "catch_up"},
		"recent_chat":                    {Name: "recent_chat"},
		"ai-agent-bridge-tools__rockets": {Name: "ai-agent-bridge-tools__rockets"},
		"mts-v1__team_clocks":            {Name: "mts-v1__team_clocks"},
	}

	for _, tc := range []struct{ asked, want, why string }{
		{"mts-v1__catch_up", "catch_up", "a prefix invented on a bare tool, the live case"},
		{"ai-agent-bridge-tools__recent_chat", "recent_chat", "the same, with the other provider"},
		{"rockets", "ai-agent-bridge-tools__rockets", "a prefix dropped from a real one, the mirror error"},
		{"catch_up", "", "a name that resolves needs no suggestion"},
		{"nonsense", "", "nothing close enough to name"},
		{"mts-v1__nonsense", "", "a real prefix but no such tool anywhere"},
		{"__", "", "degenerate input must not panic or match"},
	} {
		if got := didYouMean(tc.asked, byName); got != tc.want {
			t.Errorf("didYouMean(%q) = %q, want %q (%s)", tc.asked, got, tc.want, tc.why)
		}
	}
}

// A suffix two providers both offer is genuinely ambiguous, and guessing there
// would point the model at the wrong provider's tool. Saying nothing is the
// only honest answer.
func TestDidYouMeanSaysNothingWhenTwoProvidersCouldBeMeant(t *testing.T) {
	byName := map[string]tools.Tool{
		"mts-v1__standings":    {Name: "mts-v1__standings"},
		"other-mod__standings": {Name: "other-mod__standings"},
	}
	if got := didYouMean("standings", byName); got != "" {
		t.Errorf("didYouMean(\"standings\") = %q, want no suggestion at all when two providers offer it", got)
	}
}
