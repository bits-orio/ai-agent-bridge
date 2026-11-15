package agent

import (
	"context"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// forcingModel is a scripted model that can be made to force a tool. It
// records which rounds were forced and with which tool name.
type forcingModel struct {
	scriptedModel
	forced []int // the call numbers that arrived through StepForced
	names  []string
}

func (m *forcingModel) StepForced(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef, tool string) (model.Step, error) {
	st, err := m.scriptedModel.Step(ctx, system, msgs, defs)
	m.forced = append(m.forced, m.calls)
	m.names = append(m.names, tool)
	return st, err
}

func forcedProbeTools() []tools.Tool {
	rec := &recordingTool{}
	return []tools.Tool{rec.tool("probe", tools.ObjectSchema(map[string]any{"n": map[string]any{"type": "integer"}}), `{"ok":true}`)}
}

func TestOnlyTheFinalRoundIsForced(t *testing.T) {
	m := &forcingModel{scriptedModel: scriptedModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{"n": 1})),
		round(use("t2", "probe", map[string]any{"n": 2})),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}}
	c := caps()
	c.MaxRounds = 3
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 6001, Text: "q", PlayerIndex: player(1)}, forcedProbeTools()); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(m.forced) != 1 || m.forced[0] != 3 || m.names[0] != SubmitTool {
		t.Errorf("forced calls = %v names = %v, want only call 3 with %s", m.forced, m.names, SubmitTool)
	}
}

func TestFirstRoundIsForcedWhenOneRound(t *testing.T) {
	m := &forcingModel{scriptedModel: scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}}
	c := caps()
	c.MaxRounds = 1
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 6002, Text: "q", PlayerIndex: player(1)}, forcedProbeTools()); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(m.forced) != 1 || m.forced[0] != 1 {
		t.Errorf("forced calls = %v, want [1]", m.forced)
	}
}

func TestModelWithoutForcingStillWorks(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{"n": 1})),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	c := caps()
	c.MaxRounds = 2
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 6003, Text: "q", PlayerIndex: player(1)}, forcedProbeTools()); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if m.calls != 2 {
		t.Errorf("calls = %d, want 2", m.calls)
	}
}

// A round that was told "one turn is left" because the token budget is nearly
// spent is as final as the last round: without forcing, a model that reads
// again there ends the question on the token notice with no answer.
func TestARoundToldTheBudgetIsNearlySpentIsForced(t *testing.T) {
	heavy := round(use("t1", "probe", map[string]any{"n": 1}))
	heavy.Usage = model.Usage{InputTokens: 100, OutputTokens: 650, CacheReadTokens: 10}
	m := &forcingModel{scriptedModel: scriptedModel{steps: []model.Step{
		heavy,
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}}
	c := caps()
	c.MaxRounds = 6
	c.MaxTokensPerQuestion = 1000
	if _, err := New(m, c).Answer(context.Background(), Question{ID: 6004, Text: "q", PlayerIndex: player(1)}, forcedProbeTools()); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(m.forced) != 1 || m.forced[0] != 2 {
		t.Errorf("forced calls = %v, want [2]: round 2 follows a round at 75%% of the token budget", m.forced)
	}
}
