// Package agent is the loop: one question in, one artifact out.
//
// The shape of a round is fixed. The model gets the system prompt, the
// conversation so far and every tool the server exposes. It calls tools, all
// of a round's calls run at once and come back in one user message, and it
// ends by calling submit_answer in a round of its own: an answer written
// beside a read it has not seen yet is refused. Anything else that can end a
// question, a model that stops talking, a round cap, a token budget, a quota,
// ends it with an artifact too, so a player always gets an answer.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// defaultForce is the force a question is read against when the companion
// sent no force hint. It is Factorio's own default force name.
const defaultForce = "player"

const (
	quotaNotice       = "You have asked all the questions your hourly allowance covers. Try again a bit later."
	serverQuotaNotice = "The server has asked all the questions its hourly allowance covers. Try again later."
	budgetNotice      = "The server has spent its daily allowance for questions. Try again tomorrow."
	serverKey         = "server"
	roundsNotice      = "I ran out of rounds before I could answer that. Try asking something narrower."
	tokenNotice       = "That question ran past its token budget before I could answer. Try asking something narrower."
	stalledNotice     = "The model stopped without answering. Try asking again."
)

// What the model is told when a round submits an answer it cannot mean yet. A
// submission beside a read would have been written before the read came back,
// so it is refused rather than delivered.
const (
	besideReadsRefusal = "submit_answer cannot run in the same round as other tools: you would be answering " +
		"before their results reach you. Call the reads now, look at what they return, then call submit_answer on its own."
	secondSubmitRefusal = "only the first submit_answer of a round is used. This one was ignored."
)

// Question is one thing to answer, as the companion handed it over.
type Question struct {
	ID              int64
	Text            string
	PlayerIndex     *int
	PlayerName      string // empty when the asker is not a connected player
	Force           string
	Asker           string       // a label the caller chose, used when no player name came with the question
	Scope           string       // the chat scope key the companion resolved; "" means global
	Private         bool         // true when the scope is private to an audience
	Labels          []ForceLabel // names players use for forces, from the companion's labels op
	Surface         string       // the surface the asker was looking at, "" when unknown
	PhysicalSurface string       // where their character stood, when that differs
}

// ForceLabel is a force name and what players call it.
type ForceLabel struct {
	Name  string
	Label string
}

// scope is the session pool this question belongs to.
func (q Question) scope() string {
	if q.Scope == "" {
		return "global"
	}
	return q.Scope
}

// askerName is how the asker appears in a session transcript, where every
// exchange may come from a different player.
func (q Question) askerName() string {
	switch {
	case q.PlayerName != "":
		return q.PlayerName
	case q.Asker != "":
		return q.Asker
	case q.PlayerIndex != nil:
		return fmt.Sprintf("player %d", *q.PlayerIndex)
	}
	return "another mod"
}

func (q Question) force() string {
	if q.Force == "" {
		return defaultForce
	}
	return q.Force
}

// AskerLabel is how the asker is named to the model and in the log. The name
// leads when the companion sent one: history rows are keyed by player name, so
// a model that knows the name can ask history about this player by name.
func (q Question) AskerLabel() string {
	if q.PlayerName != "" {
		if q.PlayerIndex != nil {
			return fmt.Sprintf("%s (player %d, force %s)", q.PlayerName, *q.PlayerIndex, q.force())
		}
		return fmt.Sprintf("%s (force %s)", q.PlayerName, q.force())
	}
	if q.Asker != "" {
		return q.Asker
	}
	if q.PlayerIndex != nil {
		return fmt.Sprintf("player %d (force %s)", *q.PlayerIndex, q.force())
	}
	return fmt.Sprintf("another mod (force %s)", q.force())
}

// key is what memory and quota are counted against: the player when there is
// one, the force otherwise, so a mod asking on its own behalf shares one
// bucket rather than dodging the quota entirely.
func (q Question) key() string {
	if q.PlayerIndex != nil {
		return fmt.Sprintf("player:%d", *q.PlayerIndex)
	}
	return "force:" + q.force()
}

