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
	if _, fresh := s.open("global", "", t0, false); !fresh {
		t.Fatal("the first question must start a session")
	}
	s.record("global", "", ex("Alice", "forces?", "player, enemy", t0))
	got, fresh := s.open("global", "", t0.Add(time.Minute), false)
	if fresh || len(got) != 1 || got[0].Asker != "Alice" {
		t.Errorf("Bob should see Alice's exchange: fresh=%v got=%+v", fresh, got)
	}
	if got, fresh := s.open("global", "iron", t0.Add(time.Minute), false); !fresh || len(got) != 0 {
		t.Errorf("#iron must be a separate, fresh session: fresh=%v got=%+v", fresh, got)
	}
	if got, fresh := s.open("team-3", "", t0.Add(time.Minute), false); !fresh || len(got) != 0 {
		t.Errorf("a private scope must not see the global session: fresh=%v got=%+v", fresh, got)
	}
}

// Idle time ends a session; a named one lives longer; "new" ends it at once.
func TestSessionsEndByIdleAndOnNew(t *testing.T) {
	s := newSessions(SessionCaps{Idle: 3 * time.Minute, NamedIdle: 30 * time.Minute})
	s.open("global", "", t0, false)
	s.record("global", "", ex("Alice", "q", "a", t0))
	s.open("global", "iron", t0, false)
	s.record("global", "iron", ex("Alice", "q", "a", t0))

	if got, fresh := s.open("global", "", t0.Add(2*time.Minute), false); fresh || len(got) != 1 {
		t.Errorf("two minutes later the session must still be there: fresh=%v got=%d", fresh, len(got))
	}
	if _, fresh := s.open("global", "", t0.Add(6*time.Minute), false); !fresh {
		t.Error("six minutes of silence must end the default session")
	}
	if got, fresh := s.open("global", "iron", t0.Add(6*time.Minute), false); fresh || len(got) != 1 {
		t.Errorf("a named session lives thirty minutes: fresh=%v got=%d", fresh, len(got))
	}
	if _, fresh := s.open("global", "iron", t0.Add(31*time.Minute), false); !fresh {
		t.Error("thirty-one minutes must end the named session")
	}

	s.record("global", "", ex("Bob", "q2", "a2", t0.Add(7*time.Minute)))
	if !s.end("global", "", t0.Add(7*time.Minute)) {
		t.Error("end must report the session it dropped")
	}
	if s.end("global", "", t0.Add(7*time.Minute)) {
		t.Error("a second end has nothing to drop")
	}
	if got, fresh := s.open("global", "", t0.Add(7*time.Minute), true); !fresh || len(got) != 0 {
		t.Errorf("fresh=true starts over even with a live session: fresh=%v got=%d", fresh, len(got))
	}
}

// The caps drop whole exchanges from the front and never cut one.
func TestSessionsDropWholeExchangesAtTheCaps(t *testing.T) {
	s := newSessions(SessionCaps{MaxExchanges: 3, MaxBytes: 100})
	for i := 0; i < 5; i++ {
		s.record("global", "", ex("A", "q", strings.Repeat("x", 10), t0.Add(time.Duration(i)*time.Second)))
	}
	got, _ := s.open("global", "", t0.Add(5*time.Second), false)
	if len(got) != 3 || got[0].At != t0.Add(2*time.Second) {
		t.Errorf("want the last three exchanges, got %d starting at %v", len(got), got[0].At)
	}

	big := strings.Repeat("y", 90)
	s.record("global", "", ex("A", "q", big, t0.Add(6*time.Second)))
	got, _ = s.open("global", "", t0.Add(6*time.Second), false)
	if len(got) != 1 || got[0].Answer != big {
		t.Errorf("a big answer pushes the others out and stays whole: %d exchanges, answer %d bytes", len(got), len(got[0].Answer))
	}
	huge := strings.Repeat("z", 500)
	s.record("global", "", ex("A", "q", huge, t0.Add(7*time.Second)))
	got, _ = s.open("global", "", t0.Add(7*time.Second), false)
	if len(got) != 1 || len(got[0].Answer) != 500 {
		t.Errorf("an answer over the byte cap is kept alone and uncut: %d exchanges, %d bytes", len(got), len(got[0].Answer))
	}
}

// The listing shows the asker's scope only, default session first.
func TestSessionsListIsPerScope(t *testing.T) {
	s := newSessions(SessionCaps{})
	s.record("global", "oil", ex("Bob", "q", "a", t0))
	s.record("global", "", ex("Alice", "q", "a", t0.Add(time.Second)))
	s.record("global", "iron", ex("Cy", "q", "a", t0))
	s.record("team-3", "", ex("Dee", "q", "a", t0))

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
