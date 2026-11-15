// Copied from open-discord-bridge/bridge/internal/transport/transport.go. Changed
// since: where the tail starts when the first Stat fails, a second truncation
// signal (the file's first line), and the caught-up signal history waits for.

// Package transport reads the companion mod's JSONL events file and delivers complete
// lines to the service. The read mechanism is pluggable (Local file, SFTP); the polling,
// offset tracking, truncation detection, and line buffering are shared.
package transport

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log"
	"time"
)

// headBytes bounds the first line the tailer remembers to tell a new session's
// file from the one it was reading.
const headBytes = 256

// reader abstracts "how to read the events file". Implementations handle their own
// connection lifecycle (e.g. SFTP reconnect) and should return an error, never panic,
// when the source is temporarily unavailable. A file that is not there reports an
// error that matches fs.ErrNotExist.
type reader interface {
	// Stat returns the current file size, used to detect growth and truncation.
	Stat() (int64, error)
	// Read returns the bytes from offset to the current end of file.
	Read(offset int64) ([]byte, error)
	// Head returns the file's first n bytes, fewer when it is shorter.
	Head(n int) ([]byte, error)
	// Close releases any underlying connection.
	Close() error
}

// Tailer polls a reader and emits each complete JSONL line via onLine. It starts at the
// end of file as the first successful Stat finds it (so a restart doesn't replay
// history), or at the top of a file that does not exist yet, and resets to offset 0
// when the file shrinks or its first line changes (the mod truncates once per game
// session).
type Tailer struct {
	reader   reader
	interval time.Duration
	caughtUp func(time.Time)
}

// OnCaughtUp asks the tailer to call fn(at) whenever every line written before
// at has been delivered: at is taken just before a Stat, and fn runs once the
// bytes up to that size have been emitted with no partial line left over.
// History uses it to know when the lines it was still owed from before a
// status call have all arrived. Set it before Run.
func (t *Tailer) OnCaughtUp(fn func(at time.Time)) { t.caughtUp = fn }

// tailState is where one Run has got to in the file.
type tailState struct {
	started bool   // an offset has been chosen
	offset  int64  // bytes already delivered or deliberately skipped
	head    []byte // the file's first line (or headBytes of it) when offset was chosen; nil until known
	buf     []byte // a partial line waiting for its newline
}

func (t *Tailer) Run(ctx context.Context, onLine func([]byte)) {
	defer t.reader.Close()

	var st tailState
	t.poll(&st, onLine)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.poll(&st, onLine)
		}
	}
}

// poll is one look at the file.
func (t *Tailer) poll(st *tailState, onLine func([]byte)) {
	at := time.Now()
	sz, err := t.reader.Stat()
	if !st.started {
		t.start(st, sz, err, at)
		return
	}
	if err != nil {
		log.Printf("transport: stat error (will retry): %v", err)
		return
	}
	if sz < st.offset || (sz > st.offset && t.headChanged(st)) {
		// Truncation: a new session. A file grown back past the old offset
		// between two polls is caught by its first line, not its size.
		st.offset, st.head = 0, nil
		st.buf = st.buf[:0]
	}
	if sz == st.offset {
		t.markCaughtUp(st, at)
		return
	}
	data, err := t.reader.Read(st.offset)
	if err != nil {
		log.Printf("transport: read error: %v", err)
		return
	}
	st.offset += int64(len(data))
	st.buf = emitLines(append(st.buf, data...), onLine)
	t.rememberHead(st)
	t.markCaughtUp(st, at)
}

// start picks where the tail begins. A failed Stat never chooses byte 0:
// right after a restart the file still holds the previous session, another
// map's private chat among it, and replaying it would place that session
// after the map history already moved to. So the tailer keeps trying, and
// the first size it reads is where it starts. Only a file that is not there
// yet starts at 0, so the session line a new file opens with is read.
func (t *Tailer) start(st *tailState, sz int64, err error, at time.Time) {
	switch {
	case err == nil:
		st.offset = sz
	case errors.Is(err, fs.ErrNotExist):
		st.offset = 0
	default:
		log.Printf("transport: stat error before the first read, not starting yet (will retry): %v", err)
		return
	}
	st.started = true
	t.rememberHead(st)
	// Everything before this size is skipped on purpose, which delivers it
	// as surely as reading it would.
	t.markCaughtUp(st, at)
}

// rememberHead records the file's first line once the tail has passed it. A
// first line with no newline yet may still grow, and would read as changed,
// so it is only remembered once it is whole or headBytes long.
func (t *Tailer) rememberHead(st *tailState) {
	if st.head != nil || st.offset == 0 {
		return
	}
	b, err := t.reader.Head(int(min(st.offset, headBytes)))
	if err != nil {
		return // tried again next poll
	}
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		st.head = b[:i+1]
	} else if len(b) == headBytes {
		st.head = b
	}
}

// headChanged reports whether the file no longer starts with the line it
// started with when the offset was chosen. A read that fails says nothing.
func (t *Tailer) headChanged(st *tailState) bool {
	if st.head == nil {
		t.rememberHead(st)
		return false
	}
	b, err := t.reader.Head(len(st.head))
	return err == nil && !bytes.Equal(b, st.head)
}

// markCaughtUp tells the caller every line written before at is delivered,
// unless a partial line is still owed.
func (t *Tailer) markCaughtUp(st *tailState, at time.Time) {
	if t.caughtUp != nil && len(st.buf) == 0 {
		t.caughtUp(at)
	}
}

// emitLines emits each complete (newline-terminated) line in buf and returns the
// remaining partial line.
func emitLines(buf []byte, onLine func([]byte)) []byte {
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(buf[:i])
		rest := buf[i+1:]
		if len(line) > 0 {
			cp := make([]byte, len(line))
			copy(cp, line)
			onLine(cp)
		}
		buf = append(buf[:0], rest...)
	}
	return buf
}
