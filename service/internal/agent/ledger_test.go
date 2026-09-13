// Every ending of Answer writes exactly one QuestionRecord
// (docs/design/phase4-observability-spec.md sections 2-4); a question that
// produces no ledger line is a bug. These tests drive each of the nine
// endings (agent.go's own return statements) through a real ledger.Writer
// over a temp dir, then read the files back with ledger.Read, the same
// real-temp-file idiom writer_test.go already uses.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

func findQuestion(t *testing.T, dir string, id int64) ledger.QuestionRecord {
	t.Helper()
	recs, err := ledger.Read(dir)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	for _, q := range recs.Questions {
		if q.QuestionID == id {
			return q
		}
	}
	t.Fatalf("no QuestionRecord for question %d among %d written", id, len(recs.Questions))
	return ledger.QuestionRecord{}
}

func roundsFor(t *testing.T, dir string, id int64) []ledger.RoundRecord {
	t.Helper()
	recs, err := ledger.Read(dir)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	var out []ledger.RoundRecord
	for _, r := range recs.Rounds {
		if r.QuestionID == id {
			out = append(out, r)
		}
	}
	return out
}

// submitStepCost is submitStep with an explicit reported Usage.Cost, for the
// one test that needs a question to actually cost something (CostUSD trusts
// a route-reported Usage.Cost over the price table).
func submitStepCost(id string, artifact map[string]any, cost float64) model.Step {
	s := submitStep(id, artifact)
	s.Usage.Cost = cost
	return s
}

// 1: the grammar command's own refusal, an empty question.
func TestLedgerWritesEmptyQuestionRefusal(t *testing.T) {
	dir := t.TempDir()
	a := New(fake.New(), caps())
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 101, Text: "", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Shape != ShapeNotice {
		t.Fatalf("artifact = %+v, want a notice", res.Artifact)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 101)
	if rec.Rounds != 0 || rec.Lookups != 0 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 0/0/false: a refusal never ran the model", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if !rec.Refused || rec.RefusedReason == nil || *rec.RefusedReason != emptyQuestionNotice {
		t.Errorf("refused/refused_reason = %v/%v, want true/%q", rec.Refused, rec.RefusedReason, emptyQuestionNotice)
	}
}

// The grammar command's non-refusal branches must not come back refused.
func TestLedgerCommandGrammarIsNotARefusal(t *testing.T) {
	dir := t.TempDir()
	a := New(fake.New(), caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 102, Text: "sessions", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 102)
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil for the sessions command", rec.Refused, rec.RefusedReason)
	}
}

// 2: the per-player hourly quota.
func TestLedgerWritesQuotaRefusal(t *testing.T) {
	dir := t.TempDir()
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)
	q := Question{Text: "again", PlayerIndex: player(9)}

	q.ID = 201
	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	q.ID = 202
	if _, err := a.Answer(context.Background(), q, nil); err != nil {
		t.Fatalf("second answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 202)
	if rec.Rounds != 0 || rec.Lookups != 0 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 0/0/false: a refusal never ran the model", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if !rec.Refused || rec.RefusedReason == nil || *rec.RefusedReason != quotaNotice {
		t.Errorf("refused/refused_reason = %v/%v, want true/%q", rec.Refused, rec.RefusedReason, quotaNotice)
	}
}

