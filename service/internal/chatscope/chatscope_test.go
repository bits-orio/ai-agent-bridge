package chatscope

import (
	"context"
	"testing"
)

// A context nobody marked reads as a public question: the fail-closed default
// every history read relies on.
func TestAnUnmarkedContextIsPublic(t *testing.T) {
	s := From(context.Background())
	if s != (Scope{}) {
		t.Errorf("From(Background) = %+v, want the zero, public scope", s)
	}
	if key, ok := s.PrivateKey(); ok {
		t.Errorf("an unmarked context may read private rows of %q", key)
	}
}

func TestWithRoundTrips(t *testing.T) {
	want := Scope{Key: "team-4", Private: true}
	if got := From(With(context.Background(), want)); got != want {
		t.Errorf("From(With(%+v)) = %+v", want, got)
	}
}

// Only a private scope with a key of its own reads private rows.
func TestPrivateKey(t *testing.T) {
	for _, c := range []struct {
		s    Scope
		key  string
		read bool
	}{
		{Scope{Key: "team-4", Private: true}, "team-4", true},
		{Scope{Key: "team-4"}, "", false},
		{Scope{Key: GlobalKey}, "", false},
		{Scope{Key: GlobalKey, Private: true}, "", false},
		{Scope{Private: true}, "", false},
	} {
		key, ok := c.s.PrivateKey()
		if key != c.key || ok != c.read {
			t.Errorf("%+v.PrivateKey() = %q, %v; want %q, %v", c.s, key, ok, c.key, c.read)
		}
	}
}
