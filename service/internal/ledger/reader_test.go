package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b) + "\n"
}

// TestReadSkipsMalformedMiddleLine: a garbage line between two well-formed
// ones is skipped and counted, and both good lines still come back.
func TestReadSkipsMalformedMiddleLine(t *testing.T) {
	dir := t.TempDir()
	q := NewQuestionRecord()
	q.QuestionID = 1
	r := RoundRecord{QuestionID: 1, Round: 1, ToolCalls: []ToolCall{}}

	content := mustLine(t, q) + "not json at all\n" + mustLine(t, r)
	if err := os.WriteFile(filepath.Join(dir, "ledger-2026-01-01.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Questions) != 1 || len(got.Rounds) != 1 {
		t.Fatalf("got %d questions, %d rounds, want 1 and 1", len(got.Questions), len(got.Rounds))
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
}

// TestReadToleratesTruncatedLastLine: the shape a process kill mid-write
// leaves behind, a final line cut off with no trailing newline. It must
// count as skipped, not abort the read or lose the earlier, complete line.
func TestReadToleratesTruncatedLastLine(t *testing.T) {
	dir := t.TempDir()
	q := NewQuestionRecord()
	q.QuestionID = 7

	full := mustLine(t, q)
	truncated := `{"question_id":8,"round":1,"tool_calls":[{"name":"team_cl` // cut mid-write, no closing brace, no newline
	content := full + truncated

	if err := os.WriteFile(filepath.Join(dir, "ledger-2026-01-01.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Questions) != 1 || got.Questions[0].QuestionID != 7 {
		t.Fatalf("questions = %+v, want exactly the one complete record", got.Questions)
	}
	if len(got.Rounds) != 0 {
		t.Errorf("rounds = %+v, want none decoded from the truncated line", got.Rounds)
	}
	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
}

// TestReadRangeFiltersByDay: three files, three different days, a range
// that only covers the middle one.
func TestReadRangeFiltersByDay(t *testing.T) {
	dir := t.TempDir()
	for i, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
		q := NewQuestionRecord()
		q.QuestionID = int64(i + 1)
		if err := os.WriteFile(filepath.Join(dir, "ledger-"+day+".jsonl"), []byte(mustLine(t, q)), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", day, err)
		}
	}

	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 23, 0, 0, 0, time.UTC)
	got, err := ReadRange(dir, from, to)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if len(got.Questions) != 1 || got.Questions[0].QuestionID != 2 {
		t.Fatalf("questions = %+v, want only question_id 2 (2026-01-02)", got.Questions)
	}
}

// TestReadMissingDirectoryIsEmpty: a ledger dir that was never created
// (the ledger is enabled but nothing has been asked yet) is empty, not an
// error a fresh `aab stats` run would trip over.
func TestReadMissingDirectoryIsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Questions) != 0 || len(got.Rounds) != 0 || got.Skipped != 0 {
		t.Errorf("got %+v, want a completely empty Records", got)
	}
}

// TestDaysListsSortedCalendarDays: three files written out of calendar
// order, Days returns all three sorted oldest first, matching the same
// calendar order ReadRange reads its files in.
func TestDaysListsSortedCalendarDays(t *testing.T) {
	dir := t.TempDir()
	for _, day := range []string{"2026-01-03", "2026-01-01", "2026-01-02"} {
		q := NewQuestionRecord()
		if err := os.WriteFile(filepath.Join(dir, "ledger-"+day+".jsonl"), []byte(mustLine(t, q)), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", day, err)
		}
	}

	got, err := Days(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	want := []string{"2026-01-01", "2026-01-02", "2026-01-03"}
	if len(got) != len(want) {
		t.Fatalf("Days returned %d days, want %d: %v", len(got), len(want), got)
	}
	for i, day := range want {
		if got[i].Format(dayFormat) != day {
			t.Errorf("Days()[%d] = %s, want %s (want sorted oldest first)", i, got[i].Format(dayFormat), day)
		}
	}
}

// TestDaysFiltersByRange: the same [from, to] filter ReadRange applies to
// which files it reads, Days applies to the day list it returns.
func TestDaysFiltersByRange(t *testing.T) {
	dir := t.TempDir()
	for _, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
		q := NewQuestionRecord()
		if err := os.WriteFile(filepath.Join(dir, "ledger-"+day+".jsonl"), []byte(mustLine(t, q)), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", day, err)
		}
	}

	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 23, 0, 0, 0, time.UTC)
	got, err := Days(dir, from, to)
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(got) != 1 || got[0].Format(dayFormat) != "2026-01-02" {
		t.Fatalf("Days(from, to) = %v, want only 2026-01-02", got)
	}
}

// TestDaysToleratesMissingDirectory: a ledger dir that was never created
// reads as no days, not an error, the same as Read.
func TestDaysToleratesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	got, err := Days(dir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Days = %v, want none for a directory that does not exist", got)
	}
}

// TestReadIgnoresNonLedgerFiles: a directory that also holds unrelated
// files (a config snapshot, a stray note) must not have them mistaken for
// ledger content or counted as skipped lines.
func TestReadIgnoresNonLedgerFiles(t *testing.T) {
	dir := t.TempDir()
	q := NewQuestionRecord()
	q.QuestionID = 1
	if err := os.WriteFile(filepath.Join(dir, "ledger-2026-01-01.jsonl"), []byte(mustLine(t, q)), 0o644); err != nil {
		t.Fatalf("write ledger fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aab.effective.yaml"), []byte("agent:\n  model: x\n"), 0o644); err != nil {
		t.Fatalf("write unrelated fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not json, not a ledger file"), 0o644); err != nil {
		t.Fatalf("write unrelated fixture: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Errorf("questions = %+v, want exactly the one ledger record", got.Questions)
	}
	if got.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (non-ledger files are never opened)", got.Skipped)
	}
}