// 3: the whole-server hourly quota, hit by a second asker when the player
// quota itself would still allow them.
func TestLedgerWritesServerQuotaRefusal(t *testing.T) {
	dir := t.TempDir()
	c := caps()
	c.QuestionsPerHour = 1
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 301, Text: "first", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if _, err := a.Answer(context.Background(), Question{ID: 302, Text: "second", PlayerIndex: player(2)}, nil); err != nil {
		t.Fatalf("second answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 302)
	if rec.Rounds != 0 || rec.Lookups != 0 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 0/0/false: a refusal never ran the model", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if !rec.Refused || rec.RefusedReason == nil || *rec.RefusedReason != serverQuotaNotice {
		t.Errorf("refused/refused_reason = %v/%v, want true/%q", rec.Refused, rec.RefusedReason, serverQuotaNotice)
	}
}

// 4: the daily cost budget, exhausted by a first question that actually cost
// something.
func TestLedgerWritesBudgetRefusal(t *testing.T) {
	dir := t.TempDir()
	c := caps()
	c.MaxCostPerDay = 0.001
	m := &scriptedModel{steps: []model.Step{submitStepCost("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}}, 1.0)}}
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 401, Text: "first", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if _, err := a.Answer(context.Background(), Question{ID: 402, Text: "second", PlayerIndex: player(2)}, nil); err != nil {
		t.Fatalf("second answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 402)
	if rec.Rounds != 0 || rec.Lookups != 0 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 0/0/false: a refusal never ran the model", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if !rec.Refused || rec.RefusedReason == nil || *rec.RefusedReason != budgetNotice {
		t.Errorf("refused/refused_reason = %v/%v, want true/%q", rec.Refused, rec.RefusedReason, budgetNotice)
	}
}

// 5: the model itself failing. The runner (cmd/aab/answer.go) turns this
// into a failure notice before it ever reaches the player, so the ledger
// records that shape and carries the failure text in model_error. This is
// still not a "refusal" the way the spec's own refused field defines it: the
// model was asked, and it failed, rather than the question being turned away
// before or instead of asking it.
func TestLedgerWritesModelErrorQuestion(t *testing.T) {
	dir := t.TempDir()
	a := New(&failingModel{}, caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 501, Text: "x", PlayerIndex: player(1)}, nil); err == nil {
		t.Fatal("expected the model error to surface")
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 501)
	// A provider outage ran the model and still answered nothing: zero_lookup
	// means "answered from the briefing alone", and this is not that, even
	// though rounds is nonzero and lookups is zero.
	if rec.Rounds != 1 || rec.Lookups != 0 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 1/0/false", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice: the runner delivers a failure notice on this path", rec.Shape)
	}
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil: a model error is not a refusal", rec.Refused, rec.RefusedReason)
	}
	if rec.ModelError == nil || !strings.Contains(*rec.ModelError, "no route to host") {
		t.Errorf("model_error = %v, want it to carry the model's own failure text", rec.ModelError)
	}
}

// zero_lookup's other false-positive: every round refused by the lookup cap.
// The model runs on every round and the loop still answers nothing itself,
// so the warning notice the player sees is the loop talking about being out
// of rounds, not the model answering from the briefing.
func TestLedgerZeroLookupFalseWhenCapRefusesEveryRound(t *testing.T) {
	dir := t.TempDir()
	twoReads := model.Step{StopReason: model.StopToolUse, Blocks: []model.Block{
		{Type: model.BlockToolUse, ID: "a", Name: "nothing", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "b", Name: "nothing", Input: json.RawMessage(`{}`)},
	}}
	m := &scriptedModel{steps: []model.Step{twoReads}}
	c := caps()
	c.MaxToolCalls = 1 // the model always asks for two, so every round is refused and toolCalls never advances
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 1401, Text: "loop forever", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	if res.Artifact.Shape != ShapeNotice || res.Artifact.Level != LevelWarning {
		t.Fatalf("artifact = %+v, want the rounds-exhausted warning notice", res.Artifact)
	}
	rec := findQuestion(t, dir, 1401)
	if rec.Rounds != c.MaxRounds || rec.Lookups != 0 {
		t.Fatalf("rounds/lookups = %d/%d, want %d/0: every round refused, none ever ran", rec.Rounds, rec.Lookups, c.MaxRounds)
	}
	if rec.ZeroLookup {
		t.Error("zero_lookup = true, want false: the cap refused every round, the model never answered from the briefing")
	}
}

// 6: a normal answer, a model that stops talking without calling a tool.
func TestLedgerWritesNormalAnswer(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{textStep("Two forces are playing.")}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 601, Text: "forces", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 601)
	if rec.Rounds != 1 || rec.Lookups != 0 || !rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 1/0/true", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeSummary {
		t.Errorf("shape = %q, want summary", rec.Shape)
	}
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil", rec.Refused, rec.RefusedReason)
	}

	rounds := roundsFor(t, dir, 601)
	if len(rounds) != 1 {
		t.Fatalf("round records = %d, want 1", len(rounds))
	}
	if len(rounds[0].ToolCalls) != 0 {
		t.Errorf("tool_calls = %+v, want none for a round with no tool call", rounds[0].ToolCalls)
	}
}