// Caps are the per-question limits the operator sets.
type Caps struct {
	MaxRounds                 int
	MaxTokensPerQuestion      int
	MaxToolResultBytes        int // a tool result longer than this is cut before the model sees it
	MaxRoundToolResultBytes   int // total tool-result bytes one round may add across every call; zero means DefaultMaxRoundToolResultBytes
	Sessions                  SessionCaps
	QuestionsPerPlayerPerHour int
	QuestionsPerHour          int     // the whole server's rolling-hour cap; zero or less means none
	MaxCostPerDay             float64 // USD in a rolling day; zero or less means none
	MaxToolCalls              int     // tool calls one question may make across its rounds; zero means DefaultMaxToolCalls
}

// DefaultMaxToolCalls bounds what one question may ask of the game. Six
// rounds of parallel calls could otherwise be dozens of surface scans.
const DefaultMaxToolCalls = 30

func (c Caps) maxToolCalls() int {
	if c.MaxToolCalls <= 0 {
		return DefaultMaxToolCalls
	}
	return c.MaxToolCalls
}

// DefaultMaxToolResultBytes is the cut applied when the caps leave it unset.
// The companion refuses a call reply past 8000 bytes; half of that is room
// for every bounded list at its default limit and about a thousand tokens
// of the model's context.
const DefaultMaxToolResultBytes = 4096

func (c Caps) toolResultBytes() int {
	if c.MaxToolResultBytes <= 0 {
		return DefaultMaxToolResultBytes
	}
	return c.MaxToolResultBytes
}

// DefaultMaxRoundToolResultBytes bounds the sum of every read's result bytes
// in one round (docs/design/phase4-spec.md section 13): three or four
// results each individually inside MaxToolResultBytes can still hand the
// model tens of thousands of bytes of freshly written text in one round, and
// this is that same per-result standard, CONTEXT.md invariant 4, applied one
// level up.
const DefaultMaxRoundToolResultBytes = 24000

func (c Caps) roundToolResultBytes() int {
	if c.MaxRoundToolResultBytes <= 0 {
		return DefaultMaxRoundToolResultBytes
	}
	return c.MaxRoundToolResultBytes
}

func (c Caps) maxRounds() int {
	if c.MaxRounds <= 0 {
		return 6
	}
	return c.MaxRounds
}

// Result is one answered question, and what it cost.
type Result struct {
	Artifact Artifact
	Rounds   int
	Usage    model.Usage
	CostUSD  float64
	Session  SessionMark // which session the question ran in
}

// SessionMark says which session a question ran in and whether it started
// it. It rides on the artifact as the `session` field, which the companion
// renders as a "(new session)" marker.
type SessionMark struct {
	Key   string `json:"-"`
	Name  string `json:"name"`
	Fresh bool   `json:"fresh"`
}

// Agent answers questions with one model, one set of caps, and the memory and
// quota that go with them. It is safe for concurrent use.
type Agent struct {
	mdl    model.Model
	caps   Caps
	sess   *sessions
	quota  *quota
	server *quota  // one key for the whole server
	budget *budget // USD spent in the rolling day
	now    func() time.Time

	// Trace, when set, gets one line per model round: what the round cost
	// and what the model spent its output on. Nil means no per-round lines.
	Trace func(format string, args ...any)

	// Ledger, when set to an enabled Writer, gets one QuestionRecord for
	// every question Answer finishes with and one RoundRecord for every
	// model round (docs/design/phase4-observability-spec.md sections 2-4).
	// A nil Ledger, or one opened disabled, makes every write a no-op:
	// ledger.Writer's own contract, not a nil check this package repeats.
	Ledger *ledger.Writer

	// Floor, when set, is the fastest recent RCON round trip: subtracted
	// from a tool call's own wall clock so a genuinely slow tool stands out
	// from ordinary network latency (the ledger's ms_above_floor). Nil
	// means nothing is subtracted, which is what every tool call gets when
	// this is left unset, tests included.
	Floor func() time.Duration

	// BriefingEnabled turns the per-question briefing on (docs/design/
	// phase4-spec.md section 3, the operator's briefing.enabled config key):
	// Assemble runs once before round 1, on its own BriefingBudget, and its
	// fenced text rides in the user turn ahead of the question. False (the
	// zero value New returns) skips Assemble entirely, matching the
	// pre-Phase-4 behaviour and recording ledger.BriefingOff for every
	// question, the value NewQuestionRecord already defaults to.
	BriefingEnabled bool

	// Chat, when set, is what Assemble asks for a briefing's last few
	// organic chat lines (the `ch` key). A nil Chat simply omits `ch`, the
	// same as any other Group A source that did not land.
	Chat ChatSource

	// Personality is the operator's voice setting (phase4-spec.md section
	// 16): "off", the default, or "factorio". It picks a fixed flavour
	// string the service owns, appended to the end of the system prompt, so
	// it is the same text for every question on this server and turning it
	// on costs one cache write rather than one per question.
	Personality string
}

