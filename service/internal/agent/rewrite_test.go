// The rewrite flow: an answer over a cap goes back to the model while it has
// a round to rewrite in, and goes out cut at a word boundary only when it has
// none. Questions 37 and 38 on the live server, 2026-10-11, were cut
// mid-sentence at the old 160-character cap with no word to the model.

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// traceLog collects what Agent.Trace would have logged.
type traceLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *traceLog) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *traceLog) with(sub string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range l.lines {
		if strings.Contains(line, sub) {
			out = append(out, line)
		}
	}
	return out
}

// sentenceOf is ordinary prose exactly n runes long, words and spaces, the
// kind of line question 38 was.
func sentenceOf(n int) string {
	text := strings.Repeat("team-4 leads research by a wide margin ", n/30+2)[:n]
	if strings.HasSuffix(text, " ") {
		text = text[:n-1] + "s"
	}
	return text
}

// overlong is a summary whose second line is 410 characters, the shape of
// question 38's answer.
func overlong(id string) model.Step {
	return submitStep(id, map[string]any{"shape": "summary", "lines": []string{"Research standings:", sentenceOf(410)}})
}

// cutBetweenWords fails unless line is a cut of full: a whole-word start of
// it, ending with the ellipsis, inside the cell cap.
func cutBetweenWords(t *testing.T, line, full string) {
	t.Helper()
	kept, cut := strings.CutSuffix(line, ellipsis)
	if !cut {
		t.Fatalf("the line does not end with the ellipsis: %q", line)
	}
	if n := len([]rune(line)); n > MaxCellChars {
		t.Fatalf("the cut line is %d runes, over the %d cap", n, MaxCellChars)
	}
	if !strings.HasPrefix(full, kept) || !strings.HasPrefix(full[len(kept):], " ") {
		t.Fatalf("the cut is not between words: %q", kept)
	}
}

