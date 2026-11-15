// Writer appends the two record kinds to one file a day, and rolls to the
// next file itself the moment the UTC day changes (section 3). It is built
// to never cost an answer anything: one mutex, one append, no goroutine
// fan-out, no buffering past the OS's own, and a write error is logged once
// and swallowed for the rest of the process (Decision 5) rather than risk
// spamming the log on a failing disk.

package ledger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dayFormat is the calendar-day layout ledger-YYYY-MM-DD.jsonl rolls on,
// always read in UTC (section 3: "One JSONL file per UTC calendar day").
const dayFormat = "2006-01-02"

// fileName is the file the records for a given moment belong in.
func fileName(at time.Time) string {
	return fmt.Sprintf("ledger-%s.jsonl", at.UTC().Format(dayFormat))
}

// Writer appends QuestionRecord and RoundRecord lines under one directory.
// The zero Writer, a disabled Writer, and a nil *Writer are all safe to
// call: every method is then a no-op, so a caller wires Open's result in
// unconditionally and never has to test for nil before writing.
type Writer struct {
	mu      sync.Mutex
	dir     string
	enabled bool
	now     func() time.Time

	day  string // the UTC day (dayFormat) the open file belongs to
	file *os.File

	warnOnce sync.Once

	// open is the run each question id's rounds are being written under,
	// until its question record closes it (stampRound, stampQuestion).
	open      map[int64]openRun
	lastRunAt int64
}

// openRun is one run whose rounds are written and whose question record is
// not yet.
type openRun struct {
	id        string
	lastRound int
}

// Open returns a Writer that appends ledger-YYYY-MM-DD.jsonl files under
// dir. enabled false returns the zero-value Writer, so the config's own
// enabled flag is the only thing deciding whether anything is ever
// written. Open touches no disk itself: the directory and the day's file
// are created lazily, on the first record actually written, so a server
// that never gets a question never leaves an empty directory behind.
func Open(dir string, enabled bool) *Writer {
	if !enabled {
		return &Writer{}
	}
	return &Writer{dir: dir, enabled: true, now: time.Now}
}

// Enabled reports whether this Writer will actually write anything. A nil
// Writer and a disabled one both report false, so a caller can test this
// once and skip building a record entirely rather than build it only to
// have write's own no-op discard it.
func (w *Writer) Enabled() bool {
	return w != nil && w.enabled
}

// WriteQuestion appends one per-question record. ModelError is clipped here
// when set, the same way a round's tool_calls[].error is (clip.go), so one
// long provider error can never grow the ledger past what the operator
// chose.
func (w *Writer) WriteQuestion(r QuestionRecord) {
	if r.ModelError != nil {
		clipped := clip(*r.ModelError, maxClipBytes)
		r.ModelError = &clipped
	}
	if w.Enabled() {
		w.stampQuestion(&r)
	}
	w.write(r)
}

// stampRound gives a round the run_id of its question's open run, or opens
// one. The agent writes a run's rounds, in order, just before its question
// record and runs questions one at a time, so a round numbered no higher
// than the last one seen starts another run of the same id.
func (w *Writer) stampRound(r *RoundRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r.RunID != "" {
		return
	}
	if w.open == nil {
		w.open = map[int64]openRun{}
	}
	run, ok := w.open[r.QuestionID]
	if !ok || r.Round <= run.lastRound {
		run = openRun{id: w.newRunID(r.QuestionID)}
	}
	run.lastRound = r.Round
	w.open[r.QuestionID] = run
	r.RunID = run.id
}

// stampQuestion gives a question record its open run's id, or a fresh one
// for a run with no rounds (a refusal), and closes the run.
func (w *Writer) stampQuestion(r *QuestionRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r.RunID == "" {
		if run, ok := w.open[r.QuestionID]; ok {
			r.RunID = run.id
		} else {
			r.RunID = w.newRunID(r.QuestionID)
		}
	}
	delete(w.open, r.QuestionID)
}