func New(m model.Model, caps Caps) *Agent {
	return &Agent{
		mdl:    m,
		caps:   caps,
		sess:   newSessions(caps.Sessions),
		quota:  newQuota(caps.QuestionsPerPlayerPerHour),
		server: newQuota(caps.QuestionsPerHour),
		budget: newBudget(caps.MaxCostPerDay),
		now:    time.Now,
	}
}

// Model is the model id answers are being charged against.
func (a *Agent) Model() string { return a.mdl.Name() }

// Answer runs one question to an artifact. An error means the model itself
// failed and the caller decides what to tell the player; every other ending
// comes back as a Result.
func (a *Agent) Answer(ctx context.Context, q Question, ts []tools.Tool) (Result, error) {
	now := a.now()
	// Captured before substituteLabels rewrites q.Text for the model, so
	// every ledger line below logs the same text the companion sent,
	// whichever of the nine endings this question hits.
	rawText := q.Text
	req := parse(q.Text)
	mark := SessionMark{Key: sessionKey(q.scope(), req.Name), Name: req.Name}
	if req.Command != "" {
		res := a.command(q, req, mark, now)
		var reason *string
		refused := req.Command == CommandEmpty
		if refused {
			reason = strPtr(emptyQuestionNotice)
		}
		// "new" only ends the current session; sessions.open runs for the
		// question that follows, not this one, so the ledger's own
		// session_fresh (which means "sessions.open reported a fresh
		// session for this question") must not claim that yet. The
		// artifact's own session marker is left untouched: it correctly
		// tells the player their next question starts clean.
		ledgerSession := res.Session
		if req.Command == CommandNew {
			ledgerSession.Fresh = false
		}
		// askedBack and awaitingReplyResolved both stay false: a command
		// never runs sess.record ("new" ends the session outright, and
		// "sessions" only lists), so neither is this path's to claim,
		// whatever level its own Notice happens to carry.
		a.writeQuestion(a.questionRecord(q, rawText, ledgerSession, res.Artifact.Shape, 0, 0, 0, 0, 0, refused, reason, nil, zeroLookupFor(0, 0, res.Artifact), ledger.BriefingOff, 0, 0, 0, false, false))
		return res, nil
	}
	if !a.quota.take(q.key(), now) {
		res := Result{Artifact: refusal(quotaNotice), Session: mark}
		a.writeQuestion(a.questionRecord(q, rawText, res.Session, res.Artifact.Shape, 0, 0, 0, 0, 0, true, strPtr(quotaNotice), nil, zeroLookupFor(0, 0, res.Artifact), ledger.BriefingOff, 0, 0, 0, false, false))
		return res, nil
	}
	if !a.server.take(serverKey, now) {
		a.quota.refund(q.key(), now)
		res := Result{Artifact: refusal(serverQuotaNotice), Session: mark}
		a.writeQuestion(a.questionRecord(q, rawText, res.Session, res.Artifact.Shape, 0, 0, 0, 0, 0, true, strPtr(serverQuotaNotice), nil, zeroLookupFor(0, 0, res.Artifact), ledger.BriefingOff, 0, 0, 0, false, false))
		return res, nil
	}
	if a.budget.exhausted(now) {
		a.quota.refund(q.key(), now)
		a.server.refund(serverKey, now)
		res := Result{Artifact: refusal(budgetNotice), Session: mark}
		a.writeQuestion(a.questionRecord(q, rawText, res.Session, res.Artifact.Shape, 0, 0, 0, 0, 0, true, strPtr(budgetNotice), nil, zeroLookupFor(0, 0, res.Artifact), ledger.BriefingOff, 0, 0, 0, false, false))
		return res, nil
	}
	// wasAwaiting says whether this session was still waiting on the
	// player's reply to an ask-back (Decision 7) when this question landed;
	// open has already cleared the flag, since this question's arrival
	// resolves that wait whatever it turns out to ask. It rides through
	// every ending below as the ledger's awaiting_reply_resolved.
	earlier, fresh, wasAwaiting := a.sess.open(q.scope(), req.Name, now, req.Fresh)
	mark.Fresh = fresh
	q.Text = substituteLabels(req.Question, q.Labels)

	ctx = catalog.WithForce(ctx, q.force())
	byName := index(ts)
	defs := defsFor(ts)
	system := systemPrompt(voiceText(a.Personality))

	var msModel, msRCON int64
	// The briefing runs once per question, before round 1, on its own budget
	// (docs/design/phase4-spec.md section 3): most of its cost is RCON, not
	// model time, so it is folded into msRCON right here rather than carried
	// as a separate accumulator threaded through every ending. off (the
	// operator's config) never calls Assemble at all, and every question
	// still logs ledger.BriefingOff, the value NewQuestionRecord already
	// defaults to.
	briefingStatus := ledger.BriefingOff
	var briefingBytes, briefingTokens int
	var briefingMs int64
	briefingText := ""
	if a.BriefingEnabled {
		res := Assemble(ctx, byName, q, mark, a.Chat, BriefingBudget)
		briefingStatus = res.Status
		// briefing_ms is recorded whether or not the briefing landed. A
		// failed attempt still spent the trips, and zeroing it hid exactly
		// that: on 2026-09-13 the briefing failed on every question against
		// a live server and the ledger reported it costing nothing, so the
		// only evidence left was one log line. A cost this ledger cannot see
		// is a cost nobody will find.
		briefingMs = res.Elapsed.Milliseconds()
		// bytes and tokens stay zero unless the briefing actually reached
		// the user turn: they measure what the model was given, and a failed
		// attempt gave it nothing.
		if res.Status == ledger.BriefingOn {
			briefingText = res.Text
			briefingBytes = res.Bytes
			// floor(bytes/4), the estimator docs/design/
			// phase4-observability-spec.md names: not a figure the model
			// API reports back, but consistent across every question.
			briefingTokens = briefingBytes / 4
		}
	}
	// ms_rcon carries briefing_ms as its own summand (docs/design/
	// phase4-observability-spec.md); briefingMs is still zero above when
	// there was nothing to add, so this is a no-op on "off" or "failed".
	msRCON += briefingMs

	msgs := []model.Message{{
		Role:   model.RoleUser,
		Blocks: []model.Block{{Type: model.BlockText, Text: prompt(q, earlier, briefingText)}},
	}}

	var usage model.Usage
	toolCalls := 0
	// ledgerOn is tested once per round rather than letting each round
	// build a RoundRecord and every tool call clip its own args only for
	// Writer.write to discard them: a disabled ledger (or none wired) must
	// cost one bool test per round, not a wasted build. The per-question
	// record is still built on every path, because it copies a handful of
	// fields and clips nothing; only the per-round and per-call work, which
	// grows with the round and clips every argument, is worth gating.
	ledgerOn := a.Ledger.Enabled()
	// Round records accumulate here rather than writing as each round
	// finishes: the ledger only ever writes after the artifact that ends
	// the question already exists, never on the path that decides it
	// (spec section 5). Bounded by max_rounds, so this never buffers more
	// than one question's worth of rounds, and flushRounds (called from
	// finish and the model-error path below) is what actually writes them.
	rounds := make([]ledger.RoundRecord, 0, a.caps.maxRounds())
	for round := 1; round <= a.caps.maxRounds(); round++ {
		modelStart := time.Now()
		step, err := a.mdl.Step(ctx, system, msgs, defs)
		roundModelMs := time.Since(modelStart).Milliseconds()
		if err != nil {
			// The asker got no answer, so the slot goes back: a model
			// outage must not spend anyone's hourly allowance.
			a.quota.refund(q.key(), now)
			msModel += roundModelMs
			res := Result{Rounds: round, Usage: usage, CostUSD: CostUSD(a.mdl.Name(), usage), Session: mark}
			a.flushRounds(rounds)
			// The runner (cmd/aab/answer.go) turns this error into a
			// failure notice before it ever reaches the player, so the
			// ledger records the shape actually delivered, not the empty
			// value this branch has no artifact to read it from. refused
			// stays false: refused means the service turned the question
			// away before or instead of asking the model, and here the
			// model was asked; it just failed. zero_lookup is forced false
			// rather than run through zeroLookupFor: there is no delivered
			// artifact here to test, only the placeholder shape above, and
			// a provider outage is never a free-tier win regardless.
			errText := err.Error()
			a.writeQuestion(a.questionRecord(q, rawText, mark, ShapeNotice, round, toolCalls, res.CostUSD, msModel, msRCON, false, nil, &errText, false, briefingStatus, briefingBytes, briefingTokens, briefingMs, false, wasAwaiting))
			return res, err
		}
		usage.Add(step.Usage)
		msModel += roundModelMs
		a.traceRound(q, round, step)
		msgs = append(msgs, model.Message{Role: model.RoleAssistant, Blocks: step.Blocks})

		calls := model.ToolUses(step.Blocks)
		if len(calls) == 0 {
			if ledgerOn {
				rounds = append(rounds, a.roundRecord(q.ID, round, step, roundModelMs, 0, nil))
			}
			return a.finish(q, fromText(step), round, usage, mark, toolCalls, msModel, msRCON, rawText, rounds, briefingStatus, briefingBytes, briefingTokens, briefingMs, wasAwaiting), nil
		}
		var results []model.Block
		var calledTools []ledger.ToolCall
		var roundToolMs int64
		if reads, left := countReads(calls), a.caps.maxToolCalls()-toolCalls; reads > left {
			// The round is refused, not the question: the model is told what
			// is left and answers from what it has, or asks for less. Every
			// refused call still gets a ledger.ToolCall of its own (ms:0,
			// the refusal text as its error), so a round the cap refused
			// still shows up in the repeat-count report instead of leaving
			// it blind in exactly the case that motivates it.
			results, calledTools = refuseLookups(calls, reads, left, toolCalls, a.caps.maxToolCalls(), a.caps.toolResultBytes(), ledgerOn)
			if a.Trace != nil {
				a.Trace("question %d round %d: refused %d lookups, %d of %d used", q.ID, round, reads, toolCalls, a.caps.maxToolCalls())
			}
		} else {
			toolCalls += reads
			var artifact Artifact
			var submitted bool
			// A round's reads run concurrently (runReads fans them out over
			// goroutines), so the tool phase's own wall clock, not the sum
			// of each call's own ms, is what a round actually spent on
			// tools: summing would report N concurrent calls as N times the
			// wall clock that actually passed, inflating the RCON side of
			// the one report the ledger exists to settle.
			toolStart := time.Now()
			results, artifact, submitted, calledTools = runCalls(ctx, calls, byName, q.force(), a.caps.toolResultBytes(), a.caps.roundToolResultBytes(), a.Floor, ledgerOn)
			roundToolMs = time.Since(toolStart).Milliseconds()
			if reads > 0 {
				// A round that was nothing but a submission never dispatched
				// a read: the wall clock just measured is local (parsing the
				// submission), not RCON, so it must not bill to ms_rcon.
				msRCON += roundToolMs
			}
			if submitted {
				if ledgerOn {
					rounds = append(rounds, a.roundRecord(q.ID, round, step, roundModelMs, roundToolMs, calledTools))
				}
				return a.finish(q, artifact, round, usage, mark, toolCalls, msModel, msRCON, rawText, rounds, briefingStatus, briefingBytes, briefingTokens, briefingMs, wasAwaiting), nil
			}
		}
		if ledgerOn {
			rounds = append(rounds, a.roundRecord(q.ID, round, step, roundModelMs, roundToolMs, calledTools))
		}
		msgs = append(msgs, model.Message{Role: model.RoleUser, Blocks: results})

		if budget := a.caps.MaxTokensPerQuestion; budget > 0 && usage.Budgeted() >= budget {
			return a.finish(q, Notice(LevelWarning, tokenNotice), round, usage, mark, toolCalls, msModel, msRCON, rawText, rounds, briefingStatus, briefingBytes, briefingTokens, briefingMs, wasAwaiting), nil
		}
	}
	return a.finish(q, Notice(LevelWarning, roundsNotice), a.caps.maxRounds(), usage, mark, toolCalls, msModel, msRCON, rawText, rounds, briefingStatus, briefingBytes, briefingTokens, briefingMs, wasAwaiting), nil
}

