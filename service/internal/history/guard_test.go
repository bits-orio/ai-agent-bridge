package history

// The two guards on an epoch a status row started or named (epoch.go,
// "Lines a status got ahead of"): the lines the tailer still owed from
// before the status are quarantined (S5), and a file session line that names
// another map than the status did is held until a status confirms it (S1).

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lateOld is what the tailer still owed from the old map when the status
// named the new one: team-4's private chat and question, then the answer.
var lateOld = []string{
	`{"event":"console_chat","tick":4000400,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 late secret","scope":"team-4","private":true}}`,
	`{"event":"question","tick":4000500,"data":{"qid":2,"player":"Bob","force":"team-4","text":"OLD-T4 late question","scope":"team-4","private":true}}`,
	`{"event":"answer","tick":4000600,"data":{"qid":2,"shape":"summary"}}`,
	`{"event":"console_chat","tick":4000700,"data":{"player":"Alice","force":"team-4","message":"OLD late public","scope":"global","private":false}}`,
}

// caughtUp tells the store the tailer has delivered every line written up
// to now.
func caughtUp(s *Store) { s.CaughtUp(time.Now().Add(time.Millisecond)) }

// S5: an SFTP stall leaves the tailer behind when the status names a new map
// that has written no line yet. What it delivers next is the old map's, and
// none of it may read as the new map's private chat.
func TestLinesOwedFromBeforeAStatusNeverReadInTheEpochItStarted(t *testing.T) {
	s := oldMapStore(t)
	noteMap(t, s, "M2", 900, 0)
	for _, l := range lateOld {
		ingest(t, s, l)
	}
	noReadReachesTheOldMap(t, s, "after the owed lines of the old map came in")

	// Once the tailer has caught up past the status, what follows was
	// written after it: the new map's, read as usual.
	caughtUp(s)
	ingest(t, s, newT4Secret)
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") || strings.Contains(seen, "OLD") {
		t.Errorf("team-4 after the tailer caught up:\n%s", seen)
	}
}

// S5: a caught-up signal from before the status ends nothing.
func TestACaughtUpSignalFromBeforeTheStatusKeepsTheQuarantine(t *testing.T) {
	s := oldMapStore(t)
	before := time.Now()
	noteMap(t, s, "M2", 900, 0)
	s.CaughtUp(before)
	ingest(t, s, lateOld[0])
	noReadReachesTheOldMap(t, s, "after a stale caught-up signal")
}

// S5, the normal cases: nothing is lost. The same map after a service
// restart starts no epoch, so no quarantine; a new map's own session line
// ends one; and a store opened again starts with none, its tailer starting
// at the end of the file.
func TestLinesKeepFlowingAfterARestartWithNothingLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	for _, l := range oldMap {
		ingest(t, s, l)
	}
	_ = s.Close()

	t.Run("same map, service restarted mid-session", func(t *testing.T) {
		s := mustOpen(t, path)
		defer s.Close()
		noteMap(t, s, "M1", 4_000_350, 1)
		ingest(t, s, `{"event":"console_chat","tick":4000360,"data":{"player":"Bob","force":"team-4","message":"STILL-T4 secret","scope":"team-4","private":true}}`)
		if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "STILL-T4 secret") || !strings.Contains(seen, "OLD-T4 secret") {
			t.Errorf("team-4 lost its own map's rows across a restart:\n%s", seen)
		}
	})
	t.Run("a new map's own session line ends the quarantine", func(t *testing.T) {
		s := oldMapStore(t)
		noteMap(t, s, "M2", 900, 0)
		ingest(t, s, session(300, "M2").jsonl())
		ingest(t, s, newT4Secret)
		if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") {
			t.Errorf("the new map's chat after its session line is unreadable:\n%s", seen)
		}
	})
	t.Run("a store opened again starts with no quarantine", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "h.sqlite")
		s := mustOpen(t, dir)
		for _, l := range oldMap {
			ingest(t, s, l)
		}
		noteMap(t, s, "M2", 900, 0)
		_ = s.Close()
		s = mustOpen(t, dir)
		defer s.Close()
		ingest(t, s, newT4Secret)
		if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") || strings.Contains(seen, "OLD") {
			t.Errorf("team-4 after reopening:\n%s", seen)
		}
	})
}