// newRunID is the start of a run in Unix nanoseconds and its question id,
// unique within one writer and, by the clock, across restarts. Called with
// mu held.
func (w *Writer) newRunID(qid int64) string {
	at := w.now().UnixNano()
	if at <= w.lastRunAt {
		at = w.lastRunAt + 1
	}
	w.lastRunAt = at
	return fmt.Sprintf("%d-%d", at, qid)
}

// WriteRound appends one per-round record. ToolCalls is normalised here so
// a tool-less round always serialises as "tool_calls":[], never null: a
// non-Go reader can index it without a null check, and no caller has to
// remember to leave it as an empty slice rather than nil. Each entry's own
// Error is clipped the same way Args already is by the time it reaches
// here (clip.go), and so is each submit refusal's reason.
func (w *Writer) WriteRound(r RoundRecord) {
	r.ToolCalls = normalizeToolCalls(r.ToolCalls)
	r.SubmitRefusals = clipRefusals(r.SubmitRefusals)
	if w.Enabled() {
		w.stampRound(&r)
	}
	w.write(r)
}

// clipRefusals returns refusals with every reason clipped to maxClipBytes,
// in a fresh slice so a write never mutates what the caller holds. Nil stays
// nil without an allocation: nearly every round refuses nothing.
func clipRefusals(refusals []SubmitRefusal) []SubmitRefusal {
	if refusals == nil {
		return nil
	}
	out := make([]SubmitRefusal, len(refusals))
	for i, r := range refusals {
		r.Reason = clip(r.Reason, maxClipBytes)
		out[i] = r
	}
	return out
}

// normalizeToolCalls returns calls as a non-nil slice, with every entry's
// own Error clipped to maxClipBytes. It always allocates a fresh slice,
// even when calls is already non-nil and none of its entries need
// clipping, so a write never mutates a ToolCall value the caller might
// still hold.
func normalizeToolCalls(calls []ToolCall) []ToolCall {
	out := make([]ToolCall, len(calls))
	for i, c := range calls {
		if c.Error != nil {
			clipped := clip(*c.Error, maxClipBytes)
			c.Error = &clipped
		}
		out[i] = c
	}
	return out
}

// Close closes the currently open file, if any. Safe on a nil, zero-value
// or disabled Writer. Not required for correctness (every write already
// reaches the OS on its own), but lets a caller release the handle at
// shutdown.
func (w *Writer) Close() error {
	if w == nil || !w.enabled {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) write(v any) {
	if w == nil || !w.enabled {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		w.warn("marshal: %v", err)
		return
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := w.fileLocked()
	if err != nil {
		w.warn("open: %v", err)
		return
	}
	if _, err := f.Write(line); err != nil {
		w.warn("write: %v", err)
	}
}

// fileLocked returns the file for w.now(), rolling to a new one first if
// the UTC day has moved on since the file was opened. Called with mu held.
func (w *Writer) fileLocked() (*os.File, error) {
	now := w.now()
	day := now.UTC().Format(dayFormat)
	if w.file != nil && w.day == day {
		return w.file, nil
	}
	if w.file != nil {
		// Best effort: a close error here would only ever be logged, and
		// the handle is about to be replaced either way.
		w.file.Close()
		w.file = nil
	}
	// Player names and question text: the owner's alone. A directory that
	// already exists is left as it is (the default is the config's own
	// directory); a file is narrowed even when an older service made it.
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, fileName(now)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600) // best effort
	w.file = f
	w.day = day
	return f, nil
}

// warn logs a write failure once and swallows every one after it, for the
// life of the process: the first failure is worth an operator's attention,
// a hundred more from the same failing disk are not (Decision 5).
func (w *Writer) warn(format string, args ...any) {
	w.warnOnce.Do(func() {
		log.Printf("ledger: "+format+" (further ledger write errors will not be logged)", args...)
	})
}
