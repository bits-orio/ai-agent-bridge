// One log line per model round, for the operator who is tuning cost. It
// says where the output tokens went: thinking, prose the player never sees,
// or the tool calls that do the work. And one line per answer a round sent
// back to the model, saying why.

package agent

import (
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func (a *Agent) traceRound(q Question, round int, step model.Step) {
	if a.Trace == nil {
		return
	}
	var thinking, text int
	var calls []string
	for _, b := range step.Blocks {
		switch b.Type {
		case model.BlockThinking:
			thinking += len(b.Thinking)
		case model.BlockRedactedThinking, model.BlockReasoning:
			thinking += len(b.Data) + len(b.Thinking)
		case model.BlockText:
			text += len(b.Text)
		case model.BlockToolUse:
			calls = append(calls, b.Name)
		}
	}
	u := step.Usage
	a.Trace("question %d round %d: stop=%s in=%d out=%d reasoning=%d cached=%d/%d thinking=%dc text=%dc calls=%s%s",
		q.ID, round, step.StopReason, u.InputTokens, u.OutputTokens, u.ReasoningTokens, u.CacheReadTokens, u.CacheWriteTokens,
		thinking, text, callList(calls), via(step.Provider))
}

// traceRefusals logs one line per submit_answer the round refused, with the
// reason the model was sent back. An upstream once looped on refused answers
// until its rounds ran out, and nothing said why; this and the round's
// ledger record (submit_refusals) now do.
func (a *Agent) traceRefusals(q Question, round int, refused []ledger.SubmitRefusal) {
	if a.Trace == nil {
		return
	}
	for _, r := range refused {
		a.Trace("question %d round %d: %s refused (%s): %s", q.ID, round, SubmitTool, r.Kind, r.Reason)
	}
}

// via names the host that served a round when the route says; a cache miss
// after a hit is usually a different host.
func via(provider string) string {
	if provider == "" {
		return ""
	}
	return " via=" + provider
}

func callList(calls []string) string {
	if len(calls) == 0 {
		return "none"
	}
	return strings.Join(calls, ",")
}
