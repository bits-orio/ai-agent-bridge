// Running one question and getting its artifact into the game.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
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
// new, deliver what is waiting, and leave alone what is finished with.
//
// The catalog is read once for the whole page, on the first question that needs
// it, so a tick costs one providers round trip rather than one per question.
func (r *runner) answerPage(ctx context.Context, questions rpc.PollReply) {
	var game []tools.Tool
	var haveCatalog, readCatalog bool
	for _, q := range questions {
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
			state = &delivery{result: r.run(ctx, q, game)}
			r.inFlight[q.ID] = state
		}
		if state.done {
			continue
		}
		r.deliver(ctx, q, state)
	}
}

// run is the part the operator pays for: the agent loop. Every ending produces
// an artifact, so a model that fails still has a notice to deliver rather than
// leaving the asker waiting.
func (r *runner) run(ctx context.Context, q rpc.Question, game []tools.Tool) agent.Result {
	question := agentQuestion(q)
	question.Labels = r.labelsFor(ctx)
	log.Printf("question %d from %s: %s", q.ID, question.AskerLabel(), q.Text)

	result, err := r.agent.Answer(ctx, question, game)
	if err != nil {
		log.Printf("question %d: the model failed: %v", q.ID, err)
		if openrouter.IsOutOfCredit(err) {
			log.Printf("question %d: OpenRouter has no credit left; add some at %s", q.ID, creditsURL)
		}
		result.Artifact = agent.Notice(agent.LevelWarning, failureNotice(err))
	}
	if result.Session.Fresh {
		log.Printf("question %d: new session %s", q.ID, result.Session.Key)
	}
	r.stats.RecordAnswer(result.Usage, result.CostUSD)
	r.answered++
	r.lastActivity = time.Now()
	return result
}

// deliver offers one artifact to the companion. A delivery that failed on the
// wire is tried again on the next tick with the artifact already in hand: the
// model is never asked the same question twice. Three failures and the service
// stops trying, with a line saying so.
func (r *runner) deliver(ctx context.Context, q rpc.Question, state *delivery) {
	state.attempts++
	delivered, err := r.rpc.Answer(ctx, q.ID, state.result.Artifact)
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
		// what the model wrote rather than guessed at. At most 6 KB.
		if raw, err := json.Marshal(state.result.Artifact); err == nil {
			log.Printf("answer %d artifact %s", q.ID, raw)
		}
	case rpc.HasCode(err, rpc.CodeBadArtifact):
		r.refused(ctx, q, state, err)
	case state.attempts >= maxDeliveries:
		state.done = true
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
	delivered, err := r.rpc.Answer(ctx, q.ID, state.result.Artifact)
	if err != nil {
		log.Printf("answer %d: the notice did not land either: %v", q.ID, err)
		return
	}
	if !delivered {
		log.Printf("answer %d: the companion did not confirm the notice", q.ID)
	}
}

// agentQuestion maps one polled question onto the agent's question type. The
// asker's name travels with it, which is what lets the label name a player:
// history rows are keyed by player name, so "when did I last die" can only be
// scoped to the person who asked when the prompt carries their name (review-fix
// contract 9). The label itself is built in one place, agent.Question.AskerLabel,
// so the log line and the system prompt always name the asker the same way.
func agentQuestion(q rpc.Question) agent.Question {
	return agent.Question{
		ID:              q.ID,
		Text:            q.Text,
		PlayerIndex:     q.PlayerIndex,
		PlayerName:      q.PlayerName,
		Force:           q.Force,
		Scope:           q.Scope,
		Private:         q.Private,
		Surface:         q.Surface,
		PhysicalSurface: q.PhysicalSurface,
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
