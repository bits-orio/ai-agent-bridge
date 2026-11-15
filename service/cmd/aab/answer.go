// Running one question and getting its artifact into the game.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/openrouter"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// maxDeliveries is how many times one answer is offered to the companion before
// the service stops trying (review-fix contract 3). The artifact is already paid
// for, so it is worth a retry; it is not worth retrying forever.
const maxDeliveries = 3

const modelFailedNotice = "I could not reach the model just now. Try again in a moment."

// outOfCreditNotice is what the asker is told when OpenRouter answers 402:
// trying again cannot help, and the one person who can fix it is the owner.
const outOfCreditNotice = "The model account is out of credit. The server owner needs to top it up before I can answer."

// creditsURL is where the owner adds credit. The log says so beside every
// refused question, since an in-game notice cannot carry a link.
const creditsURL = "https://openrouter.ai/settings/credits"

// failureNotice is the notice for a model error: most are transient, a 402
// is not.
func failureNotice(err error) string {
	if openrouter.IsOutOfCredit(err) {
		return outOfCreditNotice
	}
	return modelFailedNotice
}

// badArtifactNotice is what the asker is told when the companion cannot render
// the answer. Something has to reach the player: the round was paid for, and
// silence reads as the whole service being broken (second review-fix contract 3).
const badArtifactNotice = "I could not put that answer into a shape the game can show."

// answerPage takes one polled page as far as it can go this tick: run what is
// new, deliver what is waiting, and leave alone what is finished with. It
// stops the moment the companion goes out of reach (lost): nothing more is
// paid for or sent until the next reply has started the service again. It
// stops too once the service is shutting down (ctx done): a question run on
// a cancelled context would only write a failure to the ledger and be billed
// again after the restart.
//
// The catalog is read once for the whole page, on the first question that needs
// it, so a tick costs one providers round trip rather than one per question.
func (r *runner) answerPage(ctx context.Context, questions rpc.PollReply) {
	var game []tools.Tool
	var haveCatalog, readCatalog bool
	for _, q := range questions {
		if r.lost || ctx.Err() != nil {
			return
		}
		state := r.inFlight[q.ID]
		if state == nil {
			if !readCatalog {
				game, haveCatalog = r.toolsFor(ctx)
				readCatalog = true
			}
			if !haveCatalog {
				r.waitForCatalog(q.ID)
				continue
			}
			state = r.runGuarded(ctx, q, game)
			if state == nil {
				return // cut off by shutdown: left for the next start
			}
			r.inFlight[q.ID] = state
		}
		if state.done {
			continue
		}
		r.deliverGuarded(ctx, q, state)
	}
}

// runGuarded runs one question, and turns a panic in it into a notice for
// the asker, logged with its stack: left alone, the panic ends the process,
// the supervisor starts it again, the cursor starts again from the first
// pending question, and the same question runs, and is billed, again, for
// ever. A question that went past the re-run cap (S9) is not run at all. It
// returns nil for a question shutdown cut off.
func (r *runner) runGuarded(ctx context.Context, q rpc.Question, game []tools.Tool) (state *delivery) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("question %d: panic while answering, sending a notice and never running it again: %v\n%s", q.ID, p, debug.Stack())
			state = &delivery{result: agent.Result{Artifact: agent.Notice(agent.LevelWarning, panicNotice)}}
		}
	}()
	if !r.mayRun(q) {
		log.Printf("question %d: run %d times already, each lost to an outage; sending a notice instead of running it again", q.ID, maxRuns)
		return &delivery{result: agent.Result{Artifact: agent.Notice(agent.LevelWarning, reRunNotice)}}
	}
	result, ok := r.run(ctx, q, game)
	if !ok {
		return nil
	}
	return &delivery{result: result}
}

// deliverGuarded is deliver behind the same recover as runGuarded: a panic
// on the way out finishes the question with what it had, a notice if the
// companion takes one, so it is never offered to the model again.
func (r *runner) deliverGuarded(ctx context.Context, q rpc.Question, state *delivery) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("answer %d: panic while delivering, finishing the question with a notice: %v\n%s", q.ID, p, debug.Stack())
			state.done = true
			func() {
				defer func() { _ = recover() }()
				_, _ = r.rpc.AnswerOn(ctx, r.conn, q.ID, agent.Notice(agent.LevelWarning, panicNotice))
			}()
		}
	}()
	r.deliver(ctx, q, state)
}