// S1: the tailer replays the old file from its first byte after the status
// named the new map. The old session line names the old map, and the epoch
// it would start must not bring the old map's private chat back.
func TestAReplayedOldSessionNeverResurrectsTheOldMap(t *testing.T) {
	s := oldMapStore(t)
	noteMap(t, s, "M2", 900, 0)
	caughtUp(s) // the replay comes after the tail caught up: the quarantine alone is no help
	for _, l := range oldMap {
		ingest(t, s, l)
	}
	for _, l := range lateOld {
		ingest(t, s, l)
	}
	noReadReachesTheOldMap(t, s, "after the old session was replayed")

	// The next status still says M2: the replay stays unreadable for good,
	// and the new map's own lines read again.
	noteMap(t, s, "M2", 1_000, 0)
	noReadReachesTheOldMap(t, s, "after the next status named M2 again")
	caughtUp(s)
	ingest(t, s, newT4Secret)
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") {
		t.Errorf("the new map's chat after the status named it again:\n%s", seen)
	}
}

// S1, the legitimate case: the server really did go back to the older map.
// Its rows are held until the next status names it too, and then read,
// every one of them.
func TestASwitchBackToAnOlderMapReadsOnceAStatusConfirmsIt(t *testing.T) {
	s := oldMapStore(t)
	noteMap(t, s, "M2", 900, 0)
	caughtUp(s)
	ingest(t, s, session(4_100_000, "M1").jsonl())
	ingest(t, s, `{"event":"console_chat","tick":4100100,"data":{"player":"Bob","force":"team-4","message":"BACK-T4 secret","scope":"team-4","private":true}}`)
	ingest(t, s, `{"event":"console_chat","tick":4100200,"data":{"player":"Alice","force":"team-4","message":"BACK public","scope":"global","private":false}}`)
	for _, a := range audiences() {
		if seen := readEverything(t, s, a.ctx); strings.Contains(seen, "BACK") {
			t.Errorf("%s read the switched-back map before a status confirmed it:\n%s", a.name, seen)
		}
	}

	noteMap(t, s, "M1", 4_100_300, 1)
	seen := readEverything(t, s, scoped("team-4", true))
	if !strings.Contains(seen, "BACK-T4 secret") || !strings.Contains(seen, "BACK public") {
		t.Errorf("team-4 after the status confirmed the switch back:\n%s", seen)
	}
	if strings.Contains(seen, "OLD") {
		t.Errorf("the switch back reopened the old epoch's rows:\n%s", seen)
	}
	if pub := readEverything(t, s, scoped("global", false)); strings.Contains(pub, "BACK-T4 secret") || !strings.Contains(pub, "BACK public") {
		t.Errorf("a public question after the confirmation:\n%s", pub)
	}
}

// S1: the hold survives a restart of the service, and a confirmation after
// it still releases the rows.
func TestAHeldEpochStaysHeldAcrossOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	for _, l := range oldMap {
		ingest(t, s, l)
	}
	noteMap(t, s, "M2", 900, 0)
	caughtUp(s)
	ingest(t, s, session(4_100_000, "M1").jsonl())
	ingest(t, s, `{"event":"console_chat","tick":4100100,"data":{"player":"Bob","force":"team-4","message":"BACK-T4 secret","scope":"team-4","private":true}}`)
	_ = s.Close()

	s = mustOpen(t, path)
	defer s.Close()
	if seen := readEverything(t, s, scoped("team-4", true)); strings.Contains(seen, "BACK") {
		t.Errorf("a held epoch read after Open:\n%s", seen)
	}
	noteMap(t, s, "M1", 4_100_300, 1)
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "BACK-T4 secret") {
		t.Errorf("the confirmation after Open did not release the held rows:\n%s", seen)
	}
}
