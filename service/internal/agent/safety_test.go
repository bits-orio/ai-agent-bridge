package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func submitModel(lines ...string) *scriptedModel {
	return &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": lines})}}
}

// The whole server has an hourly cap on top of each player's, and a
// refused question is a notice the asker alone sees.
func TestServerWideQuota(t *testing.T) {
	m := submitModel("ok")
	c := caps()
	c.QuestionsPerHour = 2
	a := New(m, c)
	for i := 1; i <= 2; i++ {
		res, _ := a.Answer(context.Background(), Question{ID: int64(i), Text: "q", PlayerIndex: player(i)}, nil)
		if res.Artifact.Shape != ShapeSummary {
			t.Fatalf("question %d should be answered: %+v", i, res.Artifact)
		}
	}
	third, _ := a.Answer(context.Background(), Question{ID: 3, Text: "q", PlayerIndex: player(3)}, nil)
	if third.Artifact.Shape != ShapeNotice || !third.Artifact.ToAsker || !strings.Contains(third.Artifact.Text, "server has asked") {
		t.Errorf("third question should be refused to the asker alone: %+v", third.Artifact)
	}
	if m.calls != 2 {
		t.Errorf("the model was called %d times, want 2", m.calls)
	}
	// The refused player's own slot went back: they are not charged for a
	// question the server refused.
	if a.quota.take("player:3", time.Now()) != true {
		t.Error("the refused asker lost a personal slot")
	}
}

// The daily budget stops the model once the rolling day's spend reaches it.
func TestDailyBudget(t *testing.T) {
	m := submitModel("ok")
	c := caps()
	c.MaxCostPerDay = 0.01
	a := New(m, c)
	now := time.Now()
	a.now = func() time.Time { return now }
	a.budget.spend(0.009, now)
	if res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "q", PlayerIndex: player(1)}, nil); res.Artifact.Shape != ShapeSummary {
		t.Fatalf("under budget must answer: %+v", res.Artifact)
	}
	a.budget.spend(0.002, now)
	if res, _ := a.Answer(context.Background(), Question{ID: 2, Text: "q", PlayerIndex: player(1)}, nil); !res.Artifact.ToAsker || !strings.Contains(res.Artifact.Text, "daily allowance") {
		t.Errorf("over budget must refuse to the asker: %+v", res.Artifact)
	}
	now = now.Add(25 * time.Hour)
	if res, _ := a.Answer(context.Background(), Question{ID: 3, Text: "q", PlayerIndex: player(1)}, nil); res.Artifact.Shape != ShapeSummary {
		t.Errorf("a day later the budget is back: %+v", res.Artifact)
	}
	if b := newBudget(0); b.exhausted(now) {
		t.Error("no limit must never be exhausted")
	}
}

// One question may make only so many lookups across its rounds. A round that
// would pass the cap is refused and the model told what is left, so it can
// still answer from what it has; a submission on its own is never blocked.
func TestToolCallCap(t *testing.T) {
	call := func(id string) model.Step {
		return model.Step{StopReason: model.StopToolUse, Blocks: []model.Block{
			{Type: model.BlockToolUse, ID: id + "a", Name: "nothing", Input: json.RawMessage(`{}`)},
			{Type: model.BlockToolUse, ID: id + "b", Name: "nothing", Input: json.RawMessage(`{}`)},
		}}
	}
	late := submitStep("t", map[string]any{"shape": "summary", "lines": []string{"late"}})
	c := caps()
	c.MaxToolCalls = 4

	m := &scriptedModel{steps: []model.Step{call("1"), call("2"), call("3"), late}}
	res, err := New(m, c).Answer(context.Background(), Question{ID: 1, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 4 || m.calls != 4 {
		t.Errorf("the third round is refused and the fourth still answers: %+v rounds=%d calls=%d", res.Artifact, res.Rounds, m.calls)
	}
	last := m.msgs[len(m.msgs)-1]
	if last.Role != model.RoleUser || len(last.Blocks) != 2 || !last.Blocks[0].IsError || !strings.Contains(last.Blocks[0].Content, "no lookups remain for this question (4 of 4 used)") {
		t.Errorf("the model was not told the round was refused: %+v", last)
	}

	m2 := &scriptedModel{steps: []model.Step{call("1"), call("2"), late}}
	res, err = New(m2, c).Answer(context.Background(), Question{ID: 2, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 3 {
		t.Errorf("a submission alone passes a used-up cap: %+v rounds=%d", res.Artifact, res.Rounds)
	}

	refusal := refuseLookups([]model.Block{{ID: "x"}}, 20, 3, 9, 12)
	if len(refusal) != 1 || !refusal[0].IsError || !strings.Contains(refusal[0].Content, "asked for 20 lookups but only 3 remain") || !strings.Contains(refusal[0].Content, "9 of 12 used") {
		t.Errorf("refusal = %+v", refusal)
	}
	if none := refuseLookups([]model.Block{{ID: "y"}}, 1, 0, 12, 12); !strings.Contains(none[0].Content, "no lookups remain") {
		t.Errorf("used-up refusal = %+v", none)
	}
}

// A refusal encodes as to_asker so the companion prints it privately; an
// ordinary answer does not carry the field.
func TestRefusalEncodesToAsker(t *testing.T) {
	raw, _ := json.Marshal(refusal("no"))
	if !strings.Contains(string(raw), `"to_asker":true`) {
		t.Errorf("refusal lacks to_asker: %s", raw)
	}
	plain, _ := json.Marshal(Notice(LevelWarning, "no"))
	if strings.Contains(string(plain), "to_asker") {
		t.Errorf("a plain notice must not carry to_asker: %s", plain)
	}
	var back Artifact
	if err := json.Unmarshal(raw, &back); err != nil || !back.ToAsker {
		t.Errorf("to_asker does not round-trip: %+v %v", back, err)
	}
	if kept, _ := validate(refusal("no")); !kept.ToAsker {
		t.Error("validate dropped to_asker")
	}
}

// The live session map is capped; the longest idle goes first.
func TestSessionsAreCapped(t *testing.T) {
	s := newSessions(SessionCaps{})
	for i := 0; i < maxLiveSessions+10; i++ {
		s.open("global", strings.Repeat("a", 1)+string(rune('a'+i%26))+strings.Repeat("b", i/26), t0.Add(time.Duration(i)*time.Second), false)
	}
	if n := len(s.byKey); n != maxLiveSessions {
		t.Errorf("live sessions = %d, want %d", n, maxLiveSessions)
	}
	if _, fresh := s.open("global", "ab", t0.Add(time.Hour), false); !fresh {
		t.Error("the oldest session should have been trimmed")
	}
}