// runCalls executes one round's tool calls and reports whether the round
// answered. submit_answer is handled here rather than as a tool, because it
// ends the loop: a round that mixes it with reads is refused, and a round that
// submits twice keeps the first submission in block order, so which answer
// reaches the player never depends on which goroutine finished first.
// The fourth return is this round's ledger entries, one per lookup it
// actually ran; a pure submit_answer round runs no lookups and returns nil.
// ledgerOn false skips building that fourth return's entries at all, down
// through runReads and read: the calls still run, only the ledger side of
// them is left out. roundBudget is the round-wide byte cap on top of limit's
// per-result one (docs/design/phase4-spec.md section 13): limit bounds one
// result, roundBudget bounds what every result in the round adds up to.
func runCalls(ctx context.Context, calls []model.Block, byName map[string]tools.Tool, force string, limit, roundBudget int, floor func() time.Duration, ledgerOn bool) ([]model.Block, Artifact, bool, []ledger.ToolCall) {
	results := make([]model.Block, len(calls))
	submits, reads := partition(calls)

	if len(reads) > 0 {
		for _, i := range submits {
			results[i] = failed(calls[i].ID, errors.New(besideReadsRefusal))
		}
		calledTools := runReads(ctx, calls, reads, results, byName, force, limit, roundBudget, floor, ledgerOn)
		return results, Artifact{}, false, calledTools
	}

	for n, i := range submits {
		artifact, err := parseSubmission(calls[i].Input)
		if err != nil {
			results[i] = failed(calls[i].ID, err)
			continue
		}
		results[i] = model.Block{Type: model.BlockToolResult, ID: calls[i].ID, Content: "sent"}
		for _, later := range submits[n+1:] {
			results[later] = failed(calls[later].ID, errors.New(secondSubmitRefusal))
		}
		return results, artifact, true, nil
	}
	return results, Artifact{}, false, nil
}

