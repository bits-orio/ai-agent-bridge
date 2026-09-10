package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// use builds one tool_use block, and step builds a turn out of several of them,
// which is what a round that calls two tools at once looks like.
func use(id, name string, args map[string]any) model.Block {
	raw, _ := json.Marshal(args)
	return model.Block{Type: model.BlockToolUse, ID: id, Name: name, Input: raw}
}

func round(blocks ...model.Block) model.Step {
	return model.Step{
		Blocks:     blocks,
		StopReason: model.StopToolUse,
		Usage:      model.Usage{InputTokens: 10, OutputTokens: 2},
	}
}

// recordingTool answers with out and keeps the arguments it was called with.
type recordingTool struct {
	mu   sync.Mutex
	args json.RawMessage
	runs int
}

func (r *recordingTool) tool(name string, schema map[string]any, out string) tools.Tool {
	return tools.Tool{
		Name:        name,
		Description: "a recording stub",
		Schema:      schema,
		Call: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.args = args
			r.runs++
			return json.RawMessage(out), nil
		},
	}
}

func (r *recordingTool) seen() (map[string]any, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	args := map[string]any{}
	_ = json.Unmarshal(r.args, &args)
	return args, r.runs
}

// historySchema is the shape a history tool has: force is one optional filter
// among several, not a required argument the catalog injected.
func historySchema() map[string]any {
	return tools.ObjectSchema(map[string]any{
		"event":  map[string]any{"type": "string", "description": "Event key."},
		"player": map[string]any{"type": "string", "description": "Player name."},
		"force":  map[string]any{"type": "string", "description": "Force name."},
	}, "event")
}

// The prompt tells the model that leaving force out reads the asker's force.
// That has to be true of a history tool as well, or "when did I last die"
// silently answers for every force on the server.
func TestForceIsFilledOnAHistoryShapedTool(t *testing.T) {
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("last_event", historySchema(), `{"event":"player_died","tick":900}`)}
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "last_event", map[string]any{"event": "player_died"})),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"tick 900"}}),
	}}
	a := New(m, caps())

	q := Question{ID: 1, Text: "when did I last die", PlayerIndex: player(1), Force: "enemy"}
	if _, err := a.Answer(context.Background(), q, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}

	args, runs := rec.seen()
	if runs != 1 {
		t.Fatalf("tool ran %d times, want 1", runs)
	}
	if args["force"] != "enemy" {
		t.Errorf("force = %v, want the asker's force: %v", args["force"], args)
	}
	if args["event"] != "player_died" {
		t.Errorf("the model's own argument was lost: %v", args)
	}
}

// A force the model named itself wins: any player may ask about any force.
func TestForceTheModelNamedIsKept(t *testing.T) {
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("last_event", historySchema(), `{}`)}
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "last_event", map[string]any{"event": "research_finished", "force": "player"})),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())

	q := Question{ID: 2, Text: "what did the other force research", PlayerIndex: player(1), Force: "enemy"}
	if _, err := a.Answer(context.Background(), q, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if args, _ := rec.seen(); args["force"] != "player" {
		t.Errorf("force = %v, want the force the model asked for", args["force"])
	}
}

// A tool that does not declare force never gets one, so a provider's own
// argument list is never added to behind its back.
func TestForceIsNotFilledOnAToolThatDoesNotDeclareIt(t *testing.T) {
	rec := &recordingTool{}
	schema := tools.ObjectSchema(map[string]any{"name": map[string]any{"type": "string"}}, "name")
	ts := []tools.Tool{rec.tool("provider__hello", schema, `{"greeting":"hi"}`)}
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "provider__hello", map[string]any{"name": "rig"})),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"hi"}}),
	}}
	a := New(m, caps())

	q := Question{ID: 3, Text: "hello", PlayerIndex: player(1), Force: "enemy"}
	if _, err := a.Answer(context.Background(), q, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	args, _ := rec.seen()
	if _, filled := args["force"]; filled {
		t.Errorf("force was added to a tool that never asked for one: %v", args)
	}
}

// submit_answer beside a read is refused. The answer in that round was written
// before the read came back, so it cannot be the answer to what the read says.
func TestSubmitBesideAReadIsRefused(t *testing.T) {
	rec := &recordingTool{}
	ts := []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{}), `{"forces":["player"]}`)}
	m := &scriptedModel{steps: []model.Step{
		round(
			use("s1", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"guessed too early"}}),
			use("t1", "probe", map[string]any{}),
		),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"one force"}}),
	}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 4, Text: "what forces are there", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "one force" {
		t.Fatalf("artifact = %+v, want the submission from the round after the read", res.Artifact)
	}
	if res.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", res.Rounds)
	}
	if _, runs := rec.seen(); runs != 1 {
		t.Errorf("the read ran %d times, want 1: a refused submission must not cancel the round", runs)
	}

	results := m.msgs[len(m.msgs)-1].Blocks
	if len(results) != 2 {
		t.Fatalf("the model got %d results, want one per call: %+v", len(results), results)
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "same round") {
		t.Errorf("the submission was not refused with a reason: %+v", results[0])
	}
	if results[1].IsError || !strings.Contains(results[1].Content, "player") {
		t.Errorf("the read result did not reach the model: %+v", results[1])
	}
}

