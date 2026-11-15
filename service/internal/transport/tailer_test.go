package transport

// The tailer's offset rules: where it starts (S1), when it starts again from
// the top (S6), and when it says every line written before a moment has been
// delivered (the caught-up signal history's quarantine waits for).

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFile is an events file the test rewrites at will. statErrs are handed
// out, one per Stat, before any Stat succeeds; missing makes Stat say the
// file is not there.
type fakeFile struct {
	mu       sync.Mutex
	data     []byte
	missing  bool
	statErrs []error
	stats    int
}

func (f *fakeFile) set(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data, f.missing = []byte(s), false
}

func (f *fakeFile) appendLine(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, s...)
}

func (f *fakeFile) Stat() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats++
	if len(f.statErrs) > 0 {
		err := f.statErrs[0]
		f.statErrs = f.statErrs[1:]
		return 0, err
	}
	if f.missing {
		return 0, fs.ErrNotExist
	}
	return int64(len(f.data)), nil
}

func (f *fakeFile) Read(offset int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return nil, fs.ErrNotExist
	}
	if offset > int64(len(f.data)) {
		return nil, nil
	}
	return append([]byte(nil), f.data[offset:]...), nil
}

func (f *fakeFile) Head(n int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), f.data[:min(n, len(f.data))]...), nil
}

func (f *fakeFile) Close() error { return nil }

// tail runs a tailer over f until stop is called, collecting every line.
type tail struct {
	mu     sync.Mutex
	lines  []string
	caught []time.Time
	stop   func()
	done   chan struct{}
}

func startTail(t *testing.T, f *fakeFile) *tail {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	tl := &tail{done: make(chan struct{})}
	tr := &Tailer{reader: f, interval: time.Millisecond}
	tr.OnCaughtUp(func(at time.Time) {
		tl.mu.Lock()
		tl.caught = append(tl.caught, at)
		tl.mu.Unlock()
	})
	go func() {
		defer close(tl.done)
		tr.Run(ctx, func(line []byte) {
			tl.mu.Lock()
			tl.lines = append(tl.lines, string(line))
			tl.mu.Unlock()
		})
	}()
	tl.stop = func() { cancel(); <-tl.done }
	t.Cleanup(tl.stop)
	return tl
}

func (tl *tail) got() []string {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return append([]string(nil), tl.lines...)
}

func (tl *tail) caughtUpSince(at time.Time) bool {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	for _, c := range tl.caught {
		if c.After(at) {
			return true
		}
	}
	return false
}

// waitFor polls cond for up to a second, in millisecond steps.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitStats waits until the tailer has called Stat at least n times.
func waitStats(t *testing.T, f *fakeFile, n int) {
	t.Helper()
	waitFor(t, "stats", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.stats >= n
	})
}

// S1: a first Stat that fails must not leave the tailer at byte 0. Right
// after a restart the file still holds the previous session, and reading it
// from the top replays another map's private chat into history.
func TestATailerWhoseFirstStatFailsStartsAtTheEndOnceOneSucceeds(t *testing.T) {
	cases := []struct {
		name string
		errs []error
	}{
		{"one transient failure", []error{errors.New("ssh: connection refused")}},
		{"several failures", []error{errors.New("dial"), errors.New("dial"), errors.New("handshake")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeFile{data: []byte("OLD session line\nOLD private chat\n"), statErrs: c.errs}
			tl := startTail(t, f)
			waitStats(t, f, len(c.errs)+2)
			f.appendLine("NEW line\n")
			waitFor(t, "the new line", func() bool { return len(tl.got()) > 0 })
			tl.stop()
			if got := tl.got(); len(got) != 1 || got[0] != "NEW line" {
				t.Fatalf("lines = %q, want only the line written after the tailer started", got)
			}
		})
	}
}

// S1: a file that is not there yet is read from its first byte once it
// appears, so the session line a new map writes first is never missed.
func TestATailerStartsAtTheTopOfAFileCreatedAfterIt(t *testing.T) {
	f := &fakeFile{missing: true}
	tl := startTail(t, f)
	waitStats(t, f, 3)
	f.set("session line\nfirst chat\n")
	waitFor(t, "both lines", func() bool { return len(tl.got()) == 2 })
	if got := tl.got(); got[0] != "session line" || got[1] != "first chat" {
		t.Fatalf("lines = %q", got)
	}
}

// S6: a new session's file can grow back past the old offset between two
// polls. Its first line is another, so the tailer starts again from the top
// rather than reading the new file from the middle.
func TestATailerStartsAgainWhenTheFileHeadChangesWithoutShrinking(t *testing.T) {
	f := &fakeFile{data: []byte(`{"event":"session","tick":100,"data":{"map_id":"A"}}` + "\nA line\n")}
	tl := startTail(t, f)
	waitStats(t, f, 3)
	f.set(`{"event":"session","tick":5,"data":{"map_id":"B"}}` + "\nB one\nB two\nB three, long enough to pass the old offset\n")
	waitFor(t, "the new session", func() bool { return len(tl.got()) >= 4 })
	got := tl.got()
	if !strings.Contains(got[0], `"map_id":"B"`) || got[3] != "B three, long enough to pass the old offset" {
		t.Fatalf("lines = %q, want the new session read from its first line", got)
	}
}

// S6: an unchanged head is no truncation: plain growth is read once.
func TestATailerReadsGrowthOnceWhenTheHeadIsUnchanged(t *testing.T) {
	f := &fakeFile{data: []byte("head line\n")}
	tl := startTail(t, f)
	waitStats(t, f, 2)
	for _, l := range []string{"one\n", "two\n", "three\n"} {
		f.appendLine(l)
		waitStats(t, f, f.stats+2)
	}
	waitFor(t, "three lines", func() bool { return len(tl.got()) >= 3 })
	waitStats(t, f, f.stats+3)
	if got := tl.got(); strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("lines = %q, want each appended line once", got)
	}
}

// The caught-up signal: the tailer says when every line written before a
// Stat it took has been delivered, and never while a partial line is
// still waiting for its newline.
func TestATailerSaysWhenItHasCaughtUp(t *testing.T) {
	f := &fakeFile{data: []byte("x\n")}
	tl := startTail(t, f)
	waitStats(t, f, 2)
	mark := time.Now()
	f.appendLine("partial")
	waitStats(t, f, f.stats+3)
	if tl.caughtUpSince(mark) {
		t.Fatal("caught up while a partial line was still owed")
	}
	f.appendLine(" now whole\n")
	waitFor(t, "caught up", func() bool { return tl.caughtUpSince(mark) })
	if got := tl.got(); len(got) != 1 || got[0] != "partial now whole" {
		t.Fatalf("lines = %q", got)
	}
}
