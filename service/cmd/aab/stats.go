// aab stats reads the observability ledger (internal/ledger,
// docs/design/phase4-observability-spec.md sections 2-4) and reports on it.
// Nothing here calls the model or RCON: it is pure arithmetic over JSONL
// already on disk, run whenever an operator wants to look, not on any path
// that answers a question.
//
// Entry point, called from main.go BEFORE the config/RCON prologue every
// other subcommand sits behind, the same way runPoll and runRPC take their
// own remaining args (no ctx, no rpc.Client: stats never needs either):
//
//	func runStats(dir string, args []string)
//
// dir is the ledger directory to read, resolved by main.go's
// statsLedgerDir: an explicit -ledger-dir flag, or a best-effort
// config.LoadQuiet whose failure is not fatal for this subcommand alone (a
// missing RCON password, which stats has no use for, must not stop it from
// reading a ledger). args is the subcommand's own trailing words: none
// reports over every ledger file found in dir, one YYYY-MM-DD word reports
// that single UTC calendar day, two such words report the inclusive range
// between them (either order). A bad arg or an unreadable ledger directory
// calls log.Fatalf, the same way every other subcommand in this package
// reports its own usage errors.
package main

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
)

func runStats(dir string, args []string) {
	from, to, err := parseStatsRange(args)
	if err != nil {
		log.Fatalf("stats: %v", err)
	}

	recs, dayCosts, err := loadStatsRecords(dir, from, to)
	if err != nil {
		log.Fatalf("stats: %v", err)
	}

	fmt.Print(renderReport(from, to, recs, dayCosts))
}

// statsLedgerDir resolves the ledger directory for a stats run (F5): an
// explicit -ledger-dir flag wins outright and never touches config at all,
// so an operator who only wants to read the ledger never needs the RCON
// password config.Load's own validate() enforces. Absent that flag, it
// falls back to a best-effort config.LoadQuiet: on success, cfg.Ledger.Dir
// is already resolved against the config file's own directory
// (config.go's applyLedgerDefaults). A failure there (most commonly a
// missing RCON password or events_file, both irrelevant to stats) is not
// fatal for this subcommand alone: config.LoadQuiet returns no *Config at
// all when validation fails, so this falls back by hand to the same
// "directory holding the config file" default config.go documents for
// ledger.dir.
//
// config.LoadQuiet is the dump-free entry point precisely so this fallback
// path never overwrites another process's own aab.effective.yaml with a
// stats-only, possibly validation-failed snapshot (F5's motivating bug: an
// operator running `aab stats` in the service's own working directory used
// to stomp the running service's effective-config file on every read).
// Passing -ledger-dir explicitly skips config entirely and leaves no trace
// on disk either way.
func statsLedgerDir(flagVal, cfgPath string) string {
	if flagVal != "" {
		return flagVal
	}
	cfg, err := config.LoadQuiet(cfgPath)
	if err == nil {
		return cfg.Ledger.Dir
	}
	log.Printf("stats: could not load %s (%v); using the config file's own directory for the ledger", cfgPath, err)
	if dir := filepath.Dir(cfgPath); dir != "" {
		return dir
	}
	return "."
}

// statsDayFormat is the day layout both the CLI args and ledger-YYYY-MM-DD.jsonl
// file names use.
const statsDayFormat = "2006-01-02"

// parseStatsRange turns aab stats' own trailing args into a [from, to]
// window. Zero args leaves both ends open (every file ledger.ReadRange can
// find); a zero time.Time already means exactly that to ReadRange.
func parseStatsRange(args []string) (from, to time.Time, err error) {
	switch len(args) {
	case 0:
		return time.Time{}, time.Time{}, nil
	case 1:
		day, err := time.Parse(statsDayFormat, args[0])
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid day %q, want YYYY-MM-DD", args[0])
		}
		return day, day, nil
	case 2:
		a, err := time.Parse(statsDayFormat, args[0])
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid day %q, want YYYY-MM-DD", args[0])
		}
		b, err := time.Parse(statsDayFormat, args[1])
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid day %q, want YYYY-MM-DD", args[1])
		}
		if b.Before(a) {
			a, b = b, a
		}
		return a, b, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("usage: aab stats [day|from to], got %d args", len(args))
	}
}