// maxRuns is how many times one question may be run, the first run and two
// re-runs, when every outage drops the answer held for it (startAgain).
const maxRuns = 3

// mayRun counts one more run of q and reports whether it is allowed. A
// question is known by the map it was asked on, its id and its text: ids
// start again on every map, and the text tells apart two maps that share an
// id. The count lives in memory only and is cleared when it grows large: it
// only has to outlast a few outages in a row.
func (r *runner) mayRun(q rpc.Question) bool {
	if r.runs == nil || len(r.runs) > 1000 {
		r.runs = map[runKey]int{}
	}
	k := runKey{mapID: r.lastMap, qid: q.ID, text: q.Text}
	if r.runs[k] >= maxRuns {
		return false
	}
	r.runs[k]++
	return true
}

// runKey names one question across the resets that drop what is held for it.
type runKey struct {
	mapID rpc.MapID
	qid   int64
	text  string
}

// questionTimeout is agent.question_timeout, with the default for a config
// built by hand, as tests do.
func (r *runner) questionTimeout() time.Duration {
	if d := r.cfg.QuestionTimeout(); d > 0 {
		return d
	}
	return 90 * time.Second
}

// run is the part the operator pays for: the agent loop. Every ending produces
// an artifact, so a model that fails still has a notice to deliver rather than
// leaving the asker waiting. The run has its own deadline, so one slow
// question cannot hold every other player for minutes; past it the asker is
// told so, and the question is finished with like any other.
//
// It reports false when shutdown cut the run off: nothing came of it to
// deliver, and the question stays unanswered for the next start.
func (r *runner) run(ctx context.Context, q rpc.Question, game []tools.Tool) (agent.Result, bool) {
	question := agentQuestion(q)
	question.Labels = r.labelsFor(ctx)
	log.Printf("question %d from %s: %q", q.ID, logSafe(question.AskerLabel()), q.Text)

	qctx, cancel := context.WithTimeout(ctx, r.questionTimeout())
	defer cancel()
	result, err := r.agent.Answer(qctx, question, game)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			// The service is stopping, not the model failing: say nothing to
			// the asker, and leave the question for the restart.
			log.Printf("question %d: cut off by shutdown, left unanswered for the next start", q.ID)
			return agent.Result{}, false
		case errors.Is(qctx.Err(), context.DeadlineExceeded):
			// The question's own deadline, never an outage: the companion was
			// not asked anything that failed, so r.lost stays as it was.
			log.Printf("question %d: stopped at its %s deadline (agent.question_timeout): %v", q.ID, r.questionTimeout(), err)
			result.Artifact = agent.Notice(agent.LevelWarning, questionTimeoutNotice)
		default:
			log.Printf("question %d: the model failed: %v", q.ID, err)
			if openrouter.IsOutOfCredit(err) {
				log.Printf("question %d: OpenRouter has no credit left; add some at %s", q.ID, creditsURL)
			}
			result.Artifact = agent.Notice(agent.LevelWarning, failureNotice(err))
		}
	}
	if result.Session.Fresh {
		log.Printf("question %d: new session %s", q.ID, logSafe(result.Session.Key))
	}
	r.stats.RecordAnswer(result.Usage, result.CostUSD)
	r.answered++
	r.lastActivity = time.Now()
	return result, true
}

// shutdownDelivery bounds the one delivery a stopping service still makes:
// an answer already paid for.
const shutdownDelivery = 5 * time.Second

