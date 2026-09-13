// clip.go bounds the two free-form error strings a ledger record can carry:
// a tool call's own failure text (ToolCall.Error) and the model's own
// failure text (QuestionRecord.ModelError). Both come from something
// upstream of this package (a tool's error, an HTTP client's error), so
// neither arrives already bounded the way a tool result or a tool call's
// Args does: the agent package clips those itself, through its own
// content() helper, sized off the operator's configured
// max_tool_result_bytes. This package cannot reuse that helper: agent
// imports ledger, never the reverse, so a shared clip has to live on this
// side of that one-way dependency instead, and it cannot see the operator's
// own limit either, since config sits above both agent and ledger. A fixed
// size, applied here in the write path (writer.go), keeps both fields
// bounded regardless of what any caller remembers to do (section 5: the
// ledger must never grow past what the operator chose).
package ledger

import "unicode/utf8"

// maxClipBytes is the fixed size clip trims a free-form error string to.
const maxClipBytes = 4096

// clipNote is appended to a string this package actually cut, so a reader
// can tell a value was clipped rather than genuinely this short.
const clipNote = " [clipped]"

// clip trims s to at most limit bytes, backing off to the nearest UTF-8
// rune boundary so a multi-byte character is never split, the same way
// agent's own content() helper clips a tool result.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + clipNote
}