// loadStatsRecords reads the ledger exactly once (F6): one ledger.ReadRange
// call per calendar day in [from, to] (ledger.Days, internal/ledger/reader.go,
// lists which days exist under dir), each day's records appended into the
// running Records and its own cost summed into dayCosts in the same pass. A
// QuestionRecord carries no timestamp of its own (section 2's field table has
// none), so the day a question happened on is knowable only from which file
// it came from; reading one day at a time is the only way to keep that origin
// long enough to group cost by day, and doing it this way means every byte on
// disk is parsed once, not once for a whole-range read and again per day.
func loadStatsRecords(dir string, from, to time.Time) (ledger.Records, []dayCost, error) {
	days, err := ledger.Days(dir, from, to)
	if err != nil {
		return ledger.Records{}, nil, fmt.Errorf("listing ledger days: %w", err)
	}

	var recs ledger.Records
	dayCosts := make([]dayCost, 0, len(days))
	for _, day := range days {
		dayRecs, err := ledger.ReadRange(dir, day, day)
		if err != nil {
			return ledger.Records{}, nil, fmt.Errorf("reading %s: %w", day.Format(statsDayFormat), err)
		}
		recs.Questions = append(recs.Questions, dayRecs.Questions...)
		recs.Rounds = append(recs.Rounds, dayRecs.Rounds...)
		recs.Skipped += dayRecs.Skipped

		total, count := sumCost(dayRecs.Questions)
		dayCosts = append(dayCosts, dayCost{Day: day.Format(statsDayFormat), Cost: total, Questions: count})
	}
	return recs, dayCosts, nil
}

// ---- pure arithmetic, section 4's reports ----------------------------------

// tokenTotals is a question's summed input/cache_read tokens across every
// round it took, the two numbers cache hit ratio per question is built from.
type tokenTotals struct {
	input     int
	cacheRead int
}

func (t tokenTotals) cacheHitRatio() (ratio float64, ok bool) {
	total := t.input + t.cacheRead
	if total == 0 {
		return 0, false
	}
	return float64(t.cacheRead) / float64(total), true
}

// questionTokenTotals sums InputTokens/CacheReadTokens per question_id
// across every round, so a round can be joined back to the question it
// belongs to the same way the ledger itself is keyed (section 2).
func questionTokenTotals(rounds []ledger.RoundRecord) map[int64]tokenTotals {
	out := make(map[int64]tokenTotals)
	for _, r := range rounds {
		t := out[r.QuestionID]
		t.input += r.InputTokens
		t.cacheRead += r.CacheReadTokens
		out[r.QuestionID] = t
	}
	return out
}

// aggregateCacheHitRatio is the regression alarm itself: cache_read summed
// over input+cache_read summed, across every round in the window, not an
// unweighted average of per-question ratios (a handful of tiny questions
// must not swamp the number that matters).
func aggregateCacheHitRatio(rounds []ledger.RoundRecord) (ratio float64, ok bool) {
	var input, cacheRead int
	for _, r := range rounds {
		input += r.InputTokens
		cacheRead += r.CacheReadTokens
	}
	total := input + cacheRead
	if total == 0 {
		return 0, false
	}
	return float64(cacheRead) / float64(total), true
}

// questionRow is one line of the per-question detail table: cache hit ratio
// and cost side by side, since both are reported "per question" (section 4)
// and a question asked once is worth reading about once.
type questionRow struct {
	ID          int64
	Asker       string
	Rounds      int
	Lookups     int
	Cost        float64
	CacheHit    float64
	HasCacheHit bool
	Shape       string
	Refused     bool
	AskedBack   bool
}

