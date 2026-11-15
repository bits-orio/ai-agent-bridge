package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// cold is a read round on a host with no cache entry: every input token is
// fresh, the fixed prefix included.
func cold(id string, n, input int) model.Step {
	s := round(use(id, "probe", map[string]any{"n": n}))
	s.Usage = model.Usage{InputTokens: input, OutputTokens: 300}
	return s
}

func probeTools() []tools.Tool {
	return []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
}

// A3 (review findings). A question on a cold cache pays the fixed prefix,
// system prompt and tool list, in fresh tokens every round. That is the
// host's cache luck, not the question's size, and must not spend the budget:
// the ledger's failing question had rounds of 12.9k then 14.2k fresh input
// with no cache read, about 12.5k of each the prefix, and ended on a
// 20,000-token budget after two rounds.
func TestTheTokenBudgetDoesNotChargeTheColdPrefixEveryRound(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		cold("t1", 1, 12900),
		cold("t2", 2, 14200),
		func() model.Step {
			s := submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"answered"}})
			s.Usage = model.Usage{InputTokens: 14600, OutputTokens: 200}
			return s
		}(),
	}}
	c := caps()
	c.MaxTokensPerQuestion = 20000
	res, err := New(m, c).Answer(context.Background(), Question{ID: 5301, Text: "how much copper have i mined", PlayerIndex: player(1)}, probeTools())
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "answered" {
		t.Errorf("artifact = %+v, want the answer: three cold rounds of a short question are not over budget", res.Artifact)
	}
}

// A question that really grows past the budget, beyond the prefix, still
// stops on it.
func TestTheTokenBudgetStillStopsAQuestionThatReallyGrows(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		cold("t1", 1, 12900),
		cold("t2", 2, 25000),
		cold("t3", 3, 36000),
		submitStep("s4", map[string]any{"shape": "summary", "lines": []string{"too late"}}),
	}}
	c := caps()
	c.MaxTokensPerQuestion = 20000
	res, err := New(m, c).Answer(context.Background(), Question{ID: 5302, Text: "q", PlayerIndex: player(1)}, probeTools())
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice || !strings.Contains(res.Artifact.Text, "token budget") {
		t.Errorf("artifact = %+v, want the token budget notice", res.Artifact)
	}
	if m.calls != 3 {
		t.Errorf("model calls = %d, want 3: the third round passes 20k of real content", m.calls)
	}
}

// The meter on its own: a warm round is charged as the route bills it, a
// cold one has the prefix taken out, never below zero.
func TestTokenMeter(t *testing.T) {
	tm := newTokenMeter(20000, 100)
	tm.add(model.Usage{InputTokens: 12900, OutputTokens: 300})
	prefix := 12900 - 100
	if tm.prefix != prefix {
		t.Fatalf("prefix = %d, want round 1's prompt less the user turn, %d", tm.prefix, prefix)
	}
	if tm.spent != 100+300 {
		t.Errorf("spent after round 1 = %d, want the user turn and the output", tm.spent)
	}
	tm.add(model.Usage{InputTokens: 1000, CacheReadTokens: 12800, OutputTokens: 100})
	if want := 400 + 1000 + 1280 + 100; tm.spent != want {
		t.Errorf("spent after a warm round = %d, want %d, charged as billed", tm.spent, want)
	}
	tm.add(model.Usage{InputTokens: 50, OutputTokens: 10})
	if want := 400 + 2380 + 10; tm.spent != want {
		t.Errorf("spent after a tiny cold round = %d, want %d: the prefix comes off fresh input only", tm.spent, want)
	}

	// A budget of zero is off.
	off := newTokenMeter(0, 0)
	off.add(model.Usage{InputTokens: 1 << 30})
	if off.over() || off.nearlyOver() {
		t.Error("a zero budget tripped")
	}
}
