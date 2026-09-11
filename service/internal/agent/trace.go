// One log line per model round, for the operator who is tuning cost. It
// says where the output tokens went: thinking, prose the player never sees,
// or the tool calls that do the work.

package agent

import (
	"strings"

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
		case model.BlockRedactedThinking:
			thinking += len(b.Data)
		case model.BlockText:
			text += len(b.Text)
		case model.BlockToolUse:
			calls = append(calls, b.Name)
		}
	}
	u := step.Usage
	a.Trace("question %d round %d: stop=%s in=%d out=%d cached=%d/%d thinking=%dc text=%dc calls=%s",
		q.ID, round, step.StopReason, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens,
		thinking, text, callList(calls))
}

func callList(calls []string) string {
	if len(calls) == 0 {
		return "none"
	}
	return strings.Join(calls, ",")
}