// questionRows builds one row per question, in question_id order (the
// ledger's own, and the only ordering it carries at all).
func questionRows(qs []ledger.QuestionRecord, rounds []ledger.RoundRecord) []questionRow {
	totals := questionTokenTotals(rounds)
	rows := make([]questionRow, 0, len(qs))
	for _, q := range qs {
		ratio, ok := totals[q.QuestionID].cacheHitRatio()
		rows = append(rows, questionRow{
			ID: q.QuestionID, Asker: q.Asker, Rounds: q.Rounds, Lookups: q.Lookups,
			Cost: q.Cost, CacheHit: ratio, HasCacheHit: ok, Shape: q.Shape,
			Refused: q.Refused, AskedBack: q.AskedBack,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

// worstCacheHit returns up to n questions with the lowest cache hit ratio,
// worst first: the "unmistakable" surfacing the regression alarm needs,
// on top of the full per-question table.
func worstCacheHit(rows []questionRow, n int) []questionRow {
	var withData []questionRow
	for _, r := range rows {
		if r.HasCacheHit {
			withData = append(withData, r)
		}
	}
	sort.Slice(withData, func(i, j int) bool { return withData[i].CacheHit < withData[j].CacheHit })
	if len(withData) > n {
		withData = withData[:n]
	}
	return withData
}

// roundsBucket is one row of the rounds histogram.
type roundsBucket struct {
	Rounds int
	Count  int
}

func roundsHistogram(qs []ledger.QuestionRecord) []roundsBucket {
	counts := make(map[int]int)
	for _, q := range qs {
		counts[q.Rounds]++
	}
	out := make([]roundsBucket, 0, len(counts))
	for rounds, count := range counts {
		out = append(out, roundsBucket{Rounds: rounds, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rounds < out[j].Rounds })
	return out
}

// toolCount is one row of the tool frequency table. Refused counts the calls
// the lookup cap turned away, which are recorded so the repeat table can see
// them but never reached the game: served is Count minus Refused, and an
// operator reading this table for RCON load wants the difference.
type toolCount struct {
	Name    string
	Count   int
	Refused int
}

func toolFrequency(rounds []ledger.RoundRecord) []toolCount {
	counts := make(map[string]int)
	refused := make(map[string]int)
	for _, r := range rounds {
		for _, c := range r.ToolCalls {
			counts[c.Name]++
			if !c.OK {
				refused[c.Name]++
			}
		}
	}
	out := sortedToolCounts(counts)
	for i := range out {
		out[i].Refused = refused[out[i].Name]
	}
	return out
}

func sortedToolCounts(counts map[string]int) []toolCount {
	out := make([]toolCount, 0, len(counts))
	for name, count := range counts {
		out = append(out, toolCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// toolRepeat is one tool's repeat behaviour within single rounds: how many
// rounds called it more than once, how many calls beyond the first that
// added up to across the window, and the worst single round.
type toolRepeat struct {
	Name             string
	RoundsWithRepeat int
	ExtraCalls       int
	MaxInRound       int
}

// toolRepeats finds, per tool, every round that called it more than once
// (section 4's "tool repeat count within a single round"): the evidence for
// which tool to make plural next (Decision 3), from what questions actually
// asked rather than a guess. ExtraCalls is what ranks the candidates: a
// round calling the same tool 4 times costs 3 calls a plural tool would
// have saved, not 4.
func toolRepeats(rounds []ledger.RoundRecord) []toolRepeat {
	agg := make(map[string]*toolRepeat)
	var order []string
	for _, r := range rounds {
		counts := make(map[string]int)
		for _, c := range r.ToolCalls {
			counts[c.Name]++
		}
		for name, n := range counts {
			if n < 2 {
				continue
			}
			tr, ok := agg[name]
			if !ok {
				tr = &toolRepeat{Name: name}
				agg[name] = tr
				order = append(order, name)
			}
			tr.RoundsWithRepeat++
			tr.ExtraCalls += n - 1
			if n > tr.MaxInRound {
				tr.MaxInRound = n
			}
		}
	}
	out := make([]toolRepeat, 0, len(order))
	for _, name := range order {
		out = append(out, *agg[name])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExtraCalls != out[j].ExtraCalls {
			return out[i].ExtraCalls > out[j].ExtraCalls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// reasonCount is one row of the refusal-reason breakdown.
type reasonCount struct {
	Reason string
	Count  int
}

// refusalReport is refused/refused_reason across questions, grouped by
// reason (section 4).
type refusalReport struct {
	Total    int
	Refused  int
	ByReason []reasonCount
}

func (r refusalReport) Rate() (rate float64, ok bool) {
	if r.Total == 0 {
		return 0, false
	}
	return float64(r.Refused) / float64(r.Total), true
}

func computeRefusals(qs []ledger.QuestionRecord) refusalReport {
	counts := make(map[string]int)
	rep := refusalReport{Total: len(qs)}
	for _, q := range qs {
		if !q.Refused {
			continue
		}
		rep.Refused++
		reason := "(no reason recorded)"
		if q.RefusedReason != nil && *q.RefusedReason != "" {
			reason = *q.RefusedReason
		}
		counts[reason]++
	}
	for reason, n := range counts {
		rep.ByReason = append(rep.ByReason, reasonCount{Reason: reason, Count: n})
	}
	sort.Slice(rep.ByReason, func(i, j int) bool {
		if rep.ByReason[i].Count != rep.ByReason[j].Count {
			return rep.ByReason[i].Count > rep.ByReason[j].Count
		}
		return rep.ByReason[i].Reason < rep.ByReason[j].Reason
	})
	return rep
}

// msModelVsRCON sums ms_model against ms_rcon across every question
// (section 4): whether round trips or RCON dominate wall clock.
func msModelVsRCON(qs []ledger.QuestionRecord) (model, rcon int64) {
	for _, q := range qs {
		model += q.MsModel
		rcon += q.MsRCON
	}
	return model, rcon
}

// costSummary is cost per question's own aggregate: total, mean, and the
// single most expensive question, so an outlier stands out next to the
// per-question table rather than only inside it. max/maxID seed from the
// first record rather than the zero value, so a window where every question
// cost nothing still names a real question_id instead of printing "most
// expensive question 0" for a question 0 that never existed.
func costSummary(qs []ledger.QuestionRecord) (total, avg, max float64, maxID int64, ok bool) {
	if len(qs) == 0 {
		return 0, 0, 0, 0, false
	}
	max, maxID = qs[0].Cost, qs[0].QuestionID
	for _, q := range qs {
		total += q.Cost
		if q.Cost > max {
			max = q.Cost
			maxID = q.QuestionID
		}
	}
	return total, total / float64(len(qs)), max, maxID, true
}

// sumCost totals Cost across a slice of questions, and how many there were.
// Used once per calendar day for the cost-per-day breakdown
// (loadStatsRecords' doc comment says why that has to happen a day at a
// time), and reused directly here on the whole window for costSummary's
// own total.
func sumCost(qs []ledger.QuestionRecord) (total float64, count int) {
	for _, q := range qs {
		total += q.Cost
	}
	return total, len(qs)
}

// playerCost is one row of the cost-per-player table.
type playerCost struct {
	Asker     string
	Cost      float64
	Questions int
}

func costPerPlayer(qs []ledger.QuestionRecord) []playerCost {
	totals := make(map[string]*playerCost)
	var order []string
	for _, q := range qs {
		pc, ok := totals[q.Asker]
		if !ok {
			pc = &playerCost{Asker: q.Asker}
			totals[q.Asker] = pc
			order = append(order, q.Asker)
		}
		pc.Cost += q.Cost
		pc.Questions++
	}
	out := make([]playerCost, 0, len(order))
	for _, name := range order {
		out = append(out, *totals[name])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Asker < out[j].Asker
	})
	return out
}

// dayCost is one row of the cost-per-day table, filled in by
// loadStatsRecords from the same per-day ledger.ReadRange call that builds
// the aggregate Records (see loadStatsRecords).
type dayCost struct {
	Day       string
	Cost      float64
	Questions int
}

// zeroLookupReport is zero-lookup share, counted only over questions where
// briefing is on (section 4: "the free tier's own KPI... measured only
// where the briefing actually ran").
type zeroLookupReport struct {
	OnCount   int
	ZeroCount int
}

func (z zeroLookupReport) Share() (share float64, ok bool) {
	if z.OnCount == 0 {
		return 0, false
	}
	return float64(z.ZeroCount) / float64(z.OnCount), true
}

func computeZeroLookupShare(qs []ledger.QuestionRecord) zeroLookupReport {
	var rep zeroLookupReport
	for _, q := range qs {
		if q.Briefing != ledger.BriefingOn {
			continue
		}
		rep.OnCount++
		if q.ZeroLookup {
			rep.ZeroCount++
		}
	}
	return rep
}

// briefingROI is briefing cost against rounds saved (section 4), the one
// number that can overturn Decision 1's own cost argument. Both dollar
// figures are estimates built from this window's own observed averages,
// never a reported figure: BriefingTokens is itself the byte-per-4 estimate
// the ledger already carries (never a token count the model API reported),
// and there is no per-token price anywhere in the ledger schema to multiply
// it by exactly, so the effective rate is derived from the window's own
// rounds instead. The *OK flags say whether that derivation had anything to
// divide by; a window with no priced rounds at all reports the estimate as
// absent, never as a silent zero.
type briefingROI struct {
	OnQuestions     int
	ZeroLookup      int
	BriefingTokens  int
	EstBriefingCost float64
	CostRateOK      bool
	EstAvoidedCost  float64
	RoundCostOK     bool
}

func computeBriefingROI(qs []ledger.QuestionRecord, rounds []ledger.RoundRecord) briefingROI {
	var rep briefingROI
	for _, q := range qs {
		if q.Briefing != ledger.BriefingOn {
			continue
		}
		rep.OnQuestions++
		rep.BriefingTokens += q.BriefingTokens
		if q.ZeroLookup {
			rep.ZeroLookup++
		}
	}

	var roundCost float64
	var pricedTokens, roundCount int
	for _, r := range rounds {
		roundCost += r.Cost
		pricedTokens += r.InputTokens + r.CacheReadTokens
		roundCount++
	}
	if pricedTokens > 0 {
		rep.CostRateOK = true
		rep.EstBriefingCost = float64(rep.BriefingTokens) * (roundCost / float64(pricedTokens))
	}
	if roundCount > 0 {
		rep.RoundCostOK = true
		rep.EstAvoidedCost = float64(rep.ZeroLookup) * (roundCost / float64(roundCount))
	}
	return rep
}

// askBackReport is ask-back rate and its resolution rate (section 4): how
// often the anchor design (Decision 7) fires, and how often a fired one
// gets answered before the session idles out.
type askBackReport struct {
	Total     int
	AskedBack int
	Resolved  int
}

func (a askBackReport) AskBackRate() (rate float64, ok bool) {
	if a.Total == 0 {
		return 0, false
	}
	return float64(a.AskedBack) / float64(a.Total), true
}

func (a askBackReport) ResolutionRate() (rate float64, ok bool) {
	if a.AskedBack == 0 {
		return 0, false
	}
	return float64(a.Resolved) / float64(a.AskedBack), true
}

// computeAskBack counts ask-backs and, among them, how many were later
// resolved: a later question in the same session_key with
// awaiting_reply_resolved true. question_id is the only ordering the
// ledger carries at all (no timestamp field exists, section 2), so "later"
// means a strictly greater question_id, the same counter the companion's
// own question ring assigns in order.
//
// One pass builds maxResolved, the highest question_id per session_key
// among questions with awaiting_reply_resolved true; a second pass looks
// each ask-back up in that map. An ask-back at question_id x in session s
// was resolved later iff maxResolved[s] > x: if the session's own highest
// resolving id beats x, at least one resolving question after x exists,
// which is all "was this one ever resolved" needs to know. This is O(n)
// rather than a scan of the rest of the ledger per ask-back, which would
// not finish over a year of records once the feature is live.
func computeAskBack(qs []ledger.QuestionRecord) askBackReport {
	maxResolved := make(map[string]int64)
	for _, q := range qs {
		if q.AwaitingReplyResolved && q.QuestionID > maxResolved[q.SessionKey] {
			maxResolved[q.SessionKey] = q.QuestionID
		}
	}

	rep := askBackReport{Total: len(qs)}
	for _, q := range qs {
		if !q.AskedBack {
			continue
		}
		rep.AskedBack++
		if maxResolved[q.SessionKey] > q.QuestionID {
			rep.Resolved++
		}
	}
	return rep
}

// ---- rendering: plain text, aligned columns, no colour, no emoji ----------

func renderReport(from, to time.Time, recs ledger.Records, dayCosts []dayCost) string {
	var b strings.Builder

	fmt.Fprintf(&b, "aab stats: %s\n", rangeLabel(from, to))
	fmt.Fprintf(&b, "%d question(s), %d round(s)", len(recs.Questions), len(recs.Rounds))
	if recs.Skipped > 0 {
		fmt.Fprintf(&b, ", %d ledger line(s) could not be read back and were skipped", recs.Skipped)
	}
	fmt.Fprintln(&b)

	writeCacheHitSection(&b, recs)
	writeRoundsHistogram(&b, recs.Questions)
	writeToolFrequency(&b, recs.Rounds)
	writeToolRepeats(&b, recs.Rounds)
	writeRefusals(&b, recs.Questions)
	writeMsModelVsRCON(&b, recs.Questions)
	writeCost(&b, recs.Questions, dayCosts)
	writeZeroLookup(&b, recs.Questions)
	writeBriefingROI(&b, recs.Questions, recs.Rounds)
	writeAskBack(&b, recs.Questions)

	return b.String()
}

func rangeLabel(from, to time.Time) string {
	switch {
	case from.IsZero() && to.IsZero():
		return "every ledger file on disk"
	case from.Equal(to):
		return from.Format(statsDayFormat)
	default:
		return from.Format(statsDayFormat) + " to " + to.Format(statsDayFormat)
	}
}

func section(b *strings.Builder, title string) {
	fmt.Fprintf(b, "\n%s\n%s\n", title, strings.Repeat("-", len(title)))
}

// writeTable renders one aligned table via text/tabwriter, or a plain
// "(none)" line when there is nothing to show: an empty table still reads
// as a real answer ("nothing happened"), never as a missing section.
func writeTable(b *strings.Builder, headers []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Fprintln(b, "  (none)")
		return
	}
	tw := tabwriter.NewWriter(b, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	underline := make([]string, len(headers))
	for i, h := range headers {
		underline[i] = strings.Repeat("-", len(h))
	}
	fmt.Fprintln(tw, strings.Join(underline, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

func pct(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio*100)
}

func pctOr(ratio float64, ok bool) string {
	if !ok {
		return "n/a (no data)"
	}
	return pct(ratio)
}

func money(v float64) string {
	return fmt.Sprintf("$%.4f", v)
}

func flagCell(v bool) string {
	if v {
		return "yes"
	}
	return ""
}

func writeCacheHitSection(b *strings.Builder, recs ledger.Records) {
	section(b, "CACHE HIT RATIO PER QUESTION (regression alarm: a drop means the system prompt prefix moved)")
	overall, ok := aggregateCacheHitRatio(recs.Rounds)
	fmt.Fprintf(b, "  overall: %s\n\n", pctOr(overall, ok))

	rows := questionRows(recs.Questions, recs.Rounds)
	worst := worstCacheHit(rows, 5)
	if len(worst) > 0 {
		fmt.Fprintln(b, "  lowest cache hit ratio (worst first):")
		var worstRows [][]string
		for _, r := range worst {
			worstRows = append(worstRows, []string{fmt.Sprintf("%d", r.ID), r.Asker, pct(r.CacheHit)})
		}
		writeTable(b, []string{"question_id", "asker", "cache_hit"}, worstRows)
		fmt.Fprintln(b)
	}

	fmt.Fprintln(b, "  every question:")
	var all [][]string
	for _, r := range rows {
		all = append(all, []string{
			fmt.Sprintf("%d", r.ID), r.Asker, fmt.Sprintf("%d", r.Rounds), fmt.Sprintf("%d", r.Lookups),
			money(r.Cost), pctOr(r.CacheHit, r.HasCacheHit), r.Shape, flagCell(r.Refused), flagCell(r.AskedBack),
		})
	}
	writeTable(b, []string{"question_id", "asker", "rounds", "lookups", "cost", "cache_hit", "shape", "refused", "asked_back"}, all)
}

func writeRoundsHistogram(b *strings.Builder, qs []ledger.QuestionRecord) {
	section(b, "ROUNDS HISTOGRAM")
	buckets := roundsHistogram(qs)
	var rows [][]string
	for _, bucket := range buckets {
		rows = append(rows, []string{fmt.Sprintf("%d", bucket.Rounds), fmt.Sprintf("%d", bucket.Count)})
	}
	writeTable(b, []string{"rounds", "questions"}, rows)
}

func writeToolFrequency(b *strings.Builder, rounds []ledger.RoundRecord) {
	section(b, "TOOL FREQUENCY")
	freq := toolFrequency(rounds)
	var rows [][]string
	for _, t := range freq {
		rows = append(rows, []string{
			t.Name,
			fmt.Sprintf("%d", t.Count-t.Refused),
			fmt.Sprintf("%d", t.Refused),
			fmt.Sprintf("%d", t.Count),
		})
	}
	writeTable(b, []string{"tool", "served", "refused", "asked"}, rows)
}

func writeToolRepeats(b *strings.Builder, rounds []ledger.RoundRecord) {
	section(b, "TOOL REPEATS WITHIN A SINGLE ROUND (evidence for the next plural tool to build)")
	repeats := toolRepeats(rounds)
	var rows [][]string
	for _, r := range repeats {
		rows = append(rows, []string{
			r.Name, fmt.Sprintf("%d", r.RoundsWithRepeat), fmt.Sprintf("%d", r.ExtraCalls), fmt.Sprintf("%d", r.MaxInRound),
		})
	}
	writeTable(b, []string{"tool", "rounds_with_repeat", "extra_calls", "max_in_one_round"}, rows)
	if len(repeats) > 0 {
		top := repeats[0]
		fmt.Fprintf(b, "\n  >>> next plural tool to build: %s (%d extra call(s) across %d round(s), up to %d in one round)\n",
			top.Name, top.ExtraCalls, top.RoundsWithRepeat, top.MaxInRound)
	} else {
		fmt.Fprintln(b, "\n  >>> no tool was called more than once in the same round: nothing plural to build yet")
	}
}

func writeRefusals(b *strings.Builder, qs []ledger.QuestionRecord) {
	section(b, "REFUSAL RATE AND REASON")
	rep := computeRefusals(qs)
	rate, ok := rep.Rate()
	fmt.Fprintf(b, "  %d of %d question(s) refused (%s)\n\n", rep.Refused, rep.Total, pctOr(rate, ok))
	var rows [][]string
	for _, rc := range rep.ByReason {
		rows = append(rows, []string{rc.Reason, fmt.Sprintf("%d", rc.Count)})
	}
	writeTable(b, []string{"reason", "count"}, rows)
}

func writeMsModelVsRCON(b *strings.Builder, qs []ledger.QuestionRecord) {
	section(b, "MODEL MS AGAINST RCON MS")
	model, rcon := msModelVsRCON(qs)
	total := model + rcon
	if total == 0 {
		fmt.Fprintln(b, "  n/a (no data)")
		return
	}
	fmt.Fprintf(b, "  ms_model: %d (%s)\n", model, pct(float64(model)/float64(total)))
	fmt.Fprintf(b, "  ms_rcon:  %d (%s)\n", rcon, pct(float64(rcon)/float64(total)))
}

func writeCost(b *strings.Builder, qs []ledger.QuestionRecord, dayCosts []dayCost) {
	section(b, "COST PER QUESTION, PER PLAYER, PER DAY")
	total, avg, max, maxID, ok := costSummary(qs)
	if !ok {
		fmt.Fprintln(b, "  n/a (no data)")
	} else {
		fmt.Fprintf(b, "  total %s across %d question(s), average %s, most expensive question %d at %s\n",
			money(total), len(qs), money(avg), maxID, money(max))
		fmt.Fprintln(b, "  (see the per-question table above for every question's own cost)")
	}

	fmt.Fprintln(b, "\n  per player:")
	var playerRows [][]string
	for _, p := range costPerPlayer(qs) {
		playerRows = append(playerRows, []string{p.Asker, money(p.Cost), fmt.Sprintf("%d", p.Questions)})
	}
	writeTable(b, []string{"asker", "cost", "questions"}, playerRows)

	fmt.Fprintln(b, "\n  per day:")
	var dayRows [][]string
	for _, d := range dayCosts {
		dayRows = append(dayRows, []string{d.Day, money(d.Cost), fmt.Sprintf("%d", d.Questions)})
	}
	writeTable(b, []string{"day", "cost", "questions"}, dayRows)
}

func writeZeroLookup(b *strings.Builder, qs []ledger.QuestionRecord) {
	section(b, "ZERO-LOOKUP SHARE (the free tier's own KPI; briefing=on questions only)")
	rep := computeZeroLookupShare(qs)
	fmt.Fprintf(b, "  reported over the %d question(s) in this window with briefing=on\n", rep.OnCount)
	share, ok := rep.Share()
	if !ok {
		fmt.Fprintln(b, "  n/a: no question in this window has briefing=on yet (feature not shipped, or the operator has it off)")
		return
	}
	fmt.Fprintf(b, "  %d of %d answered with zero tool calls (%s)\n", rep.ZeroCount, rep.OnCount, pct(share))
}

func writeBriefingROI(b *strings.Builder, qs []ledger.QuestionRecord, rounds []ledger.RoundRecord) {
	section(b, "BRIEFING COST AGAINST ROUNDS SAVED (briefing=on questions only; falsifies Decision 1's cost argument)")
	rep := computeBriefingROI(qs, rounds)
	fmt.Fprintf(b, "  reported over the %d question(s) in this window with briefing=on\n", rep.OnQuestions)
	if rep.OnQuestions == 0 {
		fmt.Fprintln(b, "  n/a: no question in this window has briefing=on yet (feature not shipped, or the operator has it off)")
		return
	}
	fmt.Fprintf(b, "  briefing_tokens summed: %d (estimate, floor(bytes/4), never a reported figure)\n", rep.BriefingTokens)
	if rep.CostRateOK {
		fmt.Fprintf(b, "  estimated briefing cost: %s (at this window's own observed $/token)\n", money(rep.EstBriefingCost))
	} else {
		fmt.Fprintln(b, "  estimated briefing cost: n/a (no priced round in this window to derive a rate from)")
	}
	fmt.Fprintf(b, "  zero-lookup questions among them: %d\n", rep.ZeroLookup)
	if rep.RoundCostOK {
		fmt.Fprintf(b, "  estimated cost those rounds would have cost: %s (at this window's own average $/round)\n", money(rep.EstAvoidedCost))
	} else {
		fmt.Fprintln(b, "  estimated rounds-avoided cost: n/a (no round in this window to average)")
	}
	if rep.CostRateOK && rep.RoundCostOK {
		if rep.EstBriefingCost <= rep.EstAvoidedCost {
			fmt.Fprintln(b, "  the briefing's own cost argument holds in this window: briefing cost <= rounds it avoided")
		} else {
			fmt.Fprintln(b, "  the briefing's own cost argument does NOT hold in this window: briefing cost > rounds it avoided")
		}
	}
}

func writeAskBack(b *strings.Builder, qs []ledger.QuestionRecord) {
	section(b, "ASK-BACK RATE AND RESOLUTION RATE")
	rep := computeAskBack(qs)
	rate, ok := rep.AskBackRate()
	fmt.Fprintf(b, "  %d of %d question(s) were an ask-back (%s)\n", rep.AskedBack, rep.Total, pctOr(rate, ok))
	resRate, resOK := rep.ResolutionRate()
	fmt.Fprintf(b, "  %d of those %d resolved by a later reply in the same session (%s)\n", rep.Resolved, rep.AskedBack, pctOr(resRate, resOK))
}
