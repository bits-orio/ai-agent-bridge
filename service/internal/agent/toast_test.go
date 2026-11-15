package agent

import (
	"context"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// A toast asks the model for one line. It spends no player's or the server's
// hourly quota, is never part of a session, reads no briefing, and its
// ledger record says it was a toast.
func TestToastQuestionStaysOutOfSessionsAndQuotas(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"Up it goes!"}})}}
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	c.QuestionsPerHour = 1
	a := New(m, c)
	a.BriefingEnabled = true
	dir := t.TempDir()
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 0, Force: "team-1", Text: "Write one line: team-1 launched its first rocket.", Toast: "first_rocket"}, nil)
	if err != nil || res.Artifact.Line() != "Up it goes!" {
		t.Fatalf("toast = %+v, %v", res.Artifact, err)
	}
	if res.exchange != nil {
		t.Error("a toast recorded a session exchange")
	}
	if rows := a.sess.list(Question{Force: "team-1"}.pool(), a.now()); len(rows) != 0 {
		t.Errorf("sessions after a toast: %+v", rows)
	}
	if !a.quota.take("force:team-1", a.now()) || !a.server.take(serverKey, a.now()) {
		t.Error("a toast spent an hourly quota")
	}
	a.Ledger.Close()
	if rec := findQuestion(t, dir, 0); rec.Toast != "first_rocket" || rec.Briefing != ledger.BriefingOff {
		t.Errorf("ledger record %+v, want toast first_rocket with the briefing off", rec)
	}
}

// The day's budget still applies: a toast is paid for.
func TestToastRespectsTheDailyBudget(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"x"}})}}
	c := caps()
	c.MaxCostPerDay = 0.01
	a := New(m, c)
	a.budget.spend(1, a.now())
	res, _ := a.Answer(context.Background(), Question{Force: "team-1", Text: "Write one line", Toast: "rockets"}, nil)
	if res.Artifact.Shape != ShapeNotice || m.calls != 0 {
		t.Errorf("toast over budget = %+v after %d model calls", res.Artifact, m.calls)
	}
}
