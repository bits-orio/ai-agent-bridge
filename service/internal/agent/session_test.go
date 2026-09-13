package agent

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

func ex(asker, q, a string, at time.Time) Exchange {
	return Exchange{Asker: asker, Question: q, Answer: a, At: at}
}

// Two askers in one scope share the default session; a named session is
// its own; a private scope never sees the global one.
func TestSessionsShareByScopeAndName(t *testing.T) {
	s := newSessions(SessionCaps{})
	if _, fresh, _ := s.open("global", "", t0, false); !fresh {
		t.Fatal("the first question must start a session")
	}
	s.record("global", "", ex("Alice", "forces?", "player, enemy", t0), false)
	got, fresh, _ := s.open("global", "", t0.Add(time.Minute), false)
	if fresh || len(got) != 1 || got[0].Asker != "Alice" {
		t.Errorf("Bob should see Alice's exchange: fresh=%v got=%+v", fresh, got)
	}
	if got, fresh, _ := s.open("global", "iron", t0.Add(time.Minute), false); !fresh || len(got) != 0 {
		t.Errorf("#iron must be a separate, fresh session: fresh=%v got=%+v", fresh, got)
	}
	if got, fresh, _ := s.open("team-3", "", t0.Add(time.Minute), false); !fresh || len(got) != 0 {
		t.Errorf("a private scope must not see the global session: fresh=%v got=%+v", fresh, got)
	}
}

// Idle time ends a session; a named one lives longer; "new" ends it at once.
func TestSessionsEndByIdleAndOnNew(t *testing.T) {
	s := newSessions(SessionCaps{Idle: 3 * time.Minute, NamedIdle: 30 * time.Minute})
	s.open("global", "", t0, false)
	s.record("global", "", ex("Alice", "q", "a", t0), false)
	s.open("global", "iron", t0, false)
	s.record("global", "iron", ex("Alice", "q", "a", t0), false)

	if got, fresh, _ := s.open("global", "", t0.Add(2*time.Minute), false); fresh || len(got) != 1 {
		t.Errorf("two minutes later the session must still be there: fresh=%v got=%d", fresh, len(got))
	}
	if _, fresh, _ := s.open("global", "", t0.Add(6*time.Minute), false); !fresh {
		t.Error("six minutes of silence must end the default session")
	}
	if got, fresh, _ := s.open("global", "iron", t0.Add(6*time.Minute), false); fresh || len(got) != 1 {
		t.Errorf("a named session lives thirty minutes: fresh=%v got=%d", fresh, len(got))
	}
	if _, fresh, _ := s.open("global", "iron", t0.Add(31*time.Minute), false); !fresh {
		t.Error("thirty-one minutes must end the named session")
	}

	s.record("global", "", ex("Bob", "q2", "a2", t0.Add(7*time.Minute)), false)
	if !s.end("global", "", t0.Add(7*time.Minute)) {
		t.Error("end must report the session it dropped")
	}
	if s.end("global", "", t0.Add(7*time.Minute)) {
		t.Error("a second end has nothing to drop")
	}
	if got, fresh, _ := s.open("global", "", t0.Add(7*time.Minute), true); !fresh || len(got) != 0 {
		t.Errorf("fresh=true starts over even with a live session: fresh=%v got=%d", fresh, len(got))
	}
}

// idleFor is section 12's own widening rule: an ask-back gets ClarifyIdle
// in place of the ordinary window, a named session keeps whichever of
// ClarifyIdle and its own NamedIdle is longer, and the extension never
// shrinks a window that was already longer on its own.
func TestIdleForAwaitingReplyTakesTheLonger(t *testing.T) {
	s := newSessions(SessionCaps{Idle: 3 * time.Minute, NamedIdle: 5 * time.Minute, ClarifyIdle: 10 * time.Minute})
	if got := s.idleFor("", false); got != 3*time.Minute {
		t.Errorf("unnamed, not awaiting = %s, want 3m", got)
	}
	if got := s.idleFor("", true); got != 10*time.Minute {
		t.Errorf("unnamed, awaiting = %s, want the clarify window, 10m", got)
	}
	if got := s.idleFor("iron", false); got != 5*time.Minute {
		t.Errorf("named, not awaiting = %s, want 5m", got)
	}
	if got := s.idleFor("iron", true); got != 10*time.Minute {
		t.Errorf("named, awaiting, clarify longer than named = %s, want 10m", got)
	}

	// A named session's own window, already longer than ClarifyIdle, must
	// never be shortened by the ask-back extension.
	s2 := newSessions(SessionCaps{Idle: 3 * time.Minute, NamedIdle: 30 * time.Minute, ClarifyIdle: 10 * time.Minute})
	if got := s2.idleFor("iron", true); got != 30*time.Minute {
		t.Errorf("named, awaiting, named already longer = %s, want 30m (never shrink)", got)
	}
}

