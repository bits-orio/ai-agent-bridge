package ledger

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestZeroValueWriterIsNoop: the zero Writer (never Open'd) must not panic
// on any method, and must never touch disk.
func TestZeroValueWriterIsNoop(t *testing.T) {
	var w Writer
	w.WriteQuestion(NewQuestionRecord())
	w.WriteRound(RoundRecord{})
	if err := w.Close(); err != nil {
		t.Errorf("Close on zero Writer = %v, want nil", err)
	}
}

// TestNilWriterIsNoop: a *Writer that was never assigned at all must be
// just as safe as the zero value, since a caller is never expected to
// nil-check before writing.
func TestNilWriterIsNoop(t *testing.T) {
	var w *Writer
	w.WriteQuestion(NewQuestionRecord())
	w.WriteRound(RoundRecord{})
	if err := w.Close(); err != nil {
		t.Errorf("Close on nil Writer = %v, want nil", err)
	}
}

// TestOpenDisabledNeverTouchesDisk: enabled=false must behave exactly like
// the zero Writer, even though a real dir was handed to Open.
func TestOpenDisabledNeverTouchesDisk(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, false)
	w.WriteQuestion(NewQuestionRecord())
	w.WriteRound(RoundRecord{})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("disabled Writer left %d entries in %s, want 0", len(entries), dir)
	}
}

// TestOpenIsLazy: Open itself must not create the directory or a file;
// only a write does.
func TestOpenIsLazy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	w := Open(dir, true)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir exists right after Open, want it created lazily: err=%v", err)
	}

	w.WriteQuestion(NewQuestionRecord())

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir after first write: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "ledger-") {
		t.Fatalf("entries = %v, want exactly one ledger-*.jsonl file", entries)
	}
}

// TestWriterRollsAtUTCDayBoundary drives the clock directly (the same
// pattern internal/agent tests use for Agent.now) rather than sleeping
// across a real day boundary.
func TestWriterRollsAtUTCDayBoundary(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, true)

	day1 := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	w.now = func() time.Time { return day1 }
	q1 := NewQuestionRecord()
	q1.QuestionID = 1
	w.WriteQuestion(q1)

	day2 := time.Date(2026, 1, 2, 0, 1, 0, 0, time.UTC)
	w.now = func() time.Time { return day2 }
	q2 := NewQuestionRecord()
	q2.QuestionID = 2
	w.WriteQuestion(q2)

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, want := range []struct {
		file string
		id   int64
	}{
		{"ledger-2026-01-01.jsonl", 1},
		{"ledger-2026-01-02.jsonl", 2},
	} {
		b, err := os.ReadFile(filepath.Join(dir, want.file))
		if err != nil {
			t.Fatalf("read %s: %v", want.file, err)
		}
		if !strings.Contains(string(b), `"question_id":`+strconv.FormatInt(want.id, 10)) {
			t.Errorf("%s = %q, want it to carry question_id %d", want.file, b, want.id)
		}
	}

	records, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records.Questions) != 2 {
		t.Fatalf("Read found %d questions across both files, want 2", len(records.Questions))
	}
	if records.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", records.Skipped)
	}
}

// TestWriteRoundToolCallsNeverNull: a round with no tool calls at all (a
// nil ToolCalls, the zero value a caller gets for free) must still
// serialise as "tool_calls":[], never null, so a non-Go reader can index
// it without a null check (F8).
func TestWriteRoundToolCallsNeverNull(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, true)
	w.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	w.WriteRound(RoundRecord{QuestionID: 1, Round: 1})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "ledger-2026-01-01.jsonl"))
	if err != nil {
		t.Fatalf("read ledger file: %v", err)
	}
	if !strings.Contains(string(b), `"tool_calls":[]`) {
		t.Errorf("ledger line = %s, want it to carry \"tool_calls\":[]", b)
	}
	if strings.Contains(string(b), `"tool_calls":null`) {
		t.Errorf("ledger line = %s, want tool_calls never null", b)
	}
}

// TestWriteRoundClipsToolCallError: a tool call's Error is the one string
// on a ToolCall nothing upstream already bounds, so the write path clips
// it itself (F8), the same way Args already arrives clipped.
func TestWriteRoundClipsToolCallError(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, true)
	w.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	long := strings.Repeat("x", maxClipBytes+500)
	w.WriteRound(RoundRecord{
		QuestionID: 1,
		Round:      1,
		ToolCalls:  []ToolCall{{Name: "nope", Error: &long}},
	})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Rounds) != 1 || len(got.Rounds[0].ToolCalls) != 1 {
		t.Fatalf("got %+v, want exactly one round with one tool call", got.Rounds)
	}
	gotErr := got.Rounds[0].ToolCalls[0].Error
	if gotErr == nil {
		t.Fatalf("tool call Error = nil, want a clipped string")
	}
	if len(*gotErr) >= len(long) {
		t.Errorf("tool call Error is %d bytes, want it clipped below the original %d", len(*gotErr), len(long))
	}
	if !strings.HasSuffix(*gotErr, clipNote) {
		t.Errorf("tool call Error = %q, want it to end with the clip note", *gotErr)
	}
	// The caller's own slice must come back unclipped: normalizeToolCalls
	// must never mutate a ToolCall the caller still holds.
	if len(long) != maxClipBytes+500 {
		t.Fatalf("test setup broken: long was mutated")
	}
}

// TestWriteQuestionClipsModelError: ModelError is a provider's own error
// text, unbounded by anything upstream, so the write path clips it the
// same way a ToolCall's Error is (F3, F8).
func TestWriteQuestionClipsModelError(t *testing.T) {
	dir := t.TempDir()
	w := Open(dir, true)
	w.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	long := strings.Repeat("y", maxClipBytes+500)
	q := NewQuestionRecord()
	q.QuestionID = 1
	q.ModelError = &long
	w.WriteQuestion(q)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("got %+v, want exactly one question", got.Questions)
	}
	gotErr := got.Questions[0].ModelError
	if gotErr == nil {
		t.Fatalf("model_error = nil, want a clipped string")
	}
	if len(*gotErr) >= len(long) {
		t.Errorf("model_error is %d bytes, want it clipped below the original %d", len(*gotErr), len(long))
	}
}

// TestWriteErrorLoggedOnceThenSwallowed forces every write to fail (dir is
// actually a plain file, so MkdirAll/OpenFile can never succeed under it,
// regardless of the user the test runs as) and checks the failure reaches
// the log exactly once, never panics, and leaves the Writer usable.
func TestWriteErrorLoggedOnceThenSwallowed(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	w := Open(notADir, true)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	w.WriteQuestion(NewQuestionRecord())
	w.WriteQuestion(NewQuestionRecord())
	w.WriteRound(RoundRecord{})

	got := strings.Count(buf.String(), "ledger:")
	if got != 1 {
		t.Errorf("log lines starting \"ledger:\" = %d, want exactly 1 (log once, swallow after); log:\n%s", got, buf.String())
	}
}