// countReads is how many of a round's calls are lookups; a submission is
// not one, so a model that has used its lookups can still answer.
func countReads(calls []model.Block) int {
	_, reads := partition(calls)
	return len(reads)
}

// refuseLookups is a round's results when its reads would pass the cap: every
// call gets the same refusal, which says what is left, so the model answers
// from what it has or asks for less. A submission beside the reads is refused
// with them, as it would have been anyway, but it is not a lookup and never
// gets a ledger.ToolCall of its own: partition puts it in the same slice as
// the reads, and the reports built from tool_calls read it by name, so a
// phantom submit_answer entry would misreport what actually ran. The second
// return is the refusal recorded as a ledger.ToolCall per refused read
// (clipped args, ok:false, ms:0, the refusal text as error): the call never
// ran, but the model asked for it, and the repeat-count report needs to see
// that it asked. Built with append, not indexed by i, so a skipped
// submission never leaves a zero-value gap. ledgerOn false skips building
// any of them.
func refuseLookups(calls []model.Block, asked, left, used, cap, limit int, ledgerOn bool) ([]model.Block, []ledger.ToolCall) {
	text := fmt.Sprintf("Refused: this round asked for %d lookups but only %d remain for this question (%d of %d used). "+
		"Answer now from what you already have, saying what you could not check, or ask for at most %d.", asked, left, used, cap, left)
	if left <= 0 {
		text = fmt.Sprintf("Refused: no lookups remain for this question (%d of %d used). "+
			"Answer now from what you already have and say what you could not check.", used, cap)
	}
	results := make([]model.Block, len(calls))
	var calledTools []ledger.ToolCall
	for i, call := range calls {
		results[i] = failed(call.ID, errors.New(text))
		if !ledgerOn || call.Name == SubmitTool {
			continue
		}
		calledTools = append(calledTools, ledger.ToolCall{
			Name:  call.Name,
			Args:  content(call.Input, limit),
			OK:    false,
			Error: strPtr(text),
		})
	}
	return results, calledTools
}

