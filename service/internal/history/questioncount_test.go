package history

// How many questions a session row says the companion has issued (its
// last_id) against the highest question id the epoch has logged: a count
// that went back is no continuation of the map (epoch.go, fewerQuestions).
// And a status reply judged against history as it stood before the status
// call went out (Store.Mark), never against a line the tailer stored while
// the call was out.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// question is a question row with the given qid.
func question(tick, qid int64) line {
	return line{questionEvent, tick, fmt.Sprintf(`{"qid":%d,"player":"Bob","force":"team-4","text":"q","scope":"team-4","private":true}`, qid)}
}

// statusAt is the session row NoteMap records, with the companion's count.
func statusAt(tick int64, mapID string, lastID int64) line {
	return line{sessionEvent, tick, fmt.Sprintf(`{"map_id":%q,"source":%q,"last_id":%d}`, mapID, statusSource, lastID)}
}

// sessionAt is a session line from a companion that writes its count.
func sessionAt(tick int64, mapID string, lastID int64) line {
	return line{sessionEvent, tick, fmt.Sprintf(`{"map_id":%q,"last_id":%d}`, mapID, lastID)}
}

// The rule, row by row. Every case is past a map's first five minutes, so
// the tick alone would keep the epoch.
func TestEpochRuleHoldsASessionRowToTheQuestionsLogged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []line
		want  []int64
	}{
		{"a status counting fewer questions than the map logged is another map",
			[]line{session(20_000, "A"), question(30_000, 3), statusAt(40_000, "A", 0), ping(40_100)}, []int64{1, 1, 2, 2}},
		{"a status counting every logged question goes on with the map",
			[]line{session(20_000, "A"), question(30_000, 3), statusAt(40_000, "A", 3), statusAt(50_000, "A", 9)}, []int64{1, 1, 1, 1}},
		{"a session line counting fewer questions is another map",
			[]line{session(20_000, "A"), question(30_000, 3), sessionAt(40_000, "A", 2), ping(40_100)}, []int64{1, 1, 2, 2}},
		{"a session line with no count is judged as before",
			[]line{session(20_000, "A"), question(30_000, 3), session(40_000, "A")}, []int64{1, 1, 1}},
		{"an older companion's epoch is held to its questions too",
			[]line{ping(20_000), question(30_000, 4), statusAt(40_000, "A", 1)}, []int64{1, 1, 2}},
		{"a map upgraded in place keeps its count and its epoch",
			[]line{ping(20_000), question(30_000, 4), statusAt(40_000, "A", 4), ping(40_100)}, []int64{1, 1, 1, 1}},
		{"each epoch counts its own questions",
			[]line{session(9_000_000, "A"), question(9_000_100, 9), session(20_000, "B"), question(20_100, 1), statusAt(40_000, "B", 1)},
			[]int64{1, 1, 2, 2, 2}},
		{"a question row starting an epoch counts in it",
			[]line{session(9_000_000, "A"), question(9_000_100, 9), question(100, 2), statusAt(40_000, "A", 1)},
			[]int64{1, 1, 2, 3}},
		{"a qid that is not an integer counts for nothing",
			[]line{session(20_000, "A"), {questionEvent, 30_000, `{"qid":"7"}`}, {questionEvent, 30_100, `{"qid":7.5}`}, statusAt(40_000, "A", 0)},
			[]int64{1, 1, 1, 1}},
		{"a last_id that is not an integer says nothing",
			[]line{session(20_000, "A"), question(30_000, 3), {sessionEvent, 40_000, `{"map_id":"A","last_id":"0"}`}}, []int64{1, 1, 1}},
		{"only a session row carries a count",
			[]line{session(20_000, "A"), question(30_000, 3), {"ping", 40_000, `{"last_id":0}`}}, []int64{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := epochsOf(tc.lines); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("epochs = %v, want %v", got, tc.want)
			}
		})
	}
}

// roundOne is the first round of a server that resets every round from one
// fixed seed: its map id, team-4's private chat and a question, all past the
// fifth minute.
func roundOne(t *testing.T) *Store {
	t.Helper()
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = s.Close() })
	for _, l := range []string{
		session(1_000, "seed-0-777").jsonl(),
		`{"event":"console_chat","tick":72000,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 secret","scope":"team-4","private":true}}`,
		question(72_100, 4).jsonl(),
	} {
		ingest(t, s, l)
	}
	return s
}

const roundTwoLine = `{"event":"console_chat","tick":40100,"data":{"player":"Cid","force":"team-4","message":"NEW-T4 line","scope":"team-4","private":true}}`

