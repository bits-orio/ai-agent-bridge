package transport

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// S7: an operation stuck on a half-open connection is cut off at its
// deadline, the connection is closed under it, and the goroutine it ran on
// ends instead of leaking.
func TestWithDeadlineAbortsAStuckOperationAndItsGoroutineEnds(t *testing.T) {
	before := runtime.NumGoroutine()
	closed := make(chan struct{})
	ended := make(chan struct{})
	err := withDeadline(5*time.Millisecond, func() error {
		defer close(ended)
		<-closed // blocked until the connection is closed, as a read on a dead socket is
		return errors.New("use of closed connection")
	}, func() { close(closed) })
	if !errors.Is(err, errOpTimeout) {
		t.Fatalf("err = %v, want errOpTimeout", err)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("the operation's goroutine did not end after the abort")
	}
	waitFor(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before })
}

// An operation that finishes in time returns its own result and never aborts.
func TestWithDeadlinePassesThroughAPromptResult(t *testing.T) {
	want := errors.New("no such file")
	aborted := false
	if err := withDeadline(time.Second, func() error { return want }, func() { aborted = true }); err != want || aborted {
		t.Fatalf("err = %v, aborted = %v", err, aborted)
	}
}

// S11: a reader told to require a host key refuses to connect without one,
// rather than falling back to accepting any key.
func TestAnSFTPReaderThatRequiresAHostKeyRefusesWithoutKnownHosts(t *testing.T) {
	r := newSFTPReader(SFTPConfig{Host: "example.invalid", User: "u", Password: "p", RequireHostKey: true}, "/x")
	if _, err := r.hostKeyCallback(); err == nil {
		t.Fatal("hostKeyCallback accepted any key with RequireHostKey set")
	}
	r = newSFTPReader(SFTPConfig{Host: "example.invalid", User: "u", Password: "p"}, "/x")
	if _, err := r.hostKeyCallback(); err != nil {
		t.Fatalf("the default, no known_hosts_path, must keep accepting with a warning: %v", err)
	}
}