// partition splits one round's calls into submissions and reads, keeping the
// order the model produced them in.
func partition(calls []model.Block) (submits, reads []int) {
	for i, call := range calls {
		if call.Name == SubmitTool {
			submits = append(submits, i)
			continue
		}
		reads = append(reads, i)
	}
	return submits, reads
}

// runReads runs a round's reads at once and writes each result into its own
// slot, so the results come back in the order the model asked for them. The
// ledger entry for each read is built in the same slot order. ledgerOn false
// leaves calledTools nil: every read still runs, only its ledger entry is
// skipped. Once every read is in, enforceRoundBudget walks them in that same
// call order and refuses whichever ones would push the round's total bytes
// past roundBudget; every call still ran, so the ledger's own record of it
// (calledTools) is left exactly as read built it, and only the block the
// model sees is replaced.
func runReads(ctx context.Context, calls []model.Block, reads []int, results []model.Block, byName map[string]tools.Tool, force string, limit, roundBudget int, floor func() time.Duration, ledgerOn bool) []ledger.ToolCall {
	var calledTools []ledger.ToolCall
	if ledgerOn {
		calledTools = make([]ledger.ToolCall, len(reads))
	}
	var wg sync.WaitGroup
	for n, i := range reads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			block, tc := read(ctx, calls[i], byName, force, limit, floor, ledgerOn)
			results[i] = block
			if ledgerOn {
				calledTools[n] = tc
			}
		}()
	}
	wg.Wait()
	enforceRoundBudget(results, reads, roundBudget)
	return calledTools
}

