package history

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return s
}

func rowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func findTool(t *testing.T, ts []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, tool := range ts {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no tool named %q", name)
	return tools.Tool{}
}

func callTool(t *testing.T, tool tools.Tool, args any) json.RawMessage {
	t.Helper()
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args for %s: %v", tool.Name, err)
		}
		raw = b
	}
	out, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("%s.Call: %v", tool.Name, err)
	}
	return out
}

// TestIngest covers the handful-of-lines-including-a-malformed-one case: a
// blank line is silently ignored, a well-formed line is stored, and each of
// invalid JSON, a missing "event" and a non-object "data" is reported as an
// error and leaves no row behind.
func TestIngest(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()

	if err := s.Ingest([]byte("")); err != nil {
		t.Errorf("empty line: got error %v, want nil", err)
	}
	if err := s.Ingest([]byte("   \n")); err != nil {
		t.Errorf("whitespace-only line: got error %v, want nil", err)
	}
	if err := s.Ingest([]byte(`{"event":"player_died","tick":10,"data":{"player":"Bob","force":"player","cause":"biter"}}`)); err != nil {
		t.Fatalf("well-formed line: %v", err)
	}
	if err := s.Ingest([]byte("not json at all")); err == nil {
		t.Error("invalid JSON: got nil error, want error")
	}
	if err := s.Ingest([]byte(`{"tick":11,"data":{}}`)); err == nil {
		t.Error("missing event: got nil error, want error")
	}
	if err := s.Ingest([]byte(`{"event":"x","tick":12,"data":"not-an-object"}`)); err == nil {
		t.Error("non-object data: got nil error, want error")
	}

	if got, want := rowCount(t, s), 1; got != want {
		t.Errorf("row count after mixed ingest = %d, want %d (only the well-formed line stored)", got, want)
	}
}

// seed ingests 25 "ping" rows at ticks 1..25 (alternating player Alice/Bob,
// force "player"), three "player_died" rows and one "other" row, and returns
// the Store's tools.
func seed(t *testing.T, s *Store) []tools.Tool {
	t.Helper()
	for i := 1; i <= 25; i++ {
		player := "Alice"
		if i%2 == 0 {
			player = "Bob"
		}
		line := fmt.Sprintf(`{"event":"ping","tick":%d,"data":{"player":%q,"force":"player"}}`, i, player)
		if err := s.Ingest([]byte(line)); err != nil {
			t.Fatalf("seed ping %d: %v", i, err)
		}
	}
	for _, l := range []string{
		`{"event":"player_died","tick":30,"data":{"player":"Bob","force":"player","cause":"biter"}}`,
		`{"event":"player_died","tick":31,"data":{"player":"Alice","force":"player","cause":"biter"}}`,
		`{"event":"player_died","tick":40,"data":{"player":"Bob","force":"player","cause":"train"}}`,
		`{"event":"other","tick":50,"data":{}}`,
	} {
		if err := s.Ingest([]byte(l)); err != nil {
			t.Fatalf("seed %q: %v", l, err)
		}
	}
	return s.Tools()
}

func TestRecentEvents(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ts := seed(t, s)
	recent := findTool(t, ts, "recent_events")

	tickOrder := func(out json.RawMessage) []int64 {
		var rows []eventRow
		if err := json.Unmarshal(out, &rows); err != nil {
			t.Fatalf("unmarshal recent_events result: %v", err)
		}
		ticks := make([]int64, len(rows))
		for i, r := range rows {
			ticks[i] = r.Tick
		}
		return ticks
	}

	t.Run("default limit is 10, newest first", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping"})
		got := tickOrder(out)
		if len(got) != defaultRecentLimit {
			t.Fatalf("len = %d, want %d", len(got), defaultRecentLimit)
		}
		for i, tick := range got {
			want := int64(25 - i)
			if tick != want {
				t.Errorf("row %d: tick = %d, want %d (newest first)", i, tick, want)
			}
		}
	})

	t.Run("explicit limit under the cap", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping", "limit": 3})
		got := tickOrder(out)
		if want := []int64{25, 24, 23}; !equalInt64(got, want) {
			t.Errorf("ticks = %v, want %v", got, want)
		}
	})

	t.Run("limit above the cap clamps to 20", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping", "limit": 100})
		got := tickOrder(out)
		if len(got) != maxRecentLimit {
			t.Fatalf("len = %d, want %d (capped)", len(got), maxRecentLimit)
		}
		if got[0] != 25 || got[len(got)-1] != 6 {
			t.Errorf("ticks span %v..%v, want 25..6", got[0], got[len(got)-1])
		}
	})

	t.Run("player filter", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping", "player": "Bob", "limit": 20})
		got := tickOrder(out)
		if len(got) != 12 {
			t.Fatalf("len = %d, want 12 (even ticks 2..24)", len(got))
		}
		for _, tick := range got {
			if tick%2 != 0 {
				t.Errorf("tick %d is not one of Bob's (even) ticks", tick)
			}
		}
	})

	t.Run("no rows for an unfiltered but empty event", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "never_happened"})
		got := tickOrder(out)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
		if string(out) != "[]" {
			t.Errorf("empty result = %s, want []", out)
		}
	})
}