// The review's case: round two reuses round one's map id, and the service
// first sees it at minute eleven, past the five-minute rule. Round two's
// companion has issued no question yet, fewer than round one logged, so round
// two starts its own epoch whether the status or the session line says so
// first, and its team-4 never reads round one's team-4 chat.
func TestASameSeedMapFirstSeenLateStartsItsOwnEpoch(t *testing.T) {
	for _, via := range []string{"status", "session line"} {
		t.Run(via, func(t *testing.T) {
			s := roundOne(t)
			if via == "status" {
				if !noteMap(t, s, "seed-0-777", 40_000, 0) {
					t.Error("NoteMap on round two said no read moved")
				}
			} else {
				ingest(t, s, sessionAt(40_000, "seed-0-777", 0).jsonl())
			}
			ingest(t, s, roundTwoLine)
			seen := readEverything(t, s, scoped("team-4", true))
			if strings.Contains(seen, "OLD-T4") {
				t.Errorf("round two's team-4 reads round one's private chat:\n%s", seen)
			}
			if !strings.Contains(seen, "NEW-T4 line") {
				t.Errorf("round two's team-4 lost its own chat:\n%s", seen)
			}
		})
	}
}

// The same map going on, a restart from its newest save, counts every
// question it logged and keeps its rows. A rollback to a save from before one
// of them does not: fail closed, the rows before it go unread.
func TestTheQuestionCountKeepsTheSameMapAndFailsClosedOnARollback(t *testing.T) {
	s := roundOne(t)
	if noteMap(t, s, "seed-0-777", 72_500, 4) {
		t.Error("a status counting every logged question moved reads")
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "OLD-T4 secret") {
		t.Errorf("the same map going on lost its rows:\n%s", seen)
	}
	if !noteMap(t, s, "seed-0-777", 70_000, 3) {
		t.Error("a rollback to before question 4 kept the epoch")
	}
	if seen := readEverything(t, s, scoped("team-4", true)); strings.Contains(seen, "OLD-T4") {
		t.Errorf("after a rollback that lost a question, the rows before it are still read:\n%s", seen)
	}
}

// A question asked after the status was read, stored while the call was
// out, is not one the status could count: the status is held to the
// questions logged before the call went out.
func TestAQuestionStoredWhileTheStatusCallWasOutCountsForNothing(t *testing.T) {
	s := roundOne(t)
	before := s.Mark()
	ingest(t, s, question(72_200, 5).jsonl())
	rows := rowCount(t, s)
	moved, err := s.NoteMap(before, "seed-0-777", 72_150, 4)
	if err != nil {
		t.Fatalf("NoteMap: %v", err)
	}
	if moved || rowCount(t, s) != rows {
		t.Errorf("a status naming the epoch's own map moved = %v and recorded %d row(s): want neither", moved, rowCount(t, s)-rows)
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "OLD-T4 secret") {
		t.Errorf("the map lost its own rows:\n%s", seen)
	}
	// History still holds question 5 against the next status: the stale one
	// above did not wind the count back to the mark's.
	if !noteMap(t, s, "seed-0-777", 72_400, 4) {
		t.Error("a later status counting 4 questions, after question 5 was logged, kept the epoch")
	}
}

// The review's case: in a map's first five minutes, the tailer stores a line
// at tick 5002 while the status call, answered at tick 5000, is out. Judged
// against that line the status reads as a drop into a new map. Judged
// against the file as it stood before the call, it is the same map.
func TestAStatusIsJudgedAgainstTheFileAsItStoodBeforeTheCall(t *testing.T) {
	const early = `{"event":"console_chat","tick":5002,"data":{"player":"Bob","force":"team-4","message":"T4 early secret","scope":"team-4","private":true}}`
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingest(t, s, session(1000, "A").jsonl())
	before := s.Mark()
	ingest(t, s, early)
	moved, err := s.NoteMap(before, "A", 5000, 0)
	if err != nil {
		t.Fatalf("NoteMap: %v", err)
	}
	if moved {
		t.Error("a status a few ticks behind a line stored while the call was out started an epoch")
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "T4 early secret") {
		t.Errorf("the map's own line went unread:\n%s", seen)
	}

	// The mark is what makes the difference: taken after that line, the
	// same status is a drop into a young map's first minutes.
	late := mustOpen(t, filepath.Join(t.TempDir(), "late.sqlite"))
	defer late.Close()
	ingest(t, late, session(1000, "A").jsonl())
	ingest(t, late, early)
	if moved, err := late.NoteMap(late.Mark(), "A", 5000, 0); err != nil || !moved {
		t.Errorf("judged against the line after it, the status moved = %v (%v), want true", moved, err)
	}
}