// enforceRoundBudget is section 13's per-round tool-result byte cap: reads
// are counted in the order the model asked for them (the same call order
// runReads already promises above, not goroutine completion order, which is
// not deterministic), a result that keeps the running total at or under cap
// is kept exactly as the tool returned it, and the moment one more result
// would push the total past cap, that result is refused instead, in the
// same {refused, why} voice as the tier 2 needs_anchor refusal (section 11).
// A cap of zero or less (Caps.roundToolResultBytes never actually produces
// one, but a directly built Caps{} might) turns the check off rather than
// refusing every read outright.
func enforceRoundBudget(results []model.Block, reads []int, cap int) {
	if cap <= 0 {
		return
	}
	spent := 0
	for _, i := range reads {
		n := len(results[i].Content)
		if spent+n <= cap {
			spent += n
			continue
		}
		results[i] = model.Block{
			Type: model.BlockToolResult,
			ID:   results[i].ID,
			Content: fmt.Sprintf(
				`{"refused":"round_budget","why":"this round has already spent %d of its %d-byte tool-result budget; %d bytes are left, not enough for this result"}`,
				spent, cap, cap-spent),
		}
	}
}

// read runs one tool. Every tool that declares force gets the asker's force
// filled in when the model left it out, game tools and history tools alike,
// which is what the system prompt promises the model. Alongside the model's
// own result, it always returns this call's ledger entry, known tool or not,
// so every lookup the model asked for shows up in the ledger exactly once;
// ledgerOn false makes that second return the zero value instead, skipping
// the args clip and the Floor call that building a real one costs.
func read(ctx context.Context, call model.Block, byName map[string]tools.Tool, force string, limit int, floor func() time.Duration, ledgerOn bool) (model.Block, ledger.ToolCall) {
	started := time.Now()
	t, known := byName[call.Name]
	if !known {
		err := fmt.Errorf("there is no tool named %q", call.Name)
		return failed(call.ID, err), maybeToolCallRecord(ledgerOn, call.Name, call.Input, limit, time.Since(started), floor, 0, err)
	}
	args := call.Input
	if tools.Declares(t.Schema, catalog.ForceParam) {
		args = tools.FillString(args, catalog.ForceParam, force)
	}
	out, err := t.Call(ctx, args)
	ms := time.Since(started)
	if err != nil {
		return failed(call.ID, err), maybeToolCallRecord(ledgerOn, call.Name, args, limit, ms, floor, 0, err)
	}
	return model.Block{Type: model.BlockToolResult, ID: call.ID, Content: content(out, limit)},
		maybeToolCallRecord(ledgerOn, call.Name, args, limit, ms, floor, len(out), nil)
}