// open reports whether a session was awaiting a reply, and clears the flag
// on the way out: whatever this question turns out to ask, its arrival
// resolves the wait (docs/design/phase4-spec.md section 12).
func TestOpenReportsAndClearsAwaitingReply(t *testing.T) {
	s := newSessions(SessionCaps{Idle: time.Minute, ClarifyIdle: 10 * time.Minute})
	s.open("global", "", t0, false)
	s.record("global", "", ex("Alice", "where should I look?", "which surface?", t0), true)

	// Five minutes on: past the ordinary one-minute idle, inside the
	// ten-minute clarify window, and the flag must come back true.
	got, fresh, wasAwaiting := s.open("global", "", t0.Add(5*time.Minute), false)
	if fresh || len(got) != 1 || !wasAwaiting {
		t.Fatalf("an ask-back session must survive past the ordinary idle and report it: fresh=%v got=%d wasAwaiting=%v", fresh, len(got), wasAwaiting)
	}
	// The flag cleared on the way out: asking again right away must not
	// still read as awaiting a reply.
	if _, _, wasAwaiting := s.open("global", "", t0.Add(5*time.Minute), false); wasAwaiting {
		t.Error("the flag must have cleared: a second open must not still report awaiting a reply")
	}
}

// The caps drop whole exchanges from the front and never cut one.
func TestSessionsDropWholeExchangesAtTheCaps(t *testing.T) {
	s := newSessions(SessionCaps{MaxExchanges: 3, MaxBytes: 100})
	for i := 0; i < 5; i++ {
		s.record("global", "", ex("A", "q", strings.Repeat("x", 10), t0.Add(time.Duration(i)*time.Second)), false)
	}
	got, _, _ := s.open("global", "", t0.Add(5*time.Second), false)
	if len(got) != 3 || got[0].At != t0.Add(2*time.Second) {
		t.Errorf("want the last three exchanges, got %d starting at %v", len(got), got[0].At)
	}

	big := strings.Repeat("y", 90)
	s.record("global", "", ex("A", "q", big, t0.Add(6*time.Second)), false)
	got, _, _ = s.open("global", "", t0.Add(6*time.Second), false)
	if len(got) != 1 || got[0].Answer != big {
		t.Errorf("a big answer pushes the others out and stays whole: %d exchanges, answer %d bytes", len(got), len(got[0].Answer))
	}
	huge := strings.Repeat("z", 500)
	s.record("global", "", ex("A", "q", huge, t0.Add(7*time.Second)), false)
	got, _, _ = s.open("global", "", t0.Add(7*time.Second), false)
	if len(got) != 1 || len(got[0].Answer) != 500 {
		t.Errorf("an answer over the byte cap is kept alone and uncut: %d exchanges, %d bytes", len(got), len(got[0].Answer))
	}
}

// The listing shows the asker's scope only, default session first.
func TestSessionsListIsPerScope(t *testing.T) {
	s := newSessions(SessionCaps{})
	s.record("global", "oil", ex("Bob", "q", "a", t0), false)
	s.record("global", "", ex("Alice", "q", "a", t0.Add(time.Second)), false)
	s.record("global", "iron", ex("Cy", "q", "a", t0), false)
	s.record("team-3", "", ex("Dee", "q", "a", t0), false)

	got := s.list("global", t0.Add(time.Minute))
	if len(got) != 3 || got[0].Name != "" || got[1].Name != "iron" || got[2].Name != "oil" {
		t.Fatalf("listing = %+v", got)
	}
	if got[0].LastAsker != "Alice" || got[0].Idle != 59*time.Second || got[0].Exchanges != 1 {
		t.Errorf("default row = %+v", got[0])
	}
	if private := s.list("team-3", t0.Add(time.Minute)); len(private) != 1 || private[0].LastAsker != "Dee" {
		t.Errorf("team listing = %+v", private)
	}
}