// deliver offers one artifact to the companion, over the RCON connection the
// question was polled on and no other (rpc.Client.AnswerOn): a server that
// restarted in between may have loaded a save where the same id is someone
// else's question, in someone else's scope. A delivery the companion refused
// or failed to confirm is tried again on the next tick with the artifact
// already in hand: the model is never asked the same question twice. Three
// failures and the service stops trying, with a line saying so. A delivery
// that got no reply at all puts the companion out of reach, and the next
// reply starts the service again with nothing held (startAgain).
func (r *runner) deliver(ctx context.Context, q rpc.Question, state *delivery) {
	stopping := ctx.Err() != nil
	if stopping {
		// Shutdown began after the answer was paid for: it still goes out,
		// on a context the signal does not cancel, for a few seconds at most.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), shutdownDelivery)
		defer cancel()
	}
	state.attempts++
	state.result.Artifact = artFor(r.modVersion, state.result.Artifact)
	delivered, err := r.rpc.AnswerOn(ctx, r.conn, q.ID, state.result.Artifact)
	if err == nil && !delivered {
		// The companion replied without confirming. Treated as a failed
		// delivery, so the three-attempt budget applies instead of the answer
		// being dropped on the floor.
		err = errors.New("the companion did not confirm it")
	}
	switch {
	case err == nil:
		state.done = true
		u := state.result.Usage
		log.Printf("answer %d shape=%s rounds=%d tokens=%d/%d cached=%d/%d cost=$%.4f",
			q.ID, state.result.Artifact.Shape, state.result.Rounds,
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, state.result.CostUSD)
		// The artifact as sent, so an odd answer in game can be traced to
		// what the model wrote rather than guessed at. At most 12 KB.
		if raw, err := json.Marshal(state.result.Artifact); err == nil {
			log.Printf("answer %d artifact %s", q.ID, raw)
		}
		// And the lines as the player saw them. The artifact says team-2; the
		// companion prints a label in a colour, or plain, or with a torn tag,
		// and none of that is visible from the JSON: six rounds of fixes to
		// colours and pings were reported as done from the artifact line and
		// turned out wrong on screen. Best effort, one extra read.
		if !stopping {
			r.logRendered(ctx, q.ID, state.result.Artifact)
		}
	case rpc.HasCode(err, rpc.CodeBadArtifact):
		r.refused(ctx, q, state, err)
	case rpc.Unreachable(err):
		r.lost = true
		// Nobody saw this answer, so a follow-up must not read it back.
		r.agent.AmendSession(state.result, nil)
		log.Printf("answer %d: dropped unsent, no reply or a new connection since the poll: after a restart this id may name another question: %v", q.ID, err)
	case state.attempts >= maxDeliveries:
		state.done = true
		r.agent.AmendSession(state.result, nil)
		log.Printf("answer %d: giving up after %d failed deliveries: %v", q.ID, state.attempts, err)
	default:
		log.Printf("answer %d: could not deliver it, trying again next tick: %v", q.ID, err)
	}
}

// refused handles the one refusal that is the artifact's own fault: the companion
// cannot render this shape, so sending it again would fail the same way
// (review-fix contract 5). The asker gets a notice instead, once, which the
// companion renders and marks the question answered on. Either way the question
// is finished with, so the cursor moves past it next tick.
func (r *runner) refused(ctx context.Context, q rpc.Question, state *delivery, cause error) {
	state.done = true
	log.Printf("answer %d: the companion refused the artifact, sending a notice instead: %v", q.ID, cause)

	state.result.Artifact = agent.Notice(agent.LevelWarning, badArtifactNotice)
	delivered, err := r.rpc.AnswerOn(ctx, r.conn, q.ID, state.result.Artifact)
	if err != nil {
		r.lost = r.lost || rpc.Unreachable(err)
		log.Printf("answer %d: the notice did not land either: %v", q.ID, err)
		r.agent.AmendSession(state.result, nil)
		return
	}
	if !delivered {
		log.Printf("answer %d: the companion did not confirm the notice", q.ID)
		r.agent.AmendSession(state.result, nil)
		return
	}
	// The session holds what the player saw: the notice, never the answer
	// the companion refused.
	shown := state.result.Artifact
	r.agent.AmendSession(state.result, &shown)
}