// Two submissions in one round: the first in block order wins, every time.
func TestFirstSubmissionOfARoundWins(t *testing.T) {
	for i := range 20 {
		m := &scriptedModel{steps: []model.Step{round(
			use("s1", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"first"}}),
			use("s2", SubmitTool, map[string]any{"shape": "notice", "text": "second"}),
		)}}
		a := New(m, caps())

		res, err := a.Answer(context.Background(), Question{ID: int64(i), Text: "x", PlayerIndex: player(1)}, nil)
		if err != nil {
			t.Fatalf("answer: %v", err)
		}
		if res.Artifact.Shape != ShapeSummary || res.Artifact.Line() != "first" {
			t.Fatalf("run %d answered with %+v, want the first submission", i, res.Artifact)
		}
	}
}

// The first submission only wins if it is an artifact at all. A broken one is
// refused and the next one in the round is taken.
func TestABrokenFirstSubmissionFallsThroughToTheNext(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{round(
		use("s1", SubmitTool, map[string]any{"shape": "chart", "lines": []string{"nope"}}),
		use("s2", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"second"}}),
	)}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 5, Text: "x", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "second" {
		t.Fatalf("artifact = %+v, want the submission that validates", res.Artifact)
	}
}

// A model that ends its turn with blank lines around its answer spends none of
// the three summary lines on them.
func TestBlankLinesNeverBecomeSummaryLines(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{textStep("\n  \n\nTwo forces are playing.\n\nThe other one is idle.\n\n")}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 6, Text: "forces", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	want := []string{"Two forces are playing.", "The other one is idle."}
	if res.Artifact.Shape != ShapeSummary || len(res.Artifact.Lines) != len(want) {
		t.Fatalf("artifact = %+v, want %d written lines", res.Artifact, len(want))
	}
	for i, line := range want {
		if res.Artifact.Lines[i] != line {
			t.Errorf("line %d = %q, want %q", i, res.Artifact.Lines[i], line)
		}
	}
}

// Text that is nothing but whitespace is not an answer, so it becomes the
// stalled notice rather than a summary of empty lines.
func TestAllBlankTextBecomesTheStalledNotice(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{textStep("\n \n\t\n")}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 7, Text: "forces", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || res.Artifact.Text != stalledNotice {
		t.Fatalf("artifact = %+v, want the stalled notice", res.Artifact)
	}
}

// flakyModel fails its first step and answers from then on, which is what a
// model outage in the middle of a busy evening looks like.
type flakyModel struct{ calls int }

func (m *flakyModel) Name() string { return "flaky" }

func (m *flakyModel) Step(context.Context, string, []model.Message, []model.ToolDef) (model.Step, error) {
	m.calls++
	if m.calls == 1 {
		return model.Step{}, errors.New("no route to host")
	}
	return submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"answered"}}), nil
}

// A question the model never answered gives its quota slot back, so an outage
// does not spend the asker's hourly allowance.
func TestQuotaIsRefundedWhenTheModelFails(t *testing.T) {
	m := &flakyModel{}
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	a := New(m, c)
	q := Question{ID: 8, Text: "what forces are there", PlayerIndex: player(9)}

	if _, err := a.Answer(context.Background(), q, nil); err == nil {
		t.Fatal("expected the model error to surface")
	}

	q.ID = 9
	res, err := a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if res.Artifact.Line() != "answered" {
		t.Fatalf("artifact = %+v, want the retry to reach the model", res.Artifact)
	}

	q.ID = 10
	res, err = a.Answer(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("third answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || !strings.Contains(res.Artifact.Text, "allowance") {
		t.Fatalf("artifact = %+v, want the quota notice: the refund gave back one slot, not two", res.Artifact)
	}
}

// The asker label names the player when the companion sent a name, because
// history rows are keyed by player name and the model needs the name to ask
// about them.
func TestAskerLabelPrefersThePlayerName(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    Question
		want string
	}{
		{"name and index", Question{PlayerName: "alice", PlayerIndex: player(3), Force: "enemy"}, "alice (player 3, force enemy)"},
		{"name only", Question{PlayerName: "alice"}, "alice (force player)"},
		{"index only", Question{PlayerIndex: player(3)}, "player 3 (force player)"},
		{"caller's own label", Question{Asker: "the bridge"}, "the bridge"},
		{"neither", Question{}, "another mod (force player)"},
	} {
		if got := tc.q.AskerLabel(); got != tc.want {
			t.Errorf("%s: label = %q, want %q", tc.name, got, tc.want)
		}
	}

	prompt := systemPrompt(Question{Text: "x", PlayerName: "alice", PlayerIndex: player(3), Force: "enemy"})
	if !strings.Contains(prompt, "alice (player 3, force enemy)") {
		t.Errorf("the system prompt does not name the asker:\n%s", prompt)
	}
}
