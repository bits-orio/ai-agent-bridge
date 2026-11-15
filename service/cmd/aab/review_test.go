package main

// The answer loop's review fixes: a deadline per question (S2), a panic
// recovered into a notice (S3), shutdown that runs nothing more and still
// delivers what is paid for (S8), a cap on re-runs after outages (S9), asker
// mode scoped to the asker (S4) and player text kept out of the log raw
// (S13).

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// scriptModel is a model whose every step runs step, counting calls.
type scriptModel struct {
	calls atomic.Int32
	step  func(ctx context.Context) (model.Step, error)
}

func (m *scriptModel) Name() string { return "script" }

func (m *scriptModel) Step(ctx context.Context, _ string, _ []model.Message, _ []model.ToolDef) (model.Step, error) {
	m.calls.Add(1)
	return m.step(ctx)
}

// answers is a step that ends the question with a plain text answer.
func answers(text string) func(context.Context) (model.Step, error) {
	return func(context.Context) (model.Step, error) {
		return model.Step{Blocks: []model.Block{{Type: model.BlockText, Text: text}}, StopReason: model.StopEndTurn}, nil
	}
}

// runnerWith is newTestRunner with the agent on m and the config's question
// timeout set.
func runnerWith(t *testing.T, companion *fakeCompanion, m model.Model, timeout time.Duration) *runner {
	t.Helper()
	r := newTestRunner(t, companion)
	r.cfg = &config.Config{Agent: config.AgentConfig{QuestionTimeout: config.Duration(timeout)}}
	r.agent = agent.New(m, agent.Caps{MaxRounds: 2})
	return r
}

// S2: one slow question has a deadline. The asker hears that it took too
// long, the question is finished with, never run again, and the deadline is
// not read as the companion going out of reach.
func TestAQuestionPastItsDeadlineEndsWithANotice(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "something slow"})
	m := &scriptModel{step: func(ctx context.Context) (model.Step, error) {
		<-ctx.Done()
		return model.Step{}, ctx.Err()
	}}
	r := runnerWith(t, companion, m, 20*time.Millisecond)
	ctx := context.Background()

	r.tick(ctx)
	if got := companion.delivered[1]; !strings.Contains(got, questionTimeoutNotice) {
		t.Fatalf("delivered %q, want the timeout notice", got)
	}
	if r.lost {
		t.Error("a question's own deadline put the companion out of reach")
	}
	r.tick(ctx)
	if n := m.calls.Load(); n != 1 {
		t.Errorf("model called %d times, want once: a timed-out question is finished with", n)
	}
	if r.after != 1 {
		t.Errorf("poll cursor = %d, want 1", r.after)
	}
}

// S3: a question that panics is answered with a notice and finished with:
// a crash loop would run it, and bill it, again on every restart.
func TestAPanickingQuestionIsAnsweredWithANoticeAndNeverRunAgain(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "poison", 2: "ping"})
	m := &scriptModel{step: func(context.Context) (model.Step, error) { panic("boom in the model") }}
	r := runnerWith(t, companion, m, time.Minute)
	ctx := context.Background()

	r.tick(ctx) // must not panic
	if got := companion.delivered[1]; !strings.Contains(got, panicNotice) {
		t.Errorf("question 1 got %q, want the panic notice", got)
	}
	r.tick(ctx)
	if n := m.calls.Load(); n != 2 {
		t.Errorf("model called %d times, want twice: once per question, never again", n)
	}
	if !strings.Contains(log.String(), "boom in the model") || !strings.Contains(log.String(), "goroutine") {
		t.Errorf("the panic was not logged with its stack:\n%s", log.String())
	}
}

// S8: once the service is told to stop, the rest of the page is not run (no
// model call, no ledger row), and an answer already paid for still goes out.
func TestShutdownRunsNoMoreQuestionsAndDeliversThePaidAnswer(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "first", 2: "second"})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	m := &scriptModel{}
	m.step = func(context.Context) (model.Step, error) {
		stop() // SIGTERM lands while the first answer is being written
		return answers("the first answer")(ctx)
	}
	r := runnerWith(t, companion, m, time.Minute)

	r.tick(ctx)
	if n := m.calls.Load(); n != 1 {
		t.Errorf("model called %d times after shutdown began, want once", n)
	}
	if got := companion.delivered[1]; !strings.Contains(got, "the first answer") {
		t.Errorf("the paid answer was dropped: delivered %q", got)
	}
	if _, ran := companion.delivered[2]; ran {
		t.Error("question 2 was answered after shutdown began")
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1", r.answered)
	}
}

// S8: a question cut off by shutdown mid-run is left for the next start:
// no failure notice reaches the asker, and nothing is held.
func TestAQuestionCutOffByShutdownIsLeftUnanswered(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "first"})
	ctx, stop := context.WithCancel(context.Background())
	m := &scriptModel{step: func(c context.Context) (model.Step, error) {
		stop()
		<-c.Done()
		return model.Step{}, c.Err()
	}}
	r := runnerWith(t, companion, m, time.Minute)
	r.tick(ctx)
	if _, sent := companion.delivered[1]; sent {
		t.Errorf("a question cut off by shutdown was answered: %q", companion.delivered[1])
	}
	if len(r.inFlight) != 0 || r.answered != 0 {
		t.Errorf("inFlight = %d, answered = %d, want nothing held or counted", len(r.inFlight), r.answered)
	}
}

// S9: each outage starts the service again and drops what it held, so a
// question that never lands runs again. Two re-runs, then a notice.
func TestReRunsAfterOutagesAreCappedAtTwo(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "never lands"})
	companion.answerErr = "too_large"
	m := &scriptModel{step: answers("an answer")}
	r := runnerWith(t, companion, m, time.Minute)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r.tick(ctx)
		r.reset() // what startAgain does after an outage
	}
	companion.answerErr = ""
	r.tick(ctx)
	if n := m.calls.Load(); n != 3 {
		t.Errorf("model called %d times, want 3: the first run and two re-runs", n)
	}
	if got := companion.delivered[1]; !strings.Contains(got, reRunNotice) {
		t.Errorf("delivered %q, want the re-run notice", got)
	}
}

// S4: in asker mode the question is private to its asker, so its session
// and its history reads are that asker's alone.
func TestAnAskerModeQuestionIsScopedToItsAsker(t *testing.T) {
	q := agentQuestion(rpc.Question{ID: 1, Text: "t", PlayerName: "Bob", Scope: "global", ToAsker: true})
	if q.Scope != "asker:Bob" || !q.Private {
		t.Errorf("asker-mode question scope = %q private %v, want asker:Bob, private", q.Scope, q.Private)
	}
	q = agentQuestion(rpc.Question{ID: 1, Text: "t", PlayerName: "Bob", Scope: "global"})
	if q.Scope != "global" || q.Private {
		t.Errorf("a plain global question became %q private %v", q.Scope, q.Private)
	}
}

// S13: player text reaches the log with its control characters escaped, so
// a question cannot forge a log line or drive the terminal.
func TestPlayerTextReachesTheLogEscaped(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "hi\nanswer 9 artifact {} \x1b[31mred"})
	r := newTestRunner(t, companion)
	r.tick(context.Background())
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.HasPrefix(l, "answer 9") {
			t.Errorf("a question forged a log line: %q", l)
		}
	}
	if strings.Contains(log.String(), "\x1b") {
		t.Errorf("an escape code reached the log: %q", log.String())
	}
}
