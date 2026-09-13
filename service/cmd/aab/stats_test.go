package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
)

func strPtr(s string) *string { return &s }

// ---- parseStatsRange --------------------------------------------------

func TestParseStatsRangeNoArgsLeavesBothEndsOpen(t *testing.T) {
	from, to, err := parseStatsRange(nil)
	if err != nil {
		t.Fatalf("parseStatsRange: %v", err)
	}
	if !from.IsZero() || !to.IsZero() {
		t.Fatalf("from=%v to=%v, want both zero", from, to)
	}
}

func TestParseStatsRangeOneDay(t *testing.T) {
	from, to, err := parseStatsRange([]string{"2026-09-12"})
	if err != nil {
		t.Fatalf("parseStatsRange: %v", err)
	}
	want := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if !from.Equal(want) || !to.Equal(want) {
		t.Fatalf("from=%v to=%v, want both %v", from, to, want)
	}
}

func TestParseStatsRangeTwoDaysEitherOrder(t *testing.T) {
	from, to, err := parseStatsRange([]string{"2026-09-13", "2026-09-01"})
	if err != nil {
		t.Fatalf("parseStatsRange: %v", err)
	}
	wantFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) || !to.Equal(wantTo) {
		t.Fatalf("from=%v to=%v, want %v to %v (reversed args still sort chronologically)", from, to, wantFrom, wantTo)
	}
}

func TestParseStatsRangeInvalidDayErrors(t *testing.T) {
	if _, _, err := parseStatsRange([]string{"not-a-date"}); err == nil {
		t.Fatal("want an error for an unparseable day")
	}
}

func TestParseStatsRangeTooManyArgsErrors(t *testing.T) {
	if _, _, err := parseStatsRange([]string{"2026-09-01", "2026-09-02", "2026-09-03"}); err == nil {
		t.Fatal("want an error for more than two args")
	}
}

// ---- loadStatsRecords ---------------------------------------------------
//
// Day-listing itself (matching ledger-YYYY-MM-DD.jsonl, filtering by range,
// tolerating a missing directory) is ledger.Days' own responsibility and
// already covered by internal/ledger/reader_test.go (F8: stats.go must not
// keep a second copy of that regexp and directory walk). What belongs here
// is the behaviour loadStatsRecords itself adds on top: reading the ledger
// exactly once, by appending each day's records into one running total
// rather than re-reading the whole range separately (F6).