// When the file moves to another epoch while the call is out, it has said
// which map the game is on more recently than the status could: nothing is
// recorded, and nothing moves back.
func TestAStatusRecordsNothingWhenTheFileMovedOnMeanwhile(t *testing.T) {
	s := roundOne(t)
	before := s.Mark()
	ingest(t, s, session(50, "B").jsonl())
	rows := rowCount(t, s)
	moved, err := s.NoteMap(before, "seed-0-777", 72_300, 4)
	if err != nil {
		t.Fatalf("NoteMap: %v", err)
	}
	if moved || rowCount(t, s) != rows {
		t.Errorf("moved = %v, rows %d, was %d: a status older than the file's own epoch was recorded", moved, rowCount(t, s), rows)
	}
	if got := epochAt(t, s, 50); got != 2 {
		t.Fatalf("map B's session line is in epoch %d, want 2", got)
	}
	ingest(t, s, ping(60).jsonl())
	if got := epochAt(t, s, 60); got != 2 {
		t.Errorf("the line after it is in epoch %d, want 2, map B's", got)
	}
}

// Open reads the epoch's highest question id back, and so does the
// migration for rows an older service stored: both hold a session row to
// it the way Ingest does.
func TestOpenAndTheMigrationKeepTheQuestionCount(t *testing.T) {
	t.Run("reopened", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "history.sqlite")
		s := mustOpen(t, path)
		// A qid that is not an integer counts for nothing on a reopen either.
		ingestLines(t, s, session(20_000, "A"), question(30_000, 6), line{questionEvent, 30_100, `{"qid":"9"}`}, line{questionEvent, 30_200, `{"qid":9.5}`})
		if err := s.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		for lastID, wantMoved := range map[int64]bool{6: false, 5: true} {
			copyPath := filepath.Join(t.TempDir(), "copy.sqlite")
			copyFile(t, path, copyPath)
			again := mustOpen(t, copyPath)
			if moved := noteMap(t, again, "A", 40_000, lastID); moved != wantMoved {
				t.Errorf("after a reopen, a status counting %d moved = %v, want %v", lastID, moved, wantMoved)
			}
			_ = again.Close()
		}
	})
	// Rows an older service stored without an epoch, between rows this one
	// placed: the count runs through both, as it does through Ingest.
	t.Run("placed by the migration", func(t *testing.T) {
		lines := []line{session(20_000, "A"), question(30_000, 6), ping(35_000), sessionAt(40_000, "A", 2), ping(40_100)}
		path := filepath.Join(t.TempDir(), "history.sqlite")
		s := mustOpen(t, path)
		ingestLines(t, s, lines[0])
		insertUnplaced(t, s, lines[1])
		ingestLines(t, s, lines[2])
		insertUnplaced(t, s, lines[3:]...)
		if err := s.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		migrated := mustOpen(t, path)
		defer migrated.Close()
		want := epochsOf(lines)
		if want[3] != 2 {
			t.Fatalf("the fixture no longer starts an epoch: %v", want)
		}
		for i, l := range lines {
			if got := epochAt(t, migrated, l.tick); got != want[i] {
				t.Errorf("row at tick %d: the migration placed it in epoch %d, Ingest in %d", l.tick, got, want[i])
			}
		}
	})
}

// The count a status row carries counts questions on every channel, the way
// a qid does: it stays in the file and out of every reply.
func TestNoReplyCarriesTheQuestionCount(t *testing.T) {
	s := roundOne(t)
	noteMap(t, s, "B", 900, 4217)
	for _, a := range audiences() {
		seen := readEverything(t, s, a.ctx)
		if strings.Contains(seen, "last_id") || strings.Contains(seen, "4217") {
			t.Errorf("%s read the question count:\n%s", a.name, seen)
		}
		if !strings.Contains(seen, `"source":"status"`) {
			t.Errorf("%s no longer reads the status row at all:\n%s", a.name, seen)
		}
	}
	var kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE json_extract(data, '$.last_id') = 4217`).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("the file keeps %d counts (%v), want the status row's", kept, err)
	}
}

// The epoch can gain a name while the status call is out: an older
// companion's unnamed epoch, named by a session line the tailer stored
// meanwhile. The status is held to that name, so another map's status starts
// an epoch rather than renaming this one.
func TestAStatusIsHeldToAMapNamedWhileTheCallWasOut(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingestLines(t, s, ping(20_000))
	before := s.Mark()
	ingestLines(t, s, session(20_100, "A"))
	moved, err := s.NoteMap(before, "B", 20_050, 0)
	if err != nil {
		t.Fatalf("NoteMap: %v", err)
	}
	if !moved {
		t.Error("a status naming map B on an epoch the file had just named A moved nothing")
	}
	ingestLines(t, s, session(20_200, "A"))
	if got := epochAt(t, s, 20_200); got != 3 {
		t.Errorf("map A's next session line is in epoch %d, want 3: B's status renamed A's epoch", got)
	}
}