// 7: submit_answer, with a real lookup the round before it.
func TestLedgerWritesSubmission(t *testing.T) {
	dir := t.TempDir()
	ts := []tools.Tool{stubTool("ai-agent-bridge-tools__list_forces", `{"forces":[{"name":"player","player_count":2}]}`, nil)}
	a := New(fake.New(), caps())
	a.Ledger = ledger.Open(dir, true)
	floorCalls := 0
	a.Floor = func() time.Duration { floorCalls++; return 0 }

	res, err := a.Answer(context.Background(), Question{ID: 701, Text: "what forces are there", PlayerIndex: player(1)}, ts)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 701)
	if rec.Rounds != 2 || rec.Lookups != 1 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 2/1/false", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeSummary || rec.Shape != res.Artifact.Shape {
		t.Errorf("shape = %q, want %q (the delivered artifact's own shape)", rec.Shape, res.Artifact.Shape)
	}
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil", rec.Refused, rec.RefusedReason)
	}

	rounds := roundsFor(t, dir, 701)
	if len(rounds) != 2 {
		t.Fatalf("round records = %d, want 2", len(rounds))
	}
	if len(rounds[0].ToolCalls) != 1 {
		t.Fatalf("round 1 tool_calls = %+v, want exactly one", rounds[0].ToolCalls)
	}
	tc := rounds[0].ToolCalls[0]
	if tc.Name != "ai-agent-bridge-tools__list_forces" {
		t.Errorf("name = %q", tc.Name)
	}
	if tc.Args == "" {
		t.Errorf("args not recorded")
	}
	if !tc.OK || tc.Error != nil {
		t.Errorf("ok/error = %v/%v, want ok with no error", tc.OK, tc.Error)
	}
	if tc.ResultBytes == 0 {
		t.Errorf("result_bytes = 0, want the tool reply's size")
	}
	if tc.Path != "" {
		t.Errorf("path = %q, want empty: find_item's rung is a future feature", tc.Path)
	}
	if len(rounds[1].ToolCalls) != 0 {
		t.Errorf("round 2 (the submit round) tool_calls = %+v, want none", rounds[1].ToolCalls)
	}
	if floorCalls == 0 {
		t.Error("Floor was never called for the one real tool call")
	}
}

// An unknown tool still produces a ledger entry, marked failed.
func TestLedgerRecordsAFailedToolCall(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{
		toolStep("t1", "not_a_tool", map[string]any{}),
		submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 801, Text: "hello", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rounds := roundsFor(t, dir, 801)
	if len(rounds) == 0 || len(rounds[0].ToolCalls) != 1 {
		t.Fatalf("round records = %+v", rounds)
	}
	tc := rounds[0].ToolCalls[0]
	if tc.OK {
		t.Error("ok = true, want false for an unknown tool")
	}
	if tc.Error == nil || !strings.Contains(*tc.Error, "not_a_tool") {
		t.Errorf("error = %v, want it to name the tool", tc.Error)
	}
}

// 8: the per-question token budget.
func TestLedgerWritesTokenBudgetNotice(t *testing.T) {
	dir := t.TempDir()
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{toolStep("t1", "probe", map[string]any{})}}
	c := caps()
	c.MaxTokensPerQuestion = 20 // two rounds of 12 tokens each trips it
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 901, Text: "spend", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 901)
	if rec.Rounds != 2 || rec.Lookups != 2 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 2/2/false", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil: the token budget is not a refusal", rec.Refused, rec.RefusedReason)
	}
}

