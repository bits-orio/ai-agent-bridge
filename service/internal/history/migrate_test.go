package history

// A history file from before rows carried a scope is migrated in place by
// Open, and every line Ingest stores carries one (visibility.go, migrate.go).

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// legacySchema is the events table exactly as every service before the
// scope columns created it.
const legacySchema = `CREATE TABLE events (
	id INTEGER PRIMARY KEY,
	tick INTEGER,
	event TEXT,
	player TEXT,
	force TEXT,
	data TEXT,
	received_at TEXT
)`

// legacyRow is one row as an old service stored it.
type legacyRow struct {
	tick                 int64
	event, player, force string
	data                 string
	wantScope            string
	wantPrivate          int
}

// legacyRows covers each classification rule, in file order. The question
// and answer pairs at the end reuse qid 7, the way a new save started on the
// same history file would, so an answer must take the question before it.
var legacyRows = []legacyRow{
	{1, "console_chat", "Bob", "team-4", `{"player":"Bob","force":"team-4","message":"team talk"}`, "team-4", 1},
	{2, "console_chat", "Server", "", `{"player":"Server","message":"from the console"}`, "global", 0},
	{3, "console_chat", "Server", "team-2", `{"player":"Server","force":"team-2","message":"a player named Server"}`, "team-2", 1},
	{4, "console_chat", "Ann", "team-1", `{"player":"Ann","force":"team-1","message":"new shape","scope":"team-1","private":true}`, "team-1", 1},
	{5, "console_chat", "Ann", "team-1", `{"player":"Ann","force":"team-1","message":"said in global","scope":"global","private":false}`, "global", 0},
	{6, "question", "Ann", "team-1", `{"qid":4,"player":"Ann","force":"team-1","text":"before scope providers"}`, "global", 0},
	{7, "question", "Ann", "team-1", `{"qid":5,"player":"Ann","force":"team-1","text":"private","scope":"team-1","private":true}`, "team-1", 1},
	{8, "question", "Ann", "team-1", `{"qid":6,"player":"Ann","force":"team-1","text":"public","scope":"global","private":false}`, "global", 0},
	{9, "answer", "", "", `{"qid":5,"shape":"summary"}`, "team-1", 1},
	{10, "answer", "", "", `{"qid":6,"shape":"summary"}`, "global", 0},
	{11, "answer", "", "", `{"qid":99,"shape":"notice"}`, "", 1},
	{12, "player_died", "Bob", "team-4", `{"player":"Bob","force":"team-4","cause":"biter"}`, "global", 0},
	{13, "research_finished", "", "team-4", `{"force":"team-4","tech":"automation","level":1}`, "global", 0},
	{14, "question", "Ann", "team-1", `{"qid":7,"player":"Ann","force":"team-1","text":"first save","scope":"global","private":false}`, "global", 0},
	{15, "answer", "", "", `{"qid":7,"shape":"summary"}`, "global", 0},
	{16, "question", "Ann", "team-3", `{"qid":7,"player":"Ann","force":"team-3","text":"second save","scope":"team-3","private":true}`, "team-3", 1},
	{17, "answer", "", "", `{"qid":7,"shape":"summary"}`, "team-3", 1},
}

// writeLegacyFile builds a pre-migration history file at path holding rows.
func writeLegacyFile(t *testing.T, path string, rows []legacyRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy file: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{legacySchema, schemaIndex} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema: %v", err)
		}
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO events (tick, event, player, force, data, received_at) VALUES (?, ?, ?, ?, ?, '')`,
			r.tick, r.event, r.player, r.force, r.data); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
}

// visibilityOf reads one row's two columns back, by tick (unique here).
func visibilityOf(t *testing.T, s *Store, tick int64) (string, int) {
	t.Helper()
	var scope sql.NullString
	var private sql.NullInt64
	if err := s.db.QueryRow("SELECT scope, private FROM events WHERE tick = ?", tick).Scan(&scope, &private); err != nil {
		t.Fatalf("row at tick %d: %v", tick, err)
	}
	if !private.Valid {
		t.Fatalf("row at tick %d was left unclassified", tick)
	}
	return scope.String, int(private.Int64)
}

func checkLegacyRows(t *testing.T, s *Store, rows []legacyRow) {
	t.Helper()
	for _, r := range rows {
		scope, private := visibilityOf(t, s, r.tick)
		if scope != r.wantScope || private != r.wantPrivate {
			t.Errorf("tick %d %s %s: scope %q private %d, want scope %q private %d",
				r.tick, r.event, r.data, scope, private, r.wantScope, r.wantPrivate)
		}
	}
}

// TestOpenMigratesAPreScopeFile: the columns are added, every old row is
// classified by the same rule Ingest uses, and opening again changes nothing.
func TestOpenMigratesAPreScopeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	writeLegacyFile(t, path, legacyRows)

	s := mustOpen(t, path)
	checkLegacyRows(t, s, legacyRows)
	if got := rowCount(t, s); got != len(legacyRows) {
		t.Errorf("row count after migration = %d, want %d: migration must never add or drop rows", got, len(legacyRows))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	again := mustOpen(t, path)
	defer again.Close()
	checkLegacyRows(t, again, legacyRows)
}

// TestMigratedLegacyChatStaysWithItsForce: after migration the tools read a
// legacy line back only for its own force's private scope.
func TestMigratedLegacyChatStaysWithItsForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	writeLegacyFile(t, path, legacyRows)
	s := mustOpen(t, path)
	defer s.Close()
	recentChat := findTool(t, s.Tools(), "recent_chat")

	for _, c := range []struct {
		key     string
		private bool
		want    bool
	}{{"global", false, false}, {"team-1", true, false}, {"team-4", true, true}} {
		out, err := recentChat.Call(scoped(c.key, c.private), nil)
		if err != nil {
			t.Fatalf("recent_chat: %v", err)
		}
		if got := strings.Contains(string(out), "team talk"); got != c.want {
			t.Errorf("scope %q private %v: legacy team-4 line shown = %v, want %v:\n%s", c.key, c.private, got, c.want, out)
		}
	}
}

// TestOpenClassifiesRowsAnOlderServiceAddedLater: a service older than the
// columns may still write to a migrated file, leaving its rows unclassified.
// The next Open classifies them, an answer among them inheriting from a
// question classified on an earlier Open.
func TestOpenClassifiesRowsAnOlderServiceAddedLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	ingest(t, s, `{"event":"question","tick":50,"data":{"qid":21,"player":"Ann","force":"team-6","text":"q","scope":"team-6","private":true}}`)
	// What an older service's INSERT leaves behind: no scope, no private.
	for _, stmt := range []string{
		`INSERT INTO events (tick, event, player, force, data, received_at) VALUES (51, 'answer', '', '', '{"qid":21,"shape":"summary"}', '')`,
		`INSERT INTO events (tick, event, player, force, data, received_at) VALUES (52, 'console_chat', 'Bob', 'team-6', '{"player":"Bob","force":"team-6","message":"m"}', '')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("old-service insert: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := mustOpen(t, path)
	defer reopened.Close()
	for tick, want := range map[int64]string{50: "team-6", 51: "team-6", 52: "team-6"} {
		if scope, private := visibilityOf(t, reopened, tick); scope != want || private != 1 {
			t.Errorf("tick %d: scope %q private %d, want %q private 1", tick, scope, private, want)
		}
	}
}

