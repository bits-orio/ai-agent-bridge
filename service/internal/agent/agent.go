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
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// defaultForce is the force a question is read against when the companion
// sent no force hint. It is Factorio's own default force name.
const defaultForce = "player"

const (
	quotaNotice   = "You have asked all the questions your hourly allowance covers. Try again a bit later."
	roundsNotice  = "I ran out of rounds before I could answer that. Try asking something narrower."
	tokenNotice   = "That question ran past its token budget before I could answer. Try asking something narrower."
	stalledNotice = "The model stopped without answering. Try asking again."
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
	ID          int64
	Text        string
	PlayerIndex *int
	PlayerName  string // empty when the asker is not a connected player
	Force       string
	Asker       string       // a label the caller chose, used when no player name came with the question
	Scope       string       // the chat scope key the companion resolved; "" means global
	Private     bool         // true when the scope is private to an audience
	Labels      []ForceLabel // names players use for forces, from the companion's labels op
	Surface     string       // the surface the asker stood on, "" when unknown
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
	Sessions                  SessionCaps
	QuestionsPerPlayerPerHour int
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
	mdl   model.Model
	caps  Caps
	sess  *sessions
	quota *quota
	now   func() time.Time

	// Trace, when set, gets one line per model round: what the round cost
	// and what the model spent its output on. Nil means no per-round lines.
	Trace func(format string, args ...any)
}

func New(m model.Model, caps Caps) *Agent {
	return &Agent{
		mdl:   m,
		caps:  caps,
		sess:  newSessions(caps.Sessions),
		quota: newQuota(caps.QuestionsPerPlayerPerHour),
		now:   time.Now,
	}
}

// Model is the model id answers are being charged against.
func (a *Agent) Model() string { return a.mdl.Name() }

// Answer runs one question to an artifact. An error means the model itself
// failed and the caller decides what to tell the player; every other ending
// comes back as a Result.
func (a *Agent) Answer(ctx context.Context, q Question, ts []tools.Tool) (Result, error) {
	now := a.now()
	req := parse(q.Text)
	mark := SessionMark{Key: sessionKey(q.scope(), req.Name), Name: req.Name}
	if req.Command != "" {
		return a.command(q, req, mark, now), nil
	}
	if !a.quota.take(q.key(), now) {
		return Result{Artifact: Notice(LevelWarning, quotaNotice), Session: mark}, nil
	}
	earlier, fresh := a.sess.open(q.scope(), req.Name, now, req.Fresh)
	mark.Fresh = fresh
	q.Text = substituteLabels(req.Question, q.Labels)

	ctx = catalog.WithForce(ctx, q.force())
	byName := index(ts)
	defs := defsFor(ts)
	system := systemPrompt(q)
	msgs := []model.Message{{
		Role:   model.RoleUser,
		Blocks: []model.Block{{Type: model.BlockText, Text: prompt(q, earlier)}},
	}}

	var usage model.Usage
	for round := 1; round <= a.caps.maxRounds(); round++ {
		step, err := a.mdl.Step(ctx, system, msgs, defs)
		if err != nil {
			// The asker got no answer, so the slot goes back: a model
			// outage must not spend anyone's hourly allowance.
			a.quota.refund(q.key(), now)
			return Result{Rounds: round, Usage: usage, CostUSD: CostUSD(a.mdl.Name(), usage), Session: mark}, err
		}
		usage.Add(step.Usage)
		a.traceRound(q, round, step)
		msgs = append(msgs, model.Message{Role: model.RoleAssistant, Blocks: step.Blocks})

		calls := model.ToolUses(step.Blocks)
		if len(calls) == 0 {
			return a.finish(q, fromText(step), round, usage, mark), nil
		}

		results, artifact, submitted := runCalls(ctx, calls, byName, q.force(), a.caps.toolResultBytes())
		if submitted {
			return a.finish(q, artifact, round, usage, mark), nil
		}
		msgs = append(msgs, model.Message{Role: model.RoleUser, Blocks: results})

		if budget := a.caps.MaxTokensPerQuestion; budget > 0 && usage.Total() >= budget {
			return a.finish(q, Notice(LevelWarning, tokenNotice), round, usage, mark), nil
		}
	}
	return a.finish(q, Notice(LevelWarning, roundsNotice), a.caps.maxRounds(), usage, mark), nil
}

// runCalls executes one round's tool calls and reports whether the round
// answered. submit_answer is handled here rather than as a tool, because it
// ends the loop: a round that mixes it with reads is refused, and a round that
// submits twice keeps the first submission in block order, so which answer
// reaches the player never depends on which goroutine finished first.
func runCalls(ctx context.Context, calls []model.Block, byName map[string]tools.Tool, force string, limit int) ([]model.Block, Artifact, bool) {
	results := make([]model.Block, len(calls))
	submits, reads := partition(calls)

	if len(reads) > 0 {
		for _, i := range submits {
			results[i] = failed(calls[i].ID, errors.New(besideReadsRefusal))
		}
		runReads(ctx, calls, reads, results, byName, force, limit)
		return results, Artifact{}, false
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
		return results, artifact, true
	}
	return results, Artifact{}, false
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
// slot, so the results come back in the order the model asked for them.
func runReads(ctx context.Context, calls []model.Block, reads []int, results []model.Block, byName map[string]tools.Tool, force string, limit int) {
	var wg sync.WaitGroup
	for _, i := range reads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = read(ctx, calls[i], byName, force, limit)
		}()
	}
	wg.Wait()
}

// read runs one tool. Every tool that declares force gets the asker's force
// filled in when the model left it out, game tools and history tools alike,
// which is what the system prompt promises the model.
func read(ctx context.Context, call model.Block, byName map[string]tools.Tool, force string, limit int) model.Block {
	t, known := byName[call.Name]
	if !known {
		return failed(call.ID, fmt.Errorf("there is no tool named %q", call.Name))
	}
	args := call.Input
	if tools.Declares(t.Schema, catalog.ForceParam) {
		args = tools.FillString(args, catalog.ForceParam, force)
	}
	out, err := t.Call(ctx, args)
	if err != nil {
		return failed(call.ID, err)
	}
	return model.Block{Type: model.BlockToolResult, ID: call.ID, Content: content(out, limit)}
}

// finish clips the artifact, remembers the exchange and prices the question.
func (a *Agent) finish(q Question, artifact Artifact, rounds int, usage model.Usage, mark SessionMark) Result {
	clipped, err := validate(artifact)
	if err != nil {
		clipped = Notice(LevelWarning, stalledNotice)
	}
	clipped.Session = &mark
	a.sess.record(q.scope(), mark.Name, Exchange{
		Asker: q.askerName(), Question: q.Text, Answer: clipped.Plain(), At: a.now(),
	})
	return Result{
		Artifact: clipped,
		Rounds:   rounds,
		Usage:    usage,
		CostUSD:  CostUSD(a.mdl.Name(), usage),
		Session:  mark,
	}
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