// 9: rounds exhausted.
func TestLedgerWritesRoundsNotice(t *testing.T) {
	dir := t.TempDir()
	ts := []tools.Tool{stubTool("probe", `{"ok":true}`, nil)}
	m := &scriptedModel{steps: []model.Step{toolStep("t1", "probe", map[string]any{})}}
	c := caps()
	c.MaxRounds = 3
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 1001, Text: "loop forever", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 1001)
	if rec.Rounds != 3 || rec.Lookups != 3 || rec.ZeroLookup {
		t.Errorf("rounds/lookups/zero_lookup = %d/%d/%v, want 3/3/false", rec.Rounds, rec.Lookups, rec.ZeroLookup)
	}
	if rec.Shape != ShapeNotice {
		t.Errorf("shape = %q, want notice", rec.Shape)
	}
	if rec.Refused || rec.RefusedReason != nil {
		t.Errorf("refused/refused_reason = %v/%v, want false/nil: running out of rounds is not a refusal", rec.Refused, rec.RefusedReason)
	}

	if rounds := roundsFor(t, dir, 1001); len(rounds) != 3 {
		t.Errorf("round records = %d, want 3", len(rounds))
	}
}

// F4 (fixes.md): a round the lookup cap refuses still records every call it
// refused, so a repeat like "current_research once per force" is not lost
// the moment the cap catches it.
func TestLedgerRecordsCapRefusedRoundCalls(t *testing.T) {
	dir := t.TempDir()
	call := func(id string) model.Step {
		return model.Step{StopReason: model.StopToolUse, Blocks: []model.Block{
			{Type: model.BlockToolUse, ID: id + "a", Name: "current_research", Input: json.RawMessage(`{"force":"north"}`)},
			{Type: model.BlockToolUse, ID: id + "b", Name: "current_research", Input: json.RawMessage(`{"force":"south"}`)},
		}}
	}
	late := submitStep("t3", map[string]any{"shape": "summary", "lines": []string{"late"}})
	c := caps()
	c.MaxToolCalls = 2 // the first round uses the whole cap, so the second is refused outright
	ts := []tools.Tool{stubTool("current_research", `{"turns":10}`, nil)}
	m := &scriptedModel{steps: []model.Step{call("1"), call("2"), late}}
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 1201, Text: "research", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 1201)
	if rec.Lookups != 2 {
		t.Errorf("lookups = %d, want 2: the refused round's calls never ran, so they never counted against the cap", rec.Lookups)
	}

	rounds := roundsFor(t, dir, 1201)
	if len(rounds) != 3 {
		t.Fatalf("round records = %d, want 3", len(rounds))
	}
	refused := rounds[1].ToolCalls
	if len(refused) != 2 {
		t.Fatalf("refused round tool_calls = %+v, want 2 entries recording what the cap refused", refused)
	}
	for _, tc := range refused {
		if tc.Name != "current_research" {
			t.Errorf("name = %q, want current_research", tc.Name)
		}
		if tc.OK {
			t.Error("ok = true, want false: this call never ran")
		}
		if tc.Ms != 0 {
			t.Errorf("ms = %d, want 0: this call never ran", tc.Ms)
		}
		if tc.Error == nil || !strings.Contains(*tc.Error, "Refused") {
			t.Errorf("error = %v, want the refusal text", tc.Error)
		}
		if tc.Args == "" {
			t.Errorf("args not recorded for a refused call")
		}
	}
}

// A round the lookup cap refuses can carry a submission alongside the reads
// that tripped it: partition puts the submission in the same slice, so
// refuseLookups must not write it into tool_calls as a phantom submit_answer
// entry, which would misreport what the round actually ran.
func TestLedgerCapRefusedRoundNeverRecordsSubmitAnswer(t *testing.T) {
	dir := t.TempDir()
	mixed := model.Step{StopReason: model.StopToolUse, Blocks: []model.Block{
		{Type: model.BlockToolUse, ID: "a", Name: "nothing", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "b", Name: "nothing", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "c", Name: SubmitTool, Input: json.RawMessage(`{"shape":"summary","lines":["too soon"]}`)},
	}}
	late := submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"late"}})
	c := caps()
	c.MaxToolCalls = 1 // the round's two reads alone exceed the cap, so the whole round, submission included, is refused
	m := &scriptedModel{steps: []model.Step{mixed, late}}
	a := New(m, c)
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 1501, Text: "go", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rounds := roundsFor(t, dir, 1501)
	if len(rounds) == 0 {
		t.Fatal("no round records written")
	}
	refused := rounds[0].ToolCalls
	if len(refused) != 2 {
		t.Fatalf("refused round tool_calls = %+v, want 2 entries: the two reads, not the submission", refused)
	}
	for _, tc := range refused {
		if tc.Name == SubmitTool {
			t.Errorf("tool_calls recorded a phantom %s entry: %+v", SubmitTool, tc)
		}
		if tc.Name != "nothing" {
			t.Errorf("name = %q, want nothing", tc.Name)
		}
	}
}

