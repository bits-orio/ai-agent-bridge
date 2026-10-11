package history

// The map a status reply names (Store.NoteMap): a session row of the
// service's own that moves every read off an earlier map when the file never
// said the map changed.

import (
	"path/filepath"
	"strings"
	"testing"
)

// oldMap is a mature map's rows under the map id M1: public chat, and
// team-4's private chat and question.
var oldMap = []string{
	`{"event":"session","tick":4000000,"data":{"map_id":"M1"}}`,
	`{"event":"console_chat","tick":4000100,"data":{"player":"Alice","force":"team-4","message":"OLD public hello","scope":"global","private":false}}`,
	`{"event":"console_chat","tick":4000200,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 secret","scope":"team-4","private":true}}`,
	`{"event":"question","tick":4000300,"data":{"qid":1,"player":"Bob","force":"team-4","text":"OLD-T4 question","scope":"team-4","private":true}}`,
}

// newT4Secret is team-4's private chat on the map after it.
const newT4Secret = `{"event":"console_chat","tick":310,"data":{"player":"Bob","force":"team-4","message":"NEW-T4 secret","scope":"team-4","private":true}}`

func oldMapStore(t *testing.T) *Store {
	t.Helper()
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = s.Close() })
	for _, l := range oldMap {
		ingest(t, s, l)
	}
	return s
}

// noteMap records a status taken now, with nothing stored while the call
// was out, from a companion that has issued lastID questions.
func noteMap(t *testing.T, s *Store, mapID string, tick, lastID int64) bool {
	t.Helper()
	moved, err := s.NoteMap(s.Mark(), mapID, tick, lastID)
	if err != nil {
		t.Fatalf("NoteMap(%q, %d, %d): %v", mapID, tick, lastID, err)
	}
	return moved
}

// noReadReachesTheOldMap reads everything under every audience.
func noReadReachesTheOldMap(t *testing.T, s *Store, when string) {
	t.Helper()
	for _, a := range audiences() {
		if seen := readEverything(t, s, a.ctx); strings.Contains(seen, "OLD") {
			t.Errorf("%s, %s read the earlier map:\n%s", when, a.name, seen)
		}
	}
}

// A map the file never named, its session line missed or never written,
// still ends every read of the earlier map once the status names it. The
// session line that arrives after the status row, at a lower tick, joins
// its epoch, and so do the lines after it.
func TestAStatusNamingAnotherMapEndsEveryReadOfTheEarlierOne(t *testing.T) {
	s := oldMapStore(t)
	if !noteMap(t, s, "M2", 900, 0) {
		t.Error("NoteMap on another map said no read moved")
	}
	noReadReachesTheOldMap(t, s, "after the status named another map")

	ingest(t, s, session(300, "M2").jsonl())
	ingest(t, s, newT4Secret)
	for _, tick := range []int64{300, 310} {
		if got, want := epochAt(t, s, tick), epochAt(t, s, 900); got != want {
			t.Errorf("the row at tick %d is in epoch %d, want %d, the status row's", tick, got, want)
		}
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") || strings.Contains(seen, "OLD") {
		t.Errorf("team-4 reads the wrong map's chat after the session line came in:\n%s", seen)
	}
	// The status row is stored and classified like a line of the file: a
	// public session row every question may see.
	out := string(callTool(t, findTool(t, s.Tools(), "recent_events"), map[string]any{"event": "session", "limit": 20}))
	if !strings.Contains(out, `"source":"status"`) || strings.Count(out, "\n") != 2 {
		t.Errorf("recent_events(session) = %q, want the status row and the session line, both public", out)
	}
}

// Two maps made from one seed share an id, so a status on the same map
// after the file's later ticks, in its first five minutes, is a new map. Its
// session line, at a lower tick still, joins the epoch the status started.
func TestAStatusOnTheSameMapInItsFirstMinutesStartsAnEpoch(t *testing.T) {
	s := oldMapStore(t)
	if !noteMap(t, s, "M1", 900, 1) {
		t.Error("NoteMap on a same-seed map in its first minutes said no read moved")
	}
	noReadReachesTheOldMap(t, s, "after a same-seed map's status")
	ingest(t, s, session(300, "M1").jsonl())
	if got, want := epochAt(t, s, 300), epochAt(t, s, 900); got != want {
		t.Errorf("the session line is in epoch %d, want %d, the status row's", got, want)
	}
}

// Every status read calls NoteMap, so it records nothing that moves
// nothing: the map the epoch already has, the same map rolled back while
// mature, or no map at all, from a companion too old to send one. A status
// with no map must not start an epoch by its tick alone either.
func TestNoteMapRecordsOnlyWhatMovesTheEpoch(t *testing.T) {
	s := oldMapStore(t)
	for _, c := range []struct {
		name  string
		mapID string
		tick  int64
	}{
		{"the map the epoch has", "M1", 4_000_500},
		{"the same map rolled back far, still mature", "M1", 1_000_000},
		{"no map, at a tick a plain line would start a new map at", "", 600},
	} {
		before := rowCount(t, s)
		if noteMap(t, s, c.mapID, c.tick, 1) || rowCount(t, s) != before {
			t.Errorf("%s: moved reads or recorded a row (%d rows, was %d)", c.name, rowCount(t, s), before)
		}
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "OLD-T4 secret") {
		t.Errorf("a status that moved nothing lost the map's own rows:\n%s", seen)
	}

	noteMap(t, s, "M2", 900, 0)
	before := rowCount(t, s)
	if noteMap(t, s, "M2", 2_000, 0) || rowCount(t, s) != before {
		t.Errorf("a second status on the new map recorded again (%d rows, was %d)", rowCount(t, s), before)
	}
}

// An epoch an older companion logged has no map. A status names it and the
// row is recorded, but the epoch stays, so no read moves.
func TestAStatusNamingAnUnnamedEpochMovesNoRead(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingest(t, s, `{"event":"console_chat","tick":10000,"data":{"player":"Ann","force":"player","message":"kept","scope":"global","private":false}}`)
	if noteMap(t, s, "A", 12_000, 0) {
		t.Error("naming the epoch said reads moved")
	}
	if rowCount(t, s) != 2 || epochAt(t, s, 12_000) != 1 {
		t.Errorf("want the status row recorded in epoch 1, got %d rows, epoch %d", rowCount(t, s), epochAt(t, s, 12_000))
	}
	if seen := readEverything(t, s, scoped("player", true)); !strings.Contains(seen, "kept") {
		t.Errorf("naming the epoch lost its rows:\n%s", seen)
	}
}

// On a store with no rows there is no earlier map to move off. The row is
// still recorded, so the session line after it joins the epoch it named.
func TestNoteMapOnAnEmptyStoreMovesNoRead(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	if noteMap(t, s, "M1", 500, 0) {
		t.Error("NoteMap on an empty store said reads moved off an earlier map")
	}
	ingest(t, s, session(100, "M1").jsonl())
	if rowCount(t, s) != 2 || epochAt(t, s, 100) != 1 {
		t.Errorf("want the status row and the session line in epoch 1, got %d rows, epoch %d", rowCount(t, s), epochAt(t, s, 100))
	}
}
