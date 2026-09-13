// The ledger lines Answer writes: one QuestionRecord per question, on every
// one of its nine endings, and one RoundRecord per model round
// (docs/design/phase4-observability-spec.md sections 2-4). Nothing here
// decides an answer or can slow one down: a nil Agent.Ledger, or one opened
// disabled, makes ledger.Writer's own methods a no-op, which is the only
// safety this file relies on.

package agent

import (
	"encoding/json"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// questionRecord builds the per-question line. rawText is q.Text as the
// companion originally sent it, captured once at the top of Answer, before
// substituteLabels rewrites q.Text for the model: every ending logs the same
// text regardless of how far the question got. shape is the artifact shape
// actually delivered; it is never empty, since even the model-error path now
// records the notice the runner sends the player. modelError is nil except on
// that one path, where it carries the failure text. zeroLookup is computed by
// the caller (zeroLookupFor where a real artifact exists, a literal false on
// the model-error path), never here: this function only records it.
// briefingStatus, briefingBytes, briefingTokens and briefingMs are the four
// fields docs/design/phase4-observability-spec.md's briefing/briefing_bytes/
// briefing_tokens/briefing_ms rows describe; a caller with no briefing to
// report passes ledger.BriefingOff and zero for the rest, the same absent
// values NewQuestionRecord already defaults to. askedBack and
// awaitingReplyResolved are the ask-back loop's own two fields (section 12):
// the caller computes both (isAskBack for the first, Answer's own
// wasAwaiting for the second), this function only records them.
func (a *Agent) questionRecord(q Question, rawText string, mark SessionMark, shape string, rounds, lookups int, cost float64, msModel, msRCON int64, refused bool, refusedReason *string, modelError *string, zeroLookup bool, briefingStatus string, briefingBytes, briefingTokens int, briefingMs int64, askedBack, awaitingReplyResolved bool) ledger.QuestionRecord {
	r := ledger.NewQuestionRecord()
	r.QuestionID = q.ID
	r.Asker = q.askerName()
	r.Force = q.force()
	r.Surface = q.Surface
	r.SessionKey = mark.Key
	r.SessionFresh = mark.Fresh
	r.Text = rawText
	r.Rounds = rounds
	r.Lookups = lookups
	r.ZeroLookup = zeroLookup
	r.Shape = shape
	r.Cost = cost
	r.MsModel = msModel
	r.MsRCON = msRCON
	r.Briefing = briefingStatus
	r.BriefingBytes = briefingBytes
	r.BriefingTokens = briefingTokens
	r.BriefingMs = briefingMs
	r.AskedBack = askedBack
	r.AwaitingReplyResolved = awaitingReplyResolved
	r.Refused = refused
	r.RefusedReason = refusedReason
	r.ModelError = modelError
	return r
}

func (a *Agent) writeQuestion(r ledger.QuestionRecord) {
	a.Ledger.WriteQuestion(r)
}

// zeroLookupFor is the free tier's own signature: a question the model
// actually ran and finished without a single lookup, answered from the
// briefing alone. False whenever no round ran at all (a refusal never asked
// the model), and false again when what actually reached the player is a
// warning notice: the loop talking about itself (out of rounds, out of
// tokens, stalled) rather than the model answering, which must never read as
// a free-tier win just because rounds is nonzero and lookups is zero.
func zeroLookupFor(rounds, lookups int, artifact Artifact) bool {
	return rounds > 0 && lookups == 0 && !(artifact.Shape == ShapeNotice && artifact.Level == LevelWarning)
}

// roundRecord builds one round's ledger line without writing it: the caller
// holds it in a slice and flushRounds writes every accumulated record only
// once the question's ending is already decided (finish, and the
// model-error path in Answer), so the ledger never writes on the path that
// decides an answer.
//
// ms_total is modelMs (this round's own model call, timed by the caller)
// plus toolMs, the one wall-clock span the caller timed once around the
// round's whole tool phase rather than summing each call's own ms (a round's
// reads run concurrently, so a sum would measure fan-out width, not elapsed
// time). The per-question ms_model and ms_rcon the caller accumulates from
// the same two numbers add back up to the sum of every round's ms_total,
// with one exception: the model-error path in Answer times and keeps that
// round's model call in ms_model (an honest number, the call really took
// that long) but never builds a RoundRecord for it, since the round never
// produced one, so ms_model there reflects one more round than the round
// records on file for the question.
func (a *Agent) roundRecord(questionID int64, round int, step model.Step, modelMs, toolMs int64, calls []ledger.ToolCall) ledger.RoundRecord {
	return ledger.RoundRecord{
		QuestionID:           questionID,
		Round:                round,
		Model:                a.mdl.Name(),
		Provider:             step.Provider,
		MsTotal:              modelMs + toolMs,
		InputTokens:          step.Usage.InputTokens,
		OutputTokens:         step.Usage.OutputTokens,
		CacheReadTokens:      step.Usage.CacheReadTokens,
		CacheWriteTokens:     step.Usage.CacheWriteTokens,
		CacheWriteHourTokens: step.Usage.CacheWriteHourTokens,
		ReasoningTokens:      step.Usage.ReasoningTokens,
		Cost:                 CostUSD(a.mdl.Name(), step.Usage),
		StopReason:           step.StopReason,
		ToolCalls:            calls,
	}
}

// flushRounds writes every accumulated round record, in the order the
// rounds ran. Called only after the artifact that ends the question already
// exists: the ledger must never write on the path that decides an answer.
func (a *Agent) flushRounds(rounds []ledger.RoundRecord) {
	for _, r := range rounds {
		a.Ledger.WriteRound(r)
	}
}

// toolCallRecord builds one ledger.ToolCall: args clipped exactly the way a
// tool result is clipped (the same content helper, the same limit), timed
// around the call, and its own outcome. path is left empty: find_item's rung
// is a future feature (Decision 4), so every tool call omits it today.
func toolCallRecord(name string, args json.RawMessage, limit int, ms time.Duration, floor func() time.Duration, resultBytes int, err error) ledger.ToolCall {
	tc := ledger.ToolCall{
		Name:         name,
		Args:         content(args, limit),
		ResultBytes:  resultBytes,
		Ms:           ms.Milliseconds(),
		MsAboveFloor: msAboveFloor(ms, floor).Milliseconds(),
		OK:           err == nil,
	}
	if err != nil {
		msg := err.Error()
		tc.Error = &msg
	}
	return tc
}

// maybeToolCallRecord is toolCallRecord gated on ledgerOn: a disabled ledger
// (or none wired) skips the args clip and the Floor call entirely rather
// than build a real ledger.ToolCall only for the writer to discard it.
func maybeToolCallRecord(ledgerOn bool, name string, args json.RawMessage, limit int, ms time.Duration, floor func() time.Duration, resultBytes int, err error) ledger.ToolCall {
	if !ledgerOn {
		return ledger.ToolCall{}
	}
	return toolCallRecord(name, args, limit, ms, floor, resultBytes, err)
}

// msAboveFloor is ms minus the fastest recent RCON round trip, clamped at
// zero: the part of a call that is not baseline network latency, so a
// genuinely slow tool stands out from a merely slow connection. A nil floor
// (Agent.Floor never wired, as in most tests) means nothing is subtracted.
func msAboveFloor(ms time.Duration, floor func() time.Duration) time.Duration {
	if floor == nil {
		return ms
	}
	if above := ms - floor(); above > 0 {
		return above
	}
	return 0
}

func strPtr(s string) *string { return &s }