// A round that is nothing but a submission never dispatches a read: any
// wall-clock time measured around it is local JSON parsing, not RCON, so it
// must not bill to ms_rcon.
func TestLedgerPureSubmissionRoundDoesNotBillRCON(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 1601, Text: "hi", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 1601)
	if rec.MsRCON != 0 {
		t.Errorf("ms_rcon = %d, want 0: the only round was a bare submission, nothing was ever dispatched to RCON", rec.MsRCON)
	}
}

// "new" ends a session; sessions.open runs for the question that follows,
// not this one, so the ledger's own session_fresh must not claim it yet,
// even though the artifact's own session marker correctly tells the player
// their next question starts clean.
func TestLedgerNewCommandSessionFreshIsNotForwardLooking(t *testing.T) {
	dir := t.TempDir()
	a := New(fake.New(), caps())
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 1701, Text: "new", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	if !res.Session.Fresh {
		t.Fatalf("the artifact's own session marker should say fresh: %+v", res.Session)
	}
	rec := findQuestion(t, dir, 1701)
	if rec.SessionFresh {
		t.Error("session_fresh = true, want false: sessions.open was never called for this command, only sessions.end")
	}
}

// F1 (fixes.md): runReads fans a round's calls out over goroutines, so the
// round's own tool time is the wall clock across the whole phase, not the
// sum of each call's own ms. Three calls that each sleep the same duration,
// run in one round, must cost the round about one sleep's worth of ms_rcon,
// not three.
func TestLedgerRoundToolTimeIsWallClockNotSum(t *testing.T) {
	dir := t.TempDir()
	sleep := 100 * time.Millisecond
	sleepy := func(name string) tools.Tool {
		return tools.Tool{
			Name:        name,
			Description: "a stub that takes a while",
			Schema:      tools.ObjectSchema(map[string]any{"force": map[string]any{"type": "string"}}, "force"),
			Call: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
				time.Sleep(sleep)
				return json.RawMessage(`{"ok":true}`), nil
			},
		}
	}
	ts := []tools.Tool{sleepy("a"), sleepy("b"), sleepy("c")}
	step := model.Step{StopReason: model.StopToolUse, Blocks: []model.Block{
		{Type: model.BlockToolUse, ID: "1", Name: "a", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "2", Name: "b", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "3", Name: "c", Input: json.RawMessage(`{}`)},
	}}
	m := &scriptedModel{steps: []model.Step{step, submitStep("t4", map[string]any{"shape": "summary", "lines": []string{"done"}})}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 1301, Text: "go", PlayerIndex: player(1)}, ts); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rec := findQuestion(t, dir, 1301)
	sum := (3 * sleep).Milliseconds()
	if rec.MsRCON >= sum {
		t.Errorf("ms_rcon = %dms, want well under %dms: summing three concurrent %s calls measures fan-out width, not wall clock", rec.MsRCON, sum, sleep)
	}
	if rec.MsRCON < sleep.Milliseconds() {
		t.Errorf("ms_rcon = %dms, want at least one call's own %s: the calls do run and take real time", rec.MsRCON, sleep)
	}
}

// A nil Agent.Ledger (the zero value New returns) must never panic: every
// non-ledger test in this package already relies on this without setting it.
func TestAnswerWorksWithNoLedgerWired(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	a := New(m, caps())
	if _, err := a.Answer(context.Background(), Question{ID: 1101, PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
}
