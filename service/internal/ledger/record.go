// Package ledger is the service's own record of what an answer cost: one
// JSON object per model round and one per question, appended as a JSONL
// line (docs/design/phase4-observability-spec.md, sections 2-4). `aab
// stats` reads it back; nothing in this package calls the model or RCON,
// and nothing here ever decides an answer, so a ledger write can never
// slow one down or fail one (section 5).
//
// This package has no dependency on internal/agent, by design: agent and
// cmd import ledger, never the reverse.
package ledger

// Absent-feature values a QuestionRecord carries before the feature they
// describe has shipped (Decision 4). NewQuestionRecord is the one place
// that sets them, so the day briefing or personality lands, this is the
// only assignment that changes.
const (
	BriefingOff    = "off"
	BriefingOn     = "on"
	BriefingFailed = "failed"

	VoiceOff = "off"
)

// QuestionRecord is the per-question line: one appended after every
// question the service finishes with, whatever the ending (a real answer,
// a refusal, an ask-back, or the model itself failing).
type QuestionRecord struct {
	QuestionID int64  `json:"question_id"`
	Asker      string `json:"asker"`
	Force      string `json:"force"`
	Surface    string `json:"surface"`

	// SessionKey and SessionFresh are sessionKey's own joined form
	// ("global", "global#iron", "team-3#iron") and sessions.open's own
	// fresh bool, copied straight across by the caller; neither needs
	// recomputing here.
	SessionKey   string `json:"session_key"`
	SessionFresh bool   `json:"session_fresh"`

	Text string `json:"text"`

	Rounds     int     `json:"rounds"`
	Lookups    int     `json:"lookups"`
	ZeroLookup bool    `json:"zero_lookup"`
	Shape      string  `json:"shape"`
	Cost       float64 `json:"cost"`
	MsModel    int64   `json:"ms_model"`
	MsRCON     int64   `json:"ms_rcon"`

	// Briefing through Voice all describe a feature that has not shipped
	// yet. NewQuestionRecord sets each to its documented absent value so
	// the schema is stable from the first line ever written.
	Briefing              string `json:"briefing"`
	BriefingBytes         int    `json:"briefing_bytes"`
	BriefingTokens        int    `json:"briefing_tokens"`
	BriefingMs            int64  `json:"briefing_ms"`
	AskedBack             bool   `json:"asked_back"`
	AwaitingReplyResolved bool   `json:"awaiting_reply_resolved"`

	Refused       bool    `json:"refused"`
	RefusedReason *string `json:"refused_reason"`

	// ModelError holds the provider's error text when the model call itself
	// failed and the question ended in a failure notice, nil for every
	// other ending. Distinct from Refused: a refusal turns a question away
	// before or instead of asking the model, while a model error is what
	// happens when the model was asked and the call itself failed. Clipped
	// in the write path the same way a ToolCall's Error is (clip.go).
	ModelError *string `json:"model_error"`

	Voice string `json:"voice"`
}

// NewQuestionRecord returns a QuestionRecord with every not-yet-shipped
// field already set to its documented absent value (Decision 4): briefing
// "off", the three briefing_* numbers left at their zero value, asked_back
// and awaiting_reply_resolved false, voice "off". The caller fills in
// everything else the question actually produced.
func NewQuestionRecord() QuestionRecord {
	return QuestionRecord{
		Briefing: BriefingOff,
		Voice:    VoiceOff,
	}
}

// RoundRecord is the per-round line: one appended for every model round a
// question takes. ms_first_byte, in the spec's own field table, is
// deliberately absent rather than a field written as zero: the OpenRouter
// client posts and waits for the whole response, it does not stream, so
// there is no first-byte moment to measure, and a zero would read as a
// measurement that never actually happened (Decision 3).
type RoundRecord struct {
	QuestionID int64  `json:"question_id"`
	Round      int    `json:"round"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`

	MsTotal int64 `json:"ms_total"`

	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadTokens      int `json:"cache_read_tokens"`
	CacheWriteTokens     int `json:"cache_write_tokens"`
	CacheWriteHourTokens int `json:"cache_write_hour_tokens"`
	ReasoningTokens      int `json:"reasoning_tokens"`

	Cost float64 `json:"cost"`

	StopReason string     `json:"stop_reason"`
	ToolCalls  []ToolCall `json:"tool_calls"`
}

// ToolCall is one entry of a round's tool_calls array.
type ToolCall struct {
	Name         string `json:"name"`
	Args         string `json:"args"`
	ResultBytes  int    `json:"result_bytes"`
	Ms           int64  `json:"ms"`
	MsAboveFloor int64  `json:"ms_above_floor"`
	OK           bool   `json:"ok"`
	// Error holds the call's error string when OK is false, and is left nil
	// (encoded as JSON null, never omitted) otherwise.
	Error *string `json:"error"`
	// Path is set only for a find_item call, to the rung that answered it
	// ("logistic", "walk" or "refused"); every other tool leaves it empty,
	// which omits the key entirely rather than writing an empty string.
	Path string `json:"path,omitempty"`
}