// finish clips the artifact, remembers the exchange, prices the question,
// flushes the question's accumulated round records and writes its own ledger
// line, in that order, once the artifact that ends the question already
// exists. rawText is what the companion actually sent, for the ledger; q.Text
// may already be the label-substituted form the model saw. The four
// briefing* arguments are what Answer computed once before round 1
// (docs/design/phase4-observability-spec.md's briefing/briefing_bytes/
// briefing_tokens/briefing_ms fields); finish only carries them into the
// record, it never decides them. awaitingReplyResolved is Answer's own
// wasAwaiting, whether this session was still waiting on a reply to an
// ask-back when this question landed (section 12); it becomes the ledger's
// awaiting_reply_resolved unchanged.
func (a *Agent) finish(q Question, artifact Artifact, rounds int, usage model.Usage, mark SessionMark, lookups int, msModel, msRCON int64, rawText string, roundRecords []ledger.RoundRecord, briefingStatus string, briefingBytes, briefingTokens int, briefingMs int64, awaitingReplyResolved bool) Result {
	clipped, err := validate(artifact)
	if err != nil {
		clipped = Notice(LevelWarning, stalledNotice)
	}
	clipped.Session = &mark
	cost := CostUSD(a.mdl.Name(), usage)
	a.budget.spend(cost, a.now())
	// askedBack renews the session's clarify_idle window when this answer is
	// itself another ask-back, and clears it (a no-op, since open already
	// cleared it on the way in) otherwise: record always takes the current
	// answer's own verdict, never leaves the flag as open left it.
	askedBack := isAskBack(clipped)
	a.sess.record(q.scope(), mark.Name, Exchange{
		Asker: q.askerName(), Question: q.Text, Answer: clipped.Plain(), At: a.now(),
	}, askedBack)
	a.flushRounds(roundRecords)
	a.writeQuestion(a.questionRecord(q, rawText, mark, clipped.Shape, rounds, lookups, cost, msModel, msRCON, false, nil, nil, zeroLookupFor(rounds, lookups, clipped), briefingStatus, briefingBytes, briefingTokens, briefingMs, askedBack, awaitingReplyResolved))
	return Result{
		Artifact: clipped,
		Rounds:   rounds,
		Usage:    usage,
		CostUSD:  cost,
		Session:  mark,
	}
}

// isAskBack is the ask-back signal (docs/design/phase4-spec.md section 12):
// the model's own submit_answer, shaped as the notice/confirmation pair the
// submit_answer schema already offers alongside notice/warning (submit.go).
// It is the narrowest signal the artifact carries, and the one the spec
// itself names: a loop-generated notice is always LevelWarning (roundsNotice,
// tokenNotice, stalledNotice, the quota and budget refusals), never
// LevelConfirmation, so this reads only what the model itself chose, never
// the loop talking about itself. What it can miss: the system prompt does
// not yet tell the model to pick level confirmation specifically for a
// clarifying question (today it only says "ask which surface in a notice",
// naming no level), so a model that asks back as a LevelWarning notice
// instead is not caught here and the session's window stays the ordinary
// one. What it can over-catch: a command's own LevelConfirmation notices
// ("Started a new session", "No sessions in your scope") never reach this
// function at all, since a.command returns before sess.record is ever
// called, so they cannot mark a session awaiting a reply that was never
// asked for.
func isAskBack(a Artifact) bool {
	return a.Shape == ShapeNotice && a.Level == LevelConfirmation
}

// fromText rescues an answer from a turn with no tool calls in it. A model
// that finished talking gets its text wrapped as a summary; one that stopped
// for any other reason gets a notice, because its text is likely a fragment.
func fromText(step model.Step) Artifact {
	if step.StopReason != model.StopEndTurn {
		return Notice(LevelWarning, stalledNotice)
	}
	// Blank lines go before the clip, or a model that opened with a newline
	// would spend the three summary lines on nothing.
	lines := written(strings.Split(model.TextOf(step.Blocks), "\n"))
	if len(lines) == 0 {
		return Notice(LevelWarning, stalledNotice)
	}
	return Summary(lines...)
}

// written keeps the lines that have something on them.
func written(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseSubmission(input json.RawMessage) (Artifact, error) {
	var artifact Artifact
	if err := json.Unmarshal(input, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("that is not a valid artifact: %v", err)
	}
	return validate(artifact)
}

func failed(id string, err error) model.Block {
	return model.Block{Type: model.BlockToolResult, ID: id, Content: err.Error(), IsError: true}
}

// content is what the model sees of a tool's return value. An empty return is
// shown as null rather than as nothing at all, which reads as a broken tool.
// content is the text the model reads for one tool result: the JSON as the
// tool returned it, cut at limit bytes. The cut lands on a rune boundary and
// says what it did, so the model asks for fewer rows instead of guessing at
// what fell off the end. Every token here is paid for on every later round.
func content(out json.RawMessage, limit int) string {
	if len(out) == 0 {
		return "null"
	}
	if limit <= 0 || len(out) <= limit {
		return string(out)
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[cut: %d of %d bytes shown; ask for fewer rows]", out[:cut], cut, len(out))
}

func index(ts []tools.Tool) map[string]tools.Tool {
	byName := make(map[string]tools.Tool, len(ts))
	for _, t := range ts {
		byName[t.Name] = t
	}
	return byName
}
