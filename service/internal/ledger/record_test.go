package ledger

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

// TestQuestionRecordRoundTrip marshals a fully populated QuestionRecord and
// unmarshals it back, and checks the wire field names the spec's example
// object uses (docs/design/phase4-observability-spec.md section 2).
func TestQuestionRecordRoundTrip(t *testing.T) {
	want := NewQuestionRecord()
	want.QuestionID = 1042
	want.Asker = "player7"
	want.Force = "team-3"
	want.Surface = "nauvis"
	want.SessionKey = "team-3#iron"
	want.SessionFresh = false
	want.Text = "what is each team researching"
	want.Rounds = 2
	want.Lookups = 3
	want.ZeroLookup = false
	want.Shape = "table"
	want.Cost = 0.00142
	want.MsModel = 5230
	want.MsRCON = 1140
	want.Refused = false
	want.RefusedReason = nil

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{
		"question_id", "asker", "force", "surface", "session_key", "session_fresh",
		"text", "rounds", "lookups", "zero_lookup", "shape", "cost", "ms_model", "ms_rcon",
		"briefing", "briefing_bytes", "briefing_tokens", "briefing_ms",
		"asked_back", "awaiting_reply_resolved", "refused", "refused_reason", "model_error", "voice",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("marshaled JSON missing key %q: %s", key, raw)
		}
	}
	if string(fields["refused_reason"]) != "null" {
		t.Errorf("refused_reason = %s, want null when RefusedReason is nil", fields["refused_reason"])
	}
	if string(fields["model_error"]) != "null" {
		t.Errorf("model_error = %s, want null when ModelError is nil", fields["model_error"])
	}
	if string(fields["briefing"]) != `"off"` {
		t.Errorf("briefing = %s, want \"off\" from NewQuestionRecord", fields["briefing"])
	}
	if string(fields["voice"]) != `"off"` {
		t.Errorf("voice = %s, want \"off\" from NewQuestionRecord", fields["voice"])
	}

	var got QuestionRecord
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip mismatch:\n want %+v\n got  %+v", want, got)
	}
}

// TestQuestionRecordRefusedReasonRoundTrips checks the non-nil case: a
// refusal's reason travels as a plain string, not null.
func TestQuestionRecordRefusedReasonRoundTrips(t *testing.T) {
	want := NewQuestionRecord()
	want.Refused = true
	want.RefusedReason = strPtr("You have asked all the questions your hourly allowance covers. Try again a bit later.")

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got QuestionRecord
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RefusedReason == nil || *got.RefusedReason != *want.RefusedReason {
		t.Errorf("refused_reason = %v, want %q", got.RefusedReason, *want.RefusedReason)
	}
}

// TestQuestionRecordModelErrorRoundTrips checks the non-nil case: a model
// outage's error text travels as a plain string, not null, and refused
// stays false since a model error is not a refusal (F3).
func TestQuestionRecordModelErrorRoundTrips(t *testing.T) {
	want := NewQuestionRecord()
	want.Shape = "notice"
	want.ModelError = strPtr("OpenRouter returned a 502")

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got QuestionRecord
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ModelError == nil || *got.ModelError != *want.ModelError {
		t.Errorf("model_error = %v, want %q", got.ModelError, *want.ModelError)
	}
	if got.Refused {
		t.Errorf("refused = true on a model-error record, want false: an outage is neither a refusal nor an ordinary answer")
	}
}

// TestRoundRecordRoundTrip covers the nested tool_calls entries too, one
// ok and one failed, and confirms ms_first_byte never appears on the wire
// (Decision 3): there is no field for it to round-trip in the first place.
func TestRoundRecordRoundTrip(t *testing.T) {
	errMsg := "there is no tool named \"nope\""
	want := RoundRecord{
		QuestionID: 1042,
		Round:      2,
		Model:      "deepseek/deepseek-v4-pro-0813",
		Provider:   "DeepInfra",
		MsTotal:    4120,

		InputTokens:          3810,
		OutputTokens:         214,
		CacheReadTokens:      3400,
		CacheWriteTokens:     0,
		CacheWriteHourTokens: 0,
		ReasoningTokens:      0,

		Cost:       0.00071,
		StopReason: "tool_use",
		ToolCalls: []ToolCall{
			{
				Name:         "mts-v1__team_clocks",
				Args:         `{"all":true}`,
				ResultBytes:  812,
				Ms:           210,
				MsAboveFloor: 105,
				OK:           true,
				Error:        nil,
			},
			{
				Name:         "ai-agent-bridge-tools__find_item",
				Args:         `{"item":"iron-plate"}`,
				ResultBytes:  340,
				Ms:           118,
				MsAboveFloor: 13,
				OK:           true,
				Error:        nil,
				Path:         "logistic",
			},
			{
				Name:        "nope",
				ResultBytes: 0,
				Ms:          4,
				OK:          false,
				Error:       &errMsg,
			},
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "ms_first_byte") {
		t.Errorf("marshaled round carries ms_first_byte, want it entirely absent: %s", raw)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{
		"question_id", "round", "model", "provider", "ms_total",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"cache_write_hour_tokens", "reasoning_tokens", "cost", "stop_reason", "tool_calls",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("marshaled JSON missing key %q: %s", key, raw)
		}
	}

	var got RoundRecord
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip mismatch:\n want %+v\n got  %+v", want, got)
	}

	// The first two calls never touched find_item's path rung except the
	// second; the first call's path must not even appear on the wire.
	var raws []json.RawMessage
	if err := json.Unmarshal(fields["tool_calls"], &raws); err != nil {
		t.Fatalf("unmarshal tool_calls: %v", err)
	}
	var first map[string]json.RawMessage
	if err := json.Unmarshal(raws[0], &first); err != nil {
		t.Fatalf("unmarshal first tool call: %v", err)
	}
	if _, ok := first["path"]; ok {
		t.Errorf("first tool call carries path, want it omitted: %s", raws[0])
	}
	var third map[string]json.RawMessage
	if err := json.Unmarshal(raws[2], &third); err != nil {
		t.Fatalf("unmarshal third tool call: %v", err)
	}
	if string(third["error"]) != `"there is no tool named \"nope\""` {
		t.Errorf("third tool call error = %s, want the quoted error string", third["error"])
	}
	if string(first["error"]) != "null" {
		t.Errorf("first tool call error = %s, want null when ok", first["error"])
	}
}