func TestLastEvent(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ts := seed(t, s)
	last := findTool(t, ts, "last_event")

	t.Run("newest matching row", func(t *testing.T) {
		out := callTool(t, last, map[string]any{"event": "player_died", "player": "Bob"})
		var r eventRow
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if r.Tick != 40 {
			t.Errorf("tick = %d, want 40 (Bob's newest death, not tick 30)", r.Tick)
		}
	})

	t.Run("none recorded for an unmatched filter", func(t *testing.T) {
		out := callTool(t, last, map[string]any{"event": "player_died", "player": "Nobody"})
		var got map[string]string
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["result"] != "none recorded" {
			t.Errorf("result = %q, want %q", got["result"], "none recorded")
		}
	})

	t.Run("none recorded for an unknown event key", func(t *testing.T) {
		out := callTool(t, last, map[string]any{"event": "never_happened"})
		var got map[string]string
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["result"] != "none recorded" {
			t.Errorf("result = %q, want %q", got["result"], "none recorded")
		}
	})

	t.Run("missing required event argument errors", func(t *testing.T) {
		if _, err := last.Call(context.Background(), []byte(`{}`)); err == nil {
			t.Error("got nil error, want error for missing \"event\"")
		}
	})
}

func TestCountEvents(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ts := seed(t, s)
	count := findTool(t, ts, "count_events")

	decode := func(out json.RawMessage) int64 {
		var got map[string]int64
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal count_events result: %v", err)
		}
		return got["count"]
	}

	if got := decode(callTool(t, count, map[string]any{"event": "ping"})); got != 25 {
		t.Errorf("count(ping) = %d, want 25", got)
	}
	if got := decode(callTool(t, count, map[string]any{"event": "player_died", "force": "player"})); got != 3 {
		t.Errorf("count(player_died, force=player) = %d, want 3", got)
	}
	if got := decode(callTool(t, count, map[string]any{"event": "ping", "since_tick": 20})); got != 6 {
		t.Errorf("count(ping, since_tick=20) = %d, want 6 (ticks 20..25)", got)
	}
	if got := decode(callTool(t, count, map[string]any{"event": "never_happened"})); got != 0 {
		t.Errorf("count(never_happened) = %d, want 0", got)
	}
	if _, err := count.Call(context.Background(), []byte(`{}`)); err == nil {
		t.Error("got nil error, want error for missing \"event\"")
	}
}

// TestOpenExistingFileKeepsRows: Ingest into a file-backed store, close it,
// reopen the same path and confirm the earlier rows are still queryable.
func TestOpenExistingFileKeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")

	s1 := mustOpen(t, path)
	if err := s1.Ingest([]byte(`{"event":"player_joined","tick":5,"data":{"player":"Alice","force":"player"}}`)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2 := mustOpen(t, path)
	defer s2.Close()

	if got, want := rowCount(t, s2), 1; got != want {
		t.Fatalf("row count after reopen = %d, want %d", got, want)
	}

	last := findTool(t, s2.Tools(), "last_event")
	out := callTool(t, last, map[string]any{"event": "player_joined"})
	var r eventRow
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Player != "Alice" || r.Tick != 5 {
		t.Errorf("row = %+v, want player Alice at tick 5", r)
	}
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