// agentQuestion maps one polled question onto the agent's question type. The
// asker's name travels with it, which is what lets the label name a player:
// history rows are keyed by player name, so "when did I last die" can only be
// scoped to the person who asked when the prompt carries their name (review-fix
// contract 9). The label itself is built in one place, agent.Question.AskerLabel,
// so the log line and the system prompt always name the asker the same way.
// The map's voice setting travels too: the agent resolves it per question,
// over the config's own.
//
// In asker mode (aab-answer-audience = asker) the companion prints the answer
// to the asker alone and marks the question to_asker. Such a question is
// private to that asker, under the key history stores it under
// (history.AskerKey): its follow-ups share a session with that asker's own
// and no one else's, and it reads that asker's private rows and the public
// ones, nobody else's.
func agentQuestion(q rpc.Question) agent.Question {
	scope, private := q.Scope, q.Private
	if q.ToAsker {
		name := q.PlayerName
		if name == "" && q.PlayerIndex != nil {
			name = fmt.Sprintf("#%d", *q.PlayerIndex)
		}
		scope, private = history.AskerKey(name), true
	}
	return agent.Question{
		ID:              q.ID,
		Text:            q.Text,
		PlayerIndex:     q.PlayerIndex,
		PlayerName:      q.PlayerName,
		Force:           q.Force,
		ViewingForce:    q.ViewingForce,
		WatchingForce:   q.WatchingForce,
		Scope:           scope,
		Private:         private,
		Surface:         q.Surface,
		PhysicalSurface: q.PhysicalSurface,
		Personality:     q.Personality,
	}
}

// labelsFor reads what players call each force, fresh for every question:
// one small RCON call, and a team renamed a minute ago is named right. A
// companion too old to know the op, or a failed read, means no labels and a
// line in the log once per streak.
func (r *runner) labelsFor(ctx context.Context) []agent.ForceLabel {
	rows, err := r.rpc.Labels(ctx)
	if err != nil {
		if !r.labelsFailed {
			log.Printf("labels: not available, answering without force labels: %v", err)
			r.labelsFailed = true
		}
		return nil
	}
	r.labelsFailed = false
	out := make([]agent.ForceLabel, 0, len(rows))
	for _, row := range rows {
		if row.Name != "" && row.Label != "" {
			out = append(out, agent.ForceLabel{Name: row.Name, Label: row.Label})
		}
	}
	return out
}

// logRendered reads back what the companion actually printed for one
// answered question and logs it, one printed line per log line, indented
// under the header (a table's own cells are split by " | ", so the lines
// cannot share that separator and stay readable). A failure here is logged
// and changes nothing: the answer is already delivered.
func (r *runner) logRendered(ctx context.Context, qid int64, sent agent.Artifact) {
	raw, err := r.rpc.Call(ctx, "answers", map[string]any{"after": qid - 1, "limit": 1})
	if err != nil {
		log.Printf("answer %d rendered: could not read it back: %v", qid, err)
		return
	}
	var entries []struct {
		ID    int64    `json:"id"`
		Shape string   `json:"shape"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return
	}
	for _, e := range entries {
		if e.ID != qid {
			continue
		}
		lines := make([]string, len(e.Lines))
		for i, l := range e.Lines {
			lines[i] = logSafe(l)
		}
		log.Printf("answer %d rendered:\n    %s", qid, strings.Join(lines, "\n    "))
		// The companion says yes to an answer for a question already answered
		// and prints nothing, so a retry after a lost reply is free. The same
		// yes hides a second service answering the same server: on the rig,
		// 2026-09-16, two instances each answered question 103 and the
		// companion kept the first, so this one logged an artifact the player
		// never saw. What was printed and what was sent cannot be compared
		// word for word, labels and sprites are decorated in, but the shape
		// and the number of lines survive decoration.
		if mismatch := printedSomethingElse(sent, e.Shape, len(e.Lines)); mismatch != "" {
			log.Printf("answer %d: the companion printed a different answer than this service sent (%s): "+
				"another service is answering this server, and the control API bind error above is the same tell", qid, mismatch)
		}
		return
	}
}

// printedSomethingElse compares the shape and, for the shapes the renderer
// prints one line per entry, the line count of what the companion printed
// against what this service sent. "" when they agree.
func printedSomethingElse(sent agent.Artifact, shape string, lines int) string {
	if shape != "" && shape != string(sent.Shape) {
		return fmt.Sprintf("shape %s printed, %s sent", shape, sent.Shape)
	}
	var want int
	switch sent.Shape {
	case agent.ShapeSummary:
		want = len(sent.Lines)
	case agent.ShapeList:
		want = len(sent.Items)
	default:
		return ""
	}
	if sent.Title != "" {
		want++
	}
	if want > 0 && lines > 0 && lines != want {
		return fmt.Sprintf("%d lines printed, %d sent", lines, want)
	}
	return ""
}

// logSafe is text a player or the model wrote, made fit for one log line:
// every control character, a newline or a terminal escape among them,
// becomes a space, so it can neither forge a line nor drive the terminal.
func logSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