// While a round is left, an answer over a cap is refused back to the model,
// naming exactly what is over, and the rewrite is what the player gets.
func TestAnswerSendsAnOverlongAnswerBackWhileARoundIsLeft(t *testing.T) {
	rewrite := submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"Research standings:", "team-4 leads at 77%."}})
	m := &scriptedModel{steps: []model.Step{overlong("s1"), rewrite}}
	log := &traceLog{}
	a := New(m, caps())
	a.Trace = log.printf

	res, err := a.Answer(context.Background(), Question{ID: 3801, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Rounds != 2 || res.Artifact.Lines[1] != "team-4 leads at 77%." {
		t.Fatalf("rounds = %d, artifact = %+v, want the rewrite from round 2", res.Rounds, res.Artifact)
	}
	refusal := m.msgs[len(m.msgs)-1].Blocks[0]
	want := fmt.Sprintf("Line 2 is 410 characters, the cap is %d: rewrite it shorter or use another line, at most %d lines.", MaxCellChars, MaxSummaryLines)
	if !refusal.IsError || !strings.Contains(refusal.Content, want) || !strings.Contains(refusal.Content, "Not sent") {
		t.Errorf("the model was not told what was over: %+v", refusal)
	}
	if len(log.with("went out cut")) != 0 {
		t.Errorf("an answer within the caps was logged as cut: %q", log.lines)
	}
}

// With no round left, the same answer goes out cut between words, never
// mid-word, and nothing is refused: a refusal there would end the question
// with a notice in place of the answer.
func TestAnswerCutsAnOverlongAnswerOnTheLastRound(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{overlong("s1")}}
	c := caps()
	c.MaxRounds = 1
	log := &traceLog{}
	a := New(m, c)
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 3802, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 1 {
		t.Fatalf("artifact = %+v rounds = %d, want the summary itself, cut", res.Artifact, res.Rounds)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	if got := log.with("refused"); len(got) != 0 {
		t.Errorf("the last round refused an answer it could not get back: %q", got)
	}
	if got := log.with("question 3802: the answer was over the caps and went out cut"); len(got) != 1 {
		t.Errorf("the cut was not logged once: %q", log.lines)
	}
	if rec := findQuestion(t, dir, 3802); !rec.Clipped {
		t.Error("ledger clipped = false for an answer that went out cut")
	}
	if rounds := roundsFor(t, dir, 3802); len(rounds) != 1 || len(rounds[0].SubmitRefusals) != 0 {
		t.Errorf("round records = %+v, want one round with no refusal", rounds)
	}
}

// Over several rounds, every round but the last refuses, and the last cuts.
func TestAnswerRefusesUntilTheLastRoundThenCuts(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{overlong("s1")}}
	c := caps()
	c.MaxRounds = 3
	log := &traceLog{}
	a := New(m, c)
	a.Trace = log.printf

	res, err := a.Answer(context.Background(), Question{ID: 3803, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Rounds != 3 || m.calls != 3 {
		t.Fatalf("rounds = %d, model calls = %d, want 3 and 3", res.Rounds, m.calls)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	for _, round := range []int{1, 2} {
		if got := log.with(fmt.Sprintf("question 3803 round %d: submit_answer refused (over_cap)", round)); len(got) != 1 {
			t.Errorf("round %d refusal lines = %q", round, got)
		}
	}
	if got := log.with("question 3803 round 3: submit_answer refused"); len(got) != 0 {
		t.Errorf("the last round refused: %q", got)
	}
}

// A round that spends the token budget is a last round too: the loop ends
// once its results are in, so its answer goes out cut instead of refused.
func TestAnswerCutsWhenTheTokenBudgetLeavesNoRound(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{overlong("s1")}}
	c := caps()
	c.MaxTokensPerQuestion = 12 // one scripted round is 10 in and 2 out
	log := &traceLog{}
	a := New(m, c)
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 3804, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if res.Artifact.Shape != ShapeSummary || m.calls != 1 {
		t.Fatalf("artifact = %+v after %d calls, want the answer cut in round 1", res.Artifact, m.calls)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	if got := log.with("refused"); len(got) != 0 {
		t.Errorf("a round with the token budget spent refused an answer: %q", got)
	}
	if rounds := roundsFor(t, dir, 3804); len(rounds) != 1 || len(rounds[0].SubmitRefusals) != 0 {
		t.Errorf("round records = %+v, want no refusal", rounds)
	}
}

// A refusal never ends a question with the stalled notice when an answer is
// in hand: a model that stops without a word after a refusal still gets its
// refused answer to the player, cut.
func TestARefusedAnswerGoesOutWhenTheModelStallsAfterIt(t *testing.T) {
	stall := model.Step{
		Blocks:     []model.Block{{Type: model.BlockText, Text: "Let me shorten th"}},
		StopReason: model.StopMaxTokens,
		Usage:      model.Usage{InputTokens: 10, OutputTokens: 2},
	}
	m := &scriptedModel{steps: []model.Step{overlong("s1"), stall}}
	dir := t.TempDir()
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 3805, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 2 {
		t.Fatalf("artifact = %+v rounds = %d, want the refused answer, not the stalled notice", res.Artifact, res.Rounds)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	if rec := findQuestion(t, dir, 3805); !rec.Clipped || rec.Shape != ShapeSummary {
		t.Errorf("ledger = clipped %v shape %q, want a clipped summary", rec.Clipped, rec.Shape)
	}
}

// The same when the rounds run out on a lookup after the refusal.
func TestARefusedAnswerGoesOutWhenTheRoundsRunOut(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{overlong("s1"), toolStep("t2", "probe", map[string]any{})}}
	c := caps()
	c.MaxRounds = 2
	a := New(m, c)

	res, err := a.Answer(context.Background(), Question{ID: 3806, Text: "who leads research", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 2 {
		t.Fatalf("artifact = %+v rounds = %d, want the refused answer, not the rounds notice", res.Artifact, res.Rounds)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
}

// And when the token budget runs out on a lookup after the refusal.
func TestARefusedAnswerGoesOutWhenTheTokensRunOut(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{overlong("s1"), toolStep("t2", "probe", map[string]any{})}}
	c := caps()
	c.MaxTokensPerQuestion = 20 // round 1 spends 12, round 2 passes it
	a := New(m, c)

	res, err := a.Answer(context.Background(), Question{ID: 3807, Text: "who leads research", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary || m.calls != 2 {
		t.Fatalf("artifact = %+v after %d calls, want the refused answer, not the token notice", res.Artifact, m.calls)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
}

// On a last round with two answers, one over a cap and one within, the one
// within goes out whole: cutting is for when nothing better was sent.
func TestAnAnswerWithinTheCapsBeatsOneThatNeedsCutting(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{round(
		use("s1", SubmitTool, map[string]any{"shape": "summary", "lines": []string{sentenceOf(410)}}),
		use("s2", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"team-4 leads at 77%."}}),
	)}}
	c := caps()
	c.MaxRounds = 1
	a := New(m, c)

	res, err := a.Answer(context.Background(), Question{ID: 3808, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "team-4 leads at 77%." {
		t.Fatalf("artifact = %+v, want the answer within the caps", res.Artifact)
	}
}

// Every refused submit_answer is one log line, with the question, the round,
// the kind and the reason the model read, and the same in that round's
// ledger record. An upstream once looped on refused answers until its rounds
// ran out, and neither said why.
func TestEverySubmitRefusalIsLoggedAndLedgered(t *testing.T) {
	dir := t.TempDir()
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	good := map[string]any{"shape": "summary", "lines": []string{"done"}}
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "chart", "lines": []string{"x"}}),
		round(use("s2", SubmitTool, good), use("t2", "probe", map[string]any{})),
		overlong("s3"),
		round(use("s4", SubmitTool, good), use("s5", SubmitTool, good)),
	}}
	log := &traceLog{}
	a := New(m, caps())
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 41, Text: "q", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rounds := roundsFor(t, dir, 41)
	if len(rounds) != 4 {
		t.Fatalf("round records = %d, want 4", len(rounds))
	}
	for i, want := range []struct{ kind, says string }{
		{ledger.RefusedInvalid, `shape "chart" is not a shape`},
		{ledger.RefusedBesideReads, "cannot run in the same round as other tools"},
		{ledger.RefusedOverCap, "Line 2 is 410 characters"},
		{ledger.RefusedSecondSubmit, "only the first submit_answer of a round is used"},
	} {
		round := i + 1
		got := rounds[i].SubmitRefusals
		if len(got) != 1 || got[0].Kind != want.kind || !strings.Contains(got[0].Reason, want.says) {
			t.Errorf("round %d submit_refusals = %+v, want one %s saying %q", round, got, want.kind, want.says)
		}
		prefix := fmt.Sprintf("question 41 round %d: submit_answer refused (%s): ", round, want.kind)
		if lines := log.with(prefix); len(lines) != 1 || !strings.Contains(lines[0], want.says) {
			t.Errorf("round %d log = %q, want one line %q... saying %q", round, lines, prefix, want.says)
		}
	}
	if n := len(log.with("submit_answer refused")); n != 4 {
		t.Errorf("logged %d refusals, want 4: %q", n, log.lines)
	}
}

// A submit_answer in a round the lookup cap refused is refused with it, and
// says so in the log and the ledger like any other.
func TestALookupCapRefusalOfASubmitIsLoggedAndLedgered(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{
		round(use("t1", "probe", map[string]any{}), use("t2", "probe", map[string]any{}), use("s1", SubmitTool, map[string]any{"shape": "summary", "lines": []string{"early"}})),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	c := caps()
	c.MaxToolCalls = 1
	log := &traceLog{}
	a := New(m, c)
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 42, Text: "q", PlayerIndex: player(1)}, []tools.Tool{stubTool("probe", `{}`, nil)}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rounds := roundsFor(t, dir, 42)
	if len(rounds) != 2 {
		t.Fatalf("round records = %d, want 2", len(rounds))
	}
	if got := rounds[0].SubmitRefusals; len(got) != 1 || got[0].Kind != ledger.RefusedLookupCap || !strings.Contains(got[0].Reason, "only 1 remain") {
		t.Errorf("round 1 submit_refusals = %+v, want one lookup_cap refusal", got)
	}
	if len(rounds[1].SubmitRefusals) != 0 {
		t.Errorf("round 2 accepted its answer but recorded refusals: %+v", rounds[1].SubmitRefusals)
	}
	if lines := log.with("question 42 round 1: submit_answer refused (lookup_cap)"); len(lines) != 1 {
		t.Errorf("lookup_cap refusal log = %q", log.lines)
	}
}

// failsAfter plays its steps in order, then fails every call after them: a
// model that answered, then went away.
type failsAfter struct{ scriptedModel }

func (m *failsAfter) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	if m.calls >= len(m.steps) {
		m.calls++
		return model.Step{}, errors.New("upstream timed out")
	}
	return m.scriptedModel.Step(ctx, system, msgs, defs)
}

// A model that fails after an answer was refused for its length has still
// answered: that answer goes out cut, through finish, and the asker's slot
// stays spent because the asker got an answer.
func TestAModelErrorAfterARefusalSendsTheRefusedAnswer(t *testing.T) {
	dir := t.TempDir()
	m := &failsAfter{scriptedModel{steps: []model.Step{overlong("s1")}}}
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	log := &traceLog{}
	a := New(m, c)
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 3809, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v, want the refused answer instead of the error", err)
	}
	a.Ledger.Close()
	if res.Artifact.Shape != ShapeSummary || res.Rounds != 2 {
		t.Fatalf("artifact = %+v rounds = %d, want the refused summary in round 2", res.Artifact, res.Rounds)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	if rec := findQuestion(t, dir, 3809); !rec.Clipped || rec.Shape != ShapeSummary {
		t.Errorf("ledger = clipped %v shape %q, want a clipped summary", rec.Clipped, rec.Shape)
	}
	if got := log.with("question 3809 round 2: the model failed before a rewrite came back"); len(got) != 1 {
		t.Errorf("the failure was not logged once: %q", log.lines)
	}
	next, _ := a.Answer(context.Background(), Question{ID: 3810, Text: "again", PlayerIndex: player(1)}, nil)
	if next.Artifact.Shape != ShapeNotice || !strings.Contains(next.Artifact.Text, "allowance") {
		t.Errorf("the next question = %+v, want the quota notice: an answered question keeps its slot", next.Artifact)
	}
}

// Rounds paid for before a model error count toward the day's budget, answer
// or no answer.
func TestPaidRoundsBeforeAModelErrorCountTowardTheDailyBudget(t *testing.T) {
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	paid := toolStep("t1", "probe", map[string]any{})
	paid.Usage.Cost = 0.02
	for _, tc := range []struct {
		name  string
		steps []model.Step
	}{
		{"no answer in hand", []model.Step{paid}},
		{"a refused answer in hand", []model.Step{paid, overlong("s2")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := caps()
			c.MaxCostPerDay = 0.01
			a := New(&failsAfter{scriptedModel{steps: tc.steps}}, c)
			_, _ = a.Answer(context.Background(), Question{ID: 1, Text: "q", PlayerIndex: player(1)}, ts)
			next, _ := a.Answer(context.Background(), Question{ID: 2, Text: "q", PlayerIndex: player(2)}, ts)
			if !strings.Contains(next.Artifact.Text, "daily allowance") {
				t.Errorf("the next question = %+v, want the budget notice: $0.02 was spent against a $0.01 day", next.Artifact)
			}
		})
	}
}

// Prose after a refusal is the model talking about its rewrite, not an
// answer: the refused answer goes out cut instead.
func TestProseAfterARefusalSendsTheRefusedAnswer(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{overlong("s1"), textStep("I will shorten that and send it again.")}}
	a := New(m, caps())

	res, err := a.Answer(context.Background(), Question{ID: 3811, Text: "who leads research", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeSummary || len(res.Artifact.Lines) != 2 || res.Rounds != 2 {
		t.Fatalf("artifact = %+v rounds = %d, want the refused two-line answer, not the prose", res.Artifact, res.Rounds)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
}

// The model is sent back at most twice for an answer over a cap, counting
// only the rounds that refused one; the third such answer goes out cut, with
// rounds to spare.
func TestAnOverlongAnswerIsRefusedAtMostTwice(t *testing.T) {
	dir := t.TempDir()
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{overlong("s1"), toolStep("t2", "probe", map[string]any{}), overlong("s3"), overlong("s4")}}
	log := &traceLog{}
	a := New(m, caps()) // six rounds
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 3812, Text: "who leads research", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if res.Rounds != 4 || m.calls != 4 {
		t.Fatalf("rounds = %d, model calls = %d, want 4: two refusals, a lookup, then the cut", res.Rounds, m.calls)
	}
	cutBetweenWords(t, res.Artifact.Lines[1], sentenceOf(410))
	if n := len(log.with("submit_answer refused (over_cap)")); n != 2 {
		t.Errorf("%d over-cap refusals logged, want 2: %q", n, log.lines)
	}
	if rounds := roundsFor(t, dir, 3812); len(rounds) != 4 || len(rounds[3].SubmitRefusals) != 0 {
		t.Errorf("round records = %+v, want the fourth round to take the answer", rounds)
	}
}