// TestIngestRecordsWhoMayReadEachLine covers the scoped chat shape, the
// legacy one, the question and answer rules and every other event.
func TestIngestRecordsWhoMayReadEachLine(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	for _, c := range []struct {
		line        string
		wantScope   string
		wantPrivate int
	}{
		{`{"event":"console_chat","tick":1,"data":{"player":"Bob","force":"team-4","message":"m","scope":"team-4","private":true}}`, "team-4", 1},
		{`{"event":"console_chat","tick":2,"data":{"player":"Bob","force":"team-4","message":"m","scope":"global","private":false}}`, "global", 0},
		{`{"event":"console_chat","tick":3,"data":{"player":"Bob","force":"team-4","message":"m","private":false}}`, "global", 0},
		{`{"event":"console_chat","tick":4,"data":{"player":"Bob","force":"team-4","message":"m","private" : false}}`, "global", 0},
		{`{"event":"console_chat","tick":5,"data":{"player":"Bob","force":"team-4","message":"m","private":true}}`, "", 1},
		{`{"event":"console_chat","tick":6,"data":{"player":"Bob","force":"team-4","message":"m","private":"no","scope":"team-4"}}`, "team-4", 1},
		{`{"event":"console_chat","tick":7,"data":{"player":"Bob","force":"team-4","message":"m","private":false,"scope":7}}`, "global", 0},
		{`{"event":"console_chat","tick":8,"data":{"player":"Bob","force":"team-4","message":"legacy"}}`, "team-4", 1},
		{`{"event":"console_chat","tick":9,"data":{"player":"Server","message":"console"}}`, "global", 0},
		{`{"event":"console_chat","tick":10,"data":{"player":"Server","message":"console","scope":"global","private":false}}`, "global", 0},
		{`{"event":"question","tick":11,"data":{"qid":1,"player":"Bob","force":"team-4","text":"q","scope":"team-4","private":true}}`, "team-4", 1},
		{`{"event":"question","tick":12,"data":{"qid":2,"player":"Bob","force":"team-4","text":"q","scope":"global","private":false}}`, "global", 0},
		{`{"event":"question","tick":13,"data":{"qid":3,"player":"Bob","force":"team-4","text":"q"}}`, "global", 0},
		{`{"event":"answer","tick":14,"data":{"qid":1,"shape":"summary"}}`, "team-4", 1},
		{`{"event":"answer","tick":15,"data":{"qid":2,"shape":"summary"}}`, "global", 0},
		{`{"event":"answer","tick":16,"data":{"qid":404,"shape":"summary"}}`, "", 1},
		{`{"event":"answer","tick":17,"data":{"shape":"summary"}}`, "", 1},
		{`{"event":"player_died","tick":18,"data":{"player":"Bob","force":"team-4","cause":"biter"}}`, "global", 0},
		{`{"event":"future_event","tick":19,"data":{"player":"Bob","force":"team-4","private":true,"scope":"team-4"}}`, "team-4", 1},
		{`{"event":"answer","tick":20,"data":{"qid":2,"shape":"summary","private":true,"scope":"team-4"}}`, "team-4", 1},
		{`{"event":"player_joined","tick":21,"data":{"player":"Bob","force":"team-4"}}`, "global", 0},
		{`{"event":"rocket_launched","tick":22}`, "global", 0},
	} {
		ingest(t, s, c.line)
		var scope sql.NullString
		var private sql.NullInt64
		if err := s.db.QueryRow("SELECT scope, private FROM events ORDER BY id DESC LIMIT 1").Scan(&scope, &private); err != nil {
			t.Fatalf("newest row: %v", err)
		}
		if !private.Valid || scope.String != c.wantScope || int(private.Int64) != c.wantPrivate {
			t.Errorf("%s: scope %q private %v, want scope %q private %d", c.line, scope.String, private, c.wantScope, c.wantPrivate)
		}
	}
}