func writeLedgerDay(t *testing.T, dir, day string, qs []ledger.QuestionRecord, rounds []ledger.RoundRecord) {
	t.Helper()
	var b strings.Builder
	for _, r := range rounds {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal round: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	for _, q := range qs {
		line, err := json.Marshal(q)
		if err != nil {
			t.Fatalf("marshal question: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(dir, "ledger-"+day+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadStatsRecordsAggregatesAcrossDays(t *testing.T) {
	dir := t.TempDir()
	writeLedgerDay(t, dir, "2026-01-01",
		[]ledger.QuestionRecord{{QuestionID: 1, Cost: 0.01}},
		[]ledger.RoundRecord{{QuestionID: 1, ToolCalls: []ledger.ToolCall{}}})
	writeLedgerDay(t, dir, "2026-01-02",
		[]ledger.QuestionRecord{{QuestionID: 2, Cost: 0.02}, {QuestionID: 3, Cost: 0.03}},
		[]ledger.RoundRecord{{QuestionID: 2, ToolCalls: []ledger.ToolCall{}}})

	recs, dayCosts, err := loadStatsRecords(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("loadStatsRecords: %v", err)
	}
	if len(recs.Questions) != 3 {
		t.Fatalf("got %d questions, want 3 across both days", len(recs.Questions))
	}
	if len(recs.Rounds) != 2 {
		t.Fatalf("got %d rounds, want 2 across both days", len(recs.Rounds))
	}
	if len(dayCosts) != 2 {
		t.Fatalf("got %d day rows, want 2", len(dayCosts))
	}
	if dayCosts[0].Day != "2026-01-01" || round4(dayCosts[0].Cost) != 0.01 || dayCosts[0].Questions != 1 {
		t.Errorf("day[0] = %+v, want 2026-01-01 cost 0.01 over 1 question", dayCosts[0])
	}
	if dayCosts[1].Day != "2026-01-02" || round4(dayCosts[1].Cost) != 0.05 || dayCosts[1].Questions != 2 {
		t.Errorf("day[1] = %+v, want 2026-01-02 cost 0.05 over 2 questions", dayCosts[1])
	}
}

func TestLoadStatsRecordsFiltersByRange(t *testing.T) {
	dir := t.TempDir()
	writeLedgerDay(t, dir, "2026-01-01", []ledger.QuestionRecord{{QuestionID: 1}}, nil)
	writeLedgerDay(t, dir, "2026-01-02", []ledger.QuestionRecord{{QuestionID: 2}}, nil)

	only := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	recs, dayCosts, err := loadStatsRecords(dir, only, only)
	if err != nil {
		t.Fatalf("loadStatsRecords: %v", err)
	}
	if len(recs.Questions) != 1 || recs.Questions[0].QuestionID != 2 {
		t.Fatalf("questions = %+v, want only question 2 (2026-01-01 excluded by range)", recs.Questions)
	}
	if len(dayCosts) != 1 || dayCosts[0].Day != "2026-01-02" {
		t.Fatalf("dayCosts = %+v, want exactly one row for 2026-01-02", dayCosts)
	}
}

func TestLoadStatsRecordsSkippedCountsAggregateAcrossDays(t *testing.T) {
	dir := t.TempDir()
	// One malformed line alongside one good question record, on each of two days.
	for _, day := range []string{"2026-01-01", "2026-01-02"} {
		path := filepath.Join(dir, "ledger-"+day+".jsonl")
		body := "not json\n" + `{"question_id":1,"shape":"summary"}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	recs, _, err := loadStatsRecords(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("loadStatsRecords: %v", err)
	}
	if recs.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2 (one malformed line per day, summed across both)", recs.Skipped)
	}
	if len(recs.Questions) != 2 {
		t.Errorf("got %d questions, want 2 (the good line from each day)", len(recs.Questions))
	}
}

func TestLoadStatsRecordsMissingDirIsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	recs, dayCosts, err := loadStatsRecords(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("loadStatsRecords: %v", err)
	}
	if len(recs.Questions) != 0 || len(recs.Rounds) != 0 || len(dayCosts) != 0 {
		t.Fatalf("got recs=%+v dayCosts=%v, want all empty", recs, dayCosts)
	}
}

// ---- statsLedgerDir -------------------------------------------------------

func TestStatsLedgerDirFlagWinsWithoutTouchingConfig(t *testing.T) {
	// A nonexistent config path would make config.Load fail loudly if it were
	// ever consulted; an explicit -ledger-dir must never reach it at all.
	got := statsLedgerDir("/some/ledger/dir", "/does/not/exist/aab.yaml")
	if got != "/some/ledger/dir" {
		t.Errorf("statsLedgerDir = %q, want the flag value unchanged", got)
	}
}

func TestStatsLedgerDirFallsBackToConfigDirOnLoadFailure(t *testing.T) {
	t.Chdir(t.TempDir()) // an operator's cwd, distinct from dir below on purpose
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "aab.yaml")
	// No FACTORIO_RCON_PASSWORD and no file at cfgPath: config.LoadQuiet's
	// env-mode fallback will fail validate() (F5's own motivating case),
	// which must not be fatal here.
	got := statsLedgerDir("", cfgPath)
	if got != dir {
		t.Errorf("statsLedgerDir = %q, want %q (the directory holding the config path)", got, dir)
	}
}

// F5: this fallback path used to reach config.Load, which always writes
// aab.effective.yaml to the caller's working directory as a side effect
// (config/effective.go's finish). An operator running `aab stats` in the
// same directory as a running service's own aab.yaml would silently
// overwrite that service's snapshot with a validation-failed one of its
// own, on every stats invocation. statsLedgerDir must use
// config.LoadQuiet instead, which resolves the same directory without
// that side effect, on both the success and the failure path.
func TestStatsLedgerDirNeverWritesTheEffectiveConfigDump(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	t.Run("load succeeds", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "aab.yaml")
		yaml := "factorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /tmp/events.jsonl\n"
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

		statsLedgerDir("", cfgPath)
		if _, err := os.Stat(filepath.Join(cwd, config.EffectiveConfigName)); !os.IsNotExist(err) {
			t.Fatalf("statsLedgerDir wrote %s into the caller's cwd, stat returned: %v", config.EffectiveConfigName, err)
		}
	})

	t.Run("load fails validation", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "aab.yaml") // no file at cfgPath: env-mode fallback
		t.Setenv("FACTORIO_RCON_PASSWORD", "")
		t.Setenv("AAB_EVENTS_FILE", "") // validate() fails on this first, same as F5's own repro
		statsLedgerDir("", cfgPath)
		if _, err := os.Stat(filepath.Join(cwd, config.EffectiveConfigName)); !os.IsNotExist(err) {
			t.Fatalf("statsLedgerDir wrote %s into the caller's cwd on the failure path, stat returned: %v", config.EffectiveConfigName, err)
		}
	})
}

// ---- cache hit ratio ----------------------------------------------------

func TestAggregateCacheHitRatio(t *testing.T) {
	rounds := []ledger.RoundRecord{
		{QuestionID: 1, InputTokens: 400, CacheReadTokens: 600},
		{QuestionID: 2, InputTokens: 900, CacheReadTokens: 100},
	}
	// (600+100) / (400+600+900+100) = 700/2000 = 0.35
	ratio, ok := aggregateCacheHitRatio(rounds)
	if !ok {
		t.Fatal("want ok=true with tokens present")
	}
	if got := round2(ratio); got != 0.35 {
		t.Errorf("ratio = %v, want 0.35", got)
	}
}

func TestAggregateCacheHitRatioAbsentWhenNoTokens(t *testing.T) {
	_, ok := aggregateCacheHitRatio(nil)
	if ok {
		t.Fatal("want ok=false for no rounds at all, not a ratio of 0")
	}
}

func TestQuestionTokenTotalsJoinsRoundsByQuestionID(t *testing.T) {
	rounds := []ledger.RoundRecord{
		{QuestionID: 1, InputTokens: 100, CacheReadTokens: 0},
		{QuestionID: 1, InputTokens: 0, CacheReadTokens: 300},
		{QuestionID: 2, InputTokens: 50, CacheReadTokens: 50},
	}
	totals := questionTokenTotals(rounds)
	ratio1, ok := totals[1].cacheHitRatio()
	if !ok || round2(ratio1) != 0.75 { // 300/(100+300)
		t.Errorf("question 1 ratio = %v ok=%v, want 0.75 true", ratio1, ok)
	}
	ratio2, ok := totals[2].cacheHitRatio()
	if !ok || round2(ratio2) != 0.5 {
		t.Errorf("question 2 ratio = %v ok=%v, want 0.5 true", ratio2, ok)
	}
	if _, ok := totals[3].cacheHitRatio(); ok {
		t.Error("question with no rounds at all must report ok=false, not a zero ratio")
	}
}

func TestQuestionRowsSortedByQuestionID(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{QuestionID: 3, Asker: "c"},
		{QuestionID: 1, Asker: "a"},
		{QuestionID: 2, Asker: "b"},
	}
	rows := questionRows(qs, nil)
	for i, want := range []int64{1, 2, 3} {
		if rows[i].ID != want {
			t.Errorf("rows[%d].ID = %d, want %d", i, rows[i].ID, want)
		}
	}
	for _, r := range rows {
		if r.HasCacheHit {
			t.Errorf("question %d has no rounds, want HasCacheHit=false", r.ID)
		}
	}
}

func TestWorstCacheHitSortsAscendingAndCaps(t *testing.T) {
	rows := []questionRow{
		{ID: 1, CacheHit: 0.9, HasCacheHit: true},
		{ID: 2, CacheHit: 0.1, HasCacheHit: true},
		{ID: 3, CacheHit: 0.5, HasCacheHit: true},
		{ID: 4, HasCacheHit: false}, // no data, must be excluded entirely
	}
	worst := worstCacheHit(rows, 2)
	if len(worst) != 2 {
		t.Fatalf("got %d rows, want 2 (capped)", len(worst))
	}
	if worst[0].ID != 2 || worst[1].ID != 3 {
		t.Errorf("worst = %+v, want question 2 then question 3 (lowest ratio first)", worst)
	}
}

// ---- rounds histogram / tool frequency ----------------------------------

func TestRoundsHistogram(t *testing.T) {
	qs := []ledger.QuestionRecord{{Rounds: 2}, {Rounds: 1}, {Rounds: 2}, {Rounds: 2}}
	buckets := roundsHistogram(qs)
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2", len(buckets))
	}
	if buckets[0].Rounds != 1 || buckets[0].Count != 1 {
		t.Errorf("buckets[0] = %+v, want {1 1}", buckets[0])
	}
	if buckets[1].Rounds != 2 || buckets[1].Count != 3 {
		t.Errorf("buckets[1] = %+v, want {2 3}", buckets[1])
	}
}

func TestToolFrequencySortedDescending(t *testing.T) {
	rounds := []ledger.RoundRecord{
		{ToolCalls: []ledger.ToolCall{{Name: "a"}, {Name: "b"}}},
		{ToolCalls: []ledger.ToolCall{{Name: "a"}}},
	}
	freq := toolFrequency(rounds)
	if len(freq) != 2 || freq[0].Name != "a" || freq[0].Count != 2 || freq[1].Name != "b" || freq[1].Count != 1 {
		t.Fatalf("freq = %+v, want a:2 then b:1", freq)
	}
}

// ---- tool repeats within a round -----------------------------------------

func TestToolRepeatsNamesTheTopCandidate(t *testing.T) {
	rounds := []ledger.RoundRecord{
		// current_research called 4 times in one round: 3 extra calls.
		{ToolCalls: []ledger.ToolCall{{Name: "current_research"}, {Name: "current_research"}, {Name: "current_research"}, {Name: "current_research"}}},
		// current_research called twice more in a second round: 1 extra call.
		{ToolCalls: []ledger.ToolCall{{Name: "current_research"}, {Name: "current_research"}, {Name: "find_item"}}},
		// find_item called once here, never repeated in the same round.
		{ToolCalls: []ledger.ToolCall{{Name: "find_item"}}},
	}
	repeats := toolRepeats(rounds)
	if len(repeats) != 1 {
		t.Fatalf("repeats = %+v, want exactly one tool (find_item never repeated within a round)", repeats)
	}
	top := repeats[0]
	if top.Name != "current_research" {
		t.Fatalf("top candidate = %q, want current_research", top.Name)
	}
	if top.RoundsWithRepeat != 2 {
		t.Errorf("RoundsWithRepeat = %d, want 2", top.RoundsWithRepeat)
	}
	if top.ExtraCalls != 4 { // 3 + 1
		t.Errorf("ExtraCalls = %d, want 4", top.ExtraCalls)
	}
	if top.MaxInRound != 4 {
		t.Errorf("MaxInRound = %d, want 4", top.MaxInRound)
	}
}

func TestToolRepeatsEmptyWhenNothingRepeatsInARound(t *testing.T) {
	rounds := []ledger.RoundRecord{
		{ToolCalls: []ledger.ToolCall{{Name: "a"}, {Name: "b"}}},
		{ToolCalls: []ledger.ToolCall{{Name: "a"}}},
	}
	if repeats := toolRepeats(rounds); len(repeats) != 0 {
		t.Errorf("repeats = %+v, want none: tool a never appears twice in the SAME round", repeats)
	}
}

// ---- refusals -------------------------------------------------------------

func TestComputeRefusalsGroupsByReason(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{Refused: true, RefusedReason: strPtr("quota")},
		{Refused: true, RefusedReason: strPtr("quota")},
		{Refused: true, RefusedReason: strPtr("budget")},
		{Refused: false},
	}
	rep := computeRefusals(qs)
	if rep.Total != 4 || rep.Refused != 3 {
		t.Fatalf("Total=%d Refused=%d, want 4 and 3", rep.Total, rep.Refused)
	}
	rate, ok := rep.Rate()
	if !ok || round2(rate) != 0.75 {
		t.Errorf("Rate = %v ok=%v, want 0.75 true", rate, ok)
	}
	if len(rep.ByReason) != 2 || rep.ByReason[0].Reason != "quota" || rep.ByReason[0].Count != 2 {
		t.Fatalf("ByReason = %+v, want quota:2 first (most common)", rep.ByReason)
	}
}

func TestComputeRefusalsRateAbsentWhenNoQuestions(t *testing.T) {
	if _, ok := computeRefusals(nil).Rate(); ok {
		t.Error("want ok=false with zero questions, not a rate of 0")
	}
}

// ---- ms model vs rcon, cost -----------------------------------------------

func TestMsModelVsRCON(t *testing.T) {
	qs := []ledger.QuestionRecord{{MsModel: 100, MsRCON: 20}, {MsModel: 50, MsRCON: 80}}
	model, rcon := msModelVsRCON(qs)
	if model != 150 || rcon != 100 {
		t.Errorf("model=%d rcon=%d, want 150 and 100", model, rcon)
	}
}

func TestCostSummary(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{QuestionID: 1, Cost: 0.01},
		{QuestionID: 2, Cost: 0.05},
		{QuestionID: 3, Cost: 0.02},
	}
	total, avg, max, maxID, ok := costSummary(qs)
	if !ok {
		t.Fatal("want ok=true")
	}
	if round4(total) != 0.08 {
		t.Errorf("total = %v, want 0.08", total)
	}
	if round4(avg) != round4(0.08/3) {
		t.Errorf("avg = %v, want %v", avg, 0.08/3)
	}
	if maxID != 2 || round4(max) != 0.05 {
		t.Errorf("max question = %d at %v, want 2 at 0.05", maxID, max)
	}
}

func TestCostSummaryAbsentWhenNoQuestions(t *testing.T) {
	if _, _, _, _, ok := costSummary(nil); ok {
		t.Error("want ok=false with zero questions")
	}
}

func TestCostSummaryZeroCostWindowNamesARealQuestion(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{QuestionID: 7, Cost: 0},
		{QuestionID: 8, Cost: 0},
	}
	_, _, max, maxID, ok := costSummary(qs)
	if !ok {
		t.Fatal("want ok=true")
	}
	if max != 0 {
		t.Errorf("max = %v, want 0", max)
	}
	// maxID must be a question that actually exists (the first one), never
	// the zero value 0, which is not among the questions asked here.
	if maxID != 7 {
		t.Errorf("maxID = %d, want 7 (the first question, not the zero value)", maxID)
	}
}

func TestSumCost(t *testing.T) {
	qs := []ledger.QuestionRecord{{Cost: 1.5}, {Cost: 2.5}}
	total, count := sumCost(qs)
	if round2(total) != 4 || count != 2 {
		t.Errorf("total=%v count=%d, want 4 and 2", total, count)
	}
}

func TestCostPerPlayerSortedDescending(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{Asker: "alice", Cost: 0.01},
		{Asker: "bob", Cost: 0.05},
		{Asker: "alice", Cost: 0.01},
	}
	players := costPerPlayer(qs)
	if len(players) != 2 || players[0].Asker != "bob" {
		t.Fatalf("players = %+v, want bob first (higher total)", players)
	}
	if players[1].Asker != "alice" || players[1].Questions != 2 || round4(players[1].Cost) != 0.02 {
		t.Errorf("alice row = %+v, want 2 questions totalling 0.02", players[1])
	}
}

// ---- zero-lookup share -----------------------------------------------------

func TestComputeZeroLookupShareOnlyCountsBriefingOn(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{Briefing: ledger.BriefingOn, ZeroLookup: true},
		{Briefing: ledger.BriefingOn, ZeroLookup: false},
		{Briefing: ledger.BriefingOff, ZeroLookup: true}, // must be excluded
	}
	rep := computeZeroLookupShare(qs)
	if rep.OnCount != 2 || rep.ZeroCount != 1 {
		t.Fatalf("OnCount=%d ZeroCount=%d, want 2 and 1 (briefing=off row excluded)", rep.OnCount, rep.ZeroCount)
	}
	share, ok := rep.Share()
	if !ok || round2(share) != 0.5 {
		t.Errorf("Share = %v ok=%v, want 0.5 true", share, ok)
	}
}

func TestComputeZeroLookupShareAbsentWhenBriefingNeverOn(t *testing.T) {
	qs := []ledger.QuestionRecord{{Briefing: ledger.BriefingOff}, {Briefing: ledger.BriefingFailed}}
	if _, ok := computeZeroLookupShare(qs).Share(); ok {
		t.Error("want ok=false when no question has briefing=on, not a share of 0")
	}
}

// ---- briefing ROI -----------------------------------------------------------

func TestComputeBriefingROIArithmetic(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{Briefing: ledger.BriefingOn, BriefingTokens: 100, ZeroLookup: true},
		{Briefing: ledger.BriefingOn, BriefingTokens: 200, ZeroLookup: false},
		{Briefing: ledger.BriefingOff, BriefingTokens: 999, ZeroLookup: true}, // excluded
	}
	rounds := []ledger.RoundRecord{
		{Cost: 0.10, InputTokens: 800, CacheReadTokens: 200}, // rate: 0.10/1000 = 0.0001/token
		{Cost: 0.20, InputTokens: 800, CacheReadTokens: 200},
	}
	rep := computeBriefingROI(qs, rounds)
	if rep.OnQuestions != 2 || rep.BriefingTokens != 300 || rep.ZeroLookup != 1 {
		t.Fatalf("rep = %+v, want OnQuestions=2 BriefingTokens=300 ZeroLookup=1", rep)
	}
	if !rep.CostRateOK {
		t.Fatal("want CostRateOK=true with priced rounds present")
	}
	// total cost 0.30 over 2000 priced tokens = 0.00015/token; 300 tokens * that = 0.045
	if round4(rep.EstBriefingCost) != 0.045 {
		t.Errorf("EstBriefingCost = %v, want 0.045", rep.EstBriefingCost)
	}
	if !rep.RoundCostOK {
		t.Fatal("want RoundCostOK=true with rounds present")
	}
	// avg round cost = 0.30/2 = 0.15; 1 zero-lookup question * 0.15 = 0.15
	if round4(rep.EstAvoidedCost) != 0.15 {
		t.Errorf("EstAvoidedCost = %v, want 0.15", rep.EstAvoidedCost)
	}
}

func TestComputeBriefingROIAbsentWhenNoRounds(t *testing.T) {
	qs := []ledger.QuestionRecord{{Briefing: ledger.BriefingOn, BriefingTokens: 50}}
	rep := computeBriefingROI(qs, nil)
	if rep.CostRateOK || rep.RoundCostOK {
		t.Errorf("rep = %+v, want both cost estimates unavailable with no rounds to derive a rate from", rep)
	}
}

// ---- ask-back rate and resolution -----------------------------------------

func TestComputeAskBackCountsAndResolvesWithinSameSession(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{QuestionID: 1, SessionKey: "team-3#iron", AskedBack: true},
		{QuestionID: 2, SessionKey: "team-3#iron", AwaitingReplyResolved: true}, // resolves question 1
		{QuestionID: 3, SessionKey: "other", AskedBack: true},                   // never resolved
	}
	rep := computeAskBack(qs)
	if rep.Total != 3 || rep.AskedBack != 2 {
		t.Fatalf("Total=%d AskedBack=%d, want 3 and 2", rep.Total, rep.AskedBack)
	}
	if rep.Resolved != 1 {
		t.Fatalf("Resolved = %d, want 1 (only the team-3#iron ask-back was followed by a resolution in its own session)", rep.Resolved)
	}
	rate, ok := rep.AskBackRate()
	if !ok || round4(rate) != round4(2.0/3.0) {
		t.Errorf("AskBackRate = %v, want 2/3", rate)
	}
	resRate, ok := rep.ResolutionRate()
	if !ok || round2(resRate) != 0.5 {
		t.Errorf("ResolutionRate = %v, want 0.5 (1 of 2 ask-backs resolved)", resRate)
	}
}

func TestComputeAskBackIgnoresResolutionInADifferentSession(t *testing.T) {
	qs := []ledger.QuestionRecord{
		{QuestionID: 1, SessionKey: "team-1", AskedBack: true},
		{QuestionID: 2, SessionKey: "team-2", AwaitingReplyResolved: true}, // different session: must not count
	}
	rep := computeAskBack(qs)
	if rep.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0: the resolution was in a different session", rep.Resolved)
	}
}

func TestComputeAskBackRateAbsentWhenNoQuestions(t *testing.T) {
	rep := computeAskBack(nil)
	if _, ok := rep.AskBackRate(); ok {
		t.Error("want ok=false with zero questions")
	}
	if _, ok := rep.ResolutionRate(); ok {
		t.Error("want ok=false with zero ask-backs")
	}
}

// ---- test helpers -----------------------------------------------------------

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func round4(f float64) float64 {
	return float64(int(f*10000+0.5)) / 10000
}
