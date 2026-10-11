package history

// Map epochs (epoch.go): which rows belong to one map, and that no read ever
// reaches past the current one.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
)

// line is one row of an epoch fixture.
type line struct {
	event string
	tick  int64
	data  string
}

func (l line) jsonl() string {
	data := l.data
	if data == "" {
		data = "{}"
	}
	return fmt.Sprintf(`{"event":%q,"tick":%d,"data":%s}`, l.event, l.tick, data)
}

func session(tick int64, mapID string) line {
	return line{sessionEvent, tick, fmt.Sprintf(`{"map_id":%q}`, mapID)}
}

func ping(tick int64) line { return line{"ping", tick, ""} }

// status is the session row the service records from a status reply
// (Store.NoteMap).
func status(tick int64, mapID string) line {
	return line{sessionEvent, tick, fmt.Sprintf(`{"map_id":%q,"source":%q}`, mapID, statusSource)}
}

// epochsOf runs the rule over lines from an empty store and returns each
// line's epoch.
func epochsOf(lines []line) []int64 {
	var s epochState
	out := make([]int64, len(lines))
	for i, l := range lines {
		s = s.next(l.event, l.tick, json.RawMessage(l.data))
		out[i] = s.epoch
	}
	return out
}

// The rule itself, line by line.
func TestEpochRule(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []line
		want  []int64
	}{
		{"ticks going up stay in one epoch", []line{ping(10), ping(20), ping(5_000_000)}, []int64{1, 1, 1}},
		{"a drop into a new map's first five minutes is a new map",
			[]line{ping(50_000), ping(newMapTicks - 1)}, []int64{1, 2}},
		{"a drop to five minutes or later, by thirty minutes or less, is a rollback",
			[]line{ping(newMapTicks + rollbackTicks), ping(newMapTicks)}, []int64{1, 1}},
		{"a drop of more than thirty minutes is a new map",
			[]line{ping(1_000_000), ping(1_000_000 - rollbackTicks - 1)}, []int64{1, 2}},
		{"a drop of exactly thirty minutes is a rollback",
			[]line{ping(1_000_000), ping(1_000_000 - rollbackTicks)}, []int64{1, 1}},
		{"an equal tick is no drop", []line{ping(500), ping(500)}, []int64{1, 1}},
		{"the same map keeps its epoch through a drop however far, to its fifth minute or later",
			[]line{session(9_000_000, "A"), ping(9_000_100), session(newMapTicks, "A"), ping(newMapTicks + 20)}, []int64{1, 1, 1, 1}},
		{"the same map dropping into its first five minutes is a new map made from the same seed",
			[]line{session(9_000_000, "A"), ping(9_000_100), session(newMapTicks-1, "A"), ping(newMapTicks)}, []int64{1, 1, 2, 2}},
		{"a young map loaded back into its first five minutes reads as a new map too",
			[]line{session(10_000, "A"), ping(17_000), session(5_000, "A")}, []int64{1, 1, 2}},
		{"the same map keeps its epoch in its first five minutes while the tick goes on",
			[]line{session(100, "A"), ping(200), session(300, "A"), ping(400)}, []int64{1, 1, 1, 1}},
		{"another map starts an epoch even with no drop",
			[]line{session(100, "A"), ping(200), session(300, "B"), ping(400)}, []int64{1, 1, 2, 2}},
		{"a session on an older companion's epoch keeps it when the tick goes on",
			[]line{ping(1_000_000), session(1_000_100, "A"), ping(1_000_200), session(newMapTicks, "A")}, []int64{1, 1, 1, 1}},
		{"a session on an older companion's epoch after a new map's drop starts one",
			[]line{ping(1_000_000), session(100, "A"), ping(200)}, []int64{1, 2, 2}},
		{"a plain line that drops without a session line still starts an epoch",
			[]line{session(100, "A"), ping(1_000_000), ping(100), session(200, "A")}, []int64{1, 1, 2, 2}},
		{"after a new epoch by tick the old map's name is gone",
			[]line{session(100, "A"), ping(1_000_000), ping(100), session(150, "B"), session(160, "A")}, []int64{1, 1, 2, 2, 3}},
		{"a session line with no usable map_id is judged by its tick",
			[]line{session(100, "A"), ping(1_000_000), {sessionEvent, 10, `{"map_id":""}`}, {sessionEvent, 20, `{"map_id":true}`}, {sessionEvent, 30, `{}`}},
			[]int64{1, 1, 2, 2, 2}},
		{"a numeric map_id names a map",
			[]line{{sessionEvent, 100, `{"map_id":12345}`}, ping(5_000_000), {sessionEvent, 50_000, `{"map_id":12345}`}, {sessionEvent, 50_001, `{"map_id":12346}`}},
			[]int64{1, 1, 1, 2}},
		{"only a session line names a map",
			[]line{{"ping", 100, `{"map_id":"A"}`}, ping(5_000_000), session(10, "A")}, []int64{1, 1, 2}},
		{"a status row naming another map starts an epoch, and the session line it got ahead of joins it",
			[]line{session(9_000_000, "A"), ping(9_000_100), status(900, "B"), session(300, "B"), ping(400)}, []int64{1, 1, 2, 2, 2}},
		{"the lines a status row got ahead of join its epoch, written before it at lower ticks",
			[]line{session(9_000_000, "A"), status(900, "B"), ping(800), ping(850)}, []int64{1, 2, 2, 2}},
		{"a status row on the same map dropping into its first five minutes starts an epoch",
			[]line{session(9_000_000, "A"), ping(9_000_100), status(600, "A"), session(300, "A"), ping(400)}, []int64{1, 1, 2, 2, 2}},
		{"a status row is judged by the tick rule on an older companion's epoch",
			[]line{ping(1_000_000), status(600, "B"), ping(700)}, []int64{1, 2, 2}},
		{"a status row naming an older companion's epoch never moves its tick up",
			[]line{ping(10_000), status(12_000, "A"), ping(11_000)}, []int64{1, 1, 1}},
		{"a status row naming an older companion's epoch keeps the tick a drop is measured from",
			[]line{ping(10_000), status(12_000, "A"), ping(5_000)}, []int64{1, 1, 2}},
		{"a session row from any source but the status is a line of the file",
			[]line{session(9_000_000, "A"), {sessionEvent, 900, `{"map_id":"B","source":"file"}`}, session(300, "B")}, []int64{1, 2, 3}},
		{"only a session row can be a status row",
			[]line{ping(10_000), {"ping", 12_000, `{"source":"status"}`}, ping(11_000)}, []int64{1, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := epochsOf(tc.lines); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("epochs = %v, want %v", got, tc.want)
			}
		})
	}
}

// ingestLines stores every line, in order.
func ingestLines(t *testing.T, s *Store, lines ...line) {
	t.Helper()
	for _, l := range lines {
		ingest(t, s, l.jsonl())
	}
}

// epochAt is the epoch of the row at tick, which the fixture keeps unique.
func epochAt(t *testing.T, s *Store, tick int64) int64 {
	t.Helper()
	var epoch sql.NullInt64
	if err := s.db.QueryRow("SELECT epoch FROM events WHERE tick = ?", tick).Scan(&epoch); err != nil {
		t.Fatalf("row at tick %d: %v", tick, err)
	}
	if !epoch.Valid {
		t.Fatalf("row at tick %d has no epoch", tick)
	}
	return epoch.Int64
}

// twoMaps is an older map with far larger ticks, then a new map from its
// first minutes, the way one history file sees a server restart on a new
// map. Every name repeats, as it does on a server whose team slots are
// recycled: team-4, Bob and qid 1 mean someone else on the new map.
var twoMaps = []string{
	`{"event":"player_joined","tick":4000000,"data":{"player":"Bob","force":"team-4"}}`,
	`{"event":"console_chat","tick":4000100,"data":{"player":"Alice","force":"team-4","message":"OLD public hello","scope":"global","private":false}}`,
	`{"event":"console_chat","tick":4000200,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 secret","scope":"team-4","private":true}}`,
	`{"event":"console_chat","tick":4000300,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 legacy"}}`,
	`{"event":"question","tick":4000400,"data":{"qid":1,"player":"Bob","force":"team-4","text":"OLD-T4 question","scope":"team-4","private":true}}`,
	`{"event":"answer","tick":4000500,"data":{"qid":1,"shape":"summary"}}`,
	`{"event":"player_died","tick":4000600,"data":{"player":"Ghost","force":"team-4","cause":"biter"}}`,
	`{"event":"player_left","tick":9000000,"data":{"player":"Carl","force":"team-9"}}`,
	`{"event":"research_finished","tick":9000100,"data":{"force":"team-4","tech":"OLD-tech","level":1}}`,
	`{"event":"player_joined","tick":600,"data":{"player":"Bob","force":"team-4"}}`,
	`{"event":"console_chat","tick":700,"data":{"player":"Alice","force":"team-4","message":"NEW public hello","scope":"global","private":false}}`,
	`{"event":"console_chat","tick":800,"data":{"player":"Bob","force":"team-4","message":"NEW-T4 secret","scope":"team-4","private":true}}`,
	`{"event":"answer","tick":900,"data":{"qid":1,"shape":"summary"}}`,
	`{"event":"question","tick":1000,"data":{"qid":1,"player":"Cid","force":"team-9","text":"NEW-T9 question","scope":"team-9","private":true}}`,
	`{"event":"research_finished","tick":1100,"data":{"force":"team-4","tech":"NEW-tech","level":1}}`,
	`{"event":"player_joined","tick":1200,"data":{"player":"Carl","force":"team-9"}}`,
}

func twoMapStore(t *testing.T) *Store {
	t.Helper()
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = s.Close() })
	for _, l := range twoMaps {
		ingest(t, s, l)
	}
	return s
}

// No read reaches the older map, whoever asks and however: not its public
// rows, not a private channel's rows under the same key, not a player who
// only appears there.
func TestNoReadReachesAnEarlierMap(t *testing.T) {
	s := twoMapStore(t)
	if got := epochAt(t, s, 600); got != 2 {
		t.Fatalf("the new map's first row is in epoch %d, want 2", got)
	}
	for _, a := range []struct {
		name string
		ctx  context.Context
		sees []string
	}{
		{"no scope", context.Background(), []string{"NEW public hello", "NEW-tech"}},
		{"global", scoped(chatscope.GlobalKey, false), []string{"NEW public hello", "NEW-tech"}},
		{"team-4 private", scoped("team-4", true), []string{"NEW-T4 secret", "NEW public hello"}},
		{"team-9 private", scoped("team-9", true), []string{"NEW-T9 question"}},
	} {
		t.Run(a.name, func(t *testing.T) {
			seen := readEverything(t, s, a.ctx)
			// Every row of the old map carries OLD or a tick from 4000000 up.
			if strings.Contains(seen, "OLD") || strings.Contains(seen, "4000") || strings.Contains(seen, "9000") {
				t.Errorf("%s read the earlier map:\n%s", a.name, seen)
			}
			for _, want := range a.sees {
				if !strings.Contains(seen, want) {
					t.Errorf("%s never read %q:\n%s", a.name, want, seen)
				}
			}
		})
	}
}

// The counts, the player lookup and the window anchors are reads too.
func TestEveryHistoryReadStaysInTheCurrentEpoch(t *testing.T) {
	s := twoMapStore(t)
	ts := s.Tools()
	team4 := scoped("team-4", true)
	call := func(ctx context.Context, name, args string) string {
		t.Helper()
		out, err := findTool(t, ts, name).Call(ctx, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s %s: %v", name, args, err)
		}
		return string(out)
	}
	count := func(ctx context.Context, event string) int64 {
		_, rows := splitColumnar(t, json.RawMessage(call(ctx, "count_events", `{"event":"`+event+`"}`)))
		n, _ := strconv.ParseInt(rows[0], 10, 64)
		return n
	}
	for _, c := range []struct {
		ctx          context.Context
		event        string
		want         int64
		whyIfWrong   string
		scopeOfCount string
	}{
		{team4, "console_chat", 2, "the old map's three team-4 lines were counted", "team-4"},
		{context.Background(), "research_finished", 1, "the old map's research was counted", "public"},
		{context.Background(), "player_joined", 2, "the old map's join was counted", "public"},
	} {
		if got := count(c.ctx, c.event); got != c.want {
			t.Errorf("count_events(%s) for %s = %d, want %d: %s", c.event, c.scopeOfCount, got, c.want, c.whyIfWrong)
		}
	}

	// Ghost died on the old map only: on this one there is no record.
	if out := call(context.Background(), "catch_up", `{"player":"Ghost"}`); !strings.Contains(out, "no record of this player") {
		t.Errorf("catch_up for a player only the old map knew = %q, want no record", out)
	}
	// Carl left the old map at tick 9000000. Read across maps, that would
	// start his window far past everything on this map.
	if out := call(context.Background(), "catch_up", `{"player":"Carl"}`); !strings.Contains(out, "NEW-tech") || strings.Contains(out, "OLD") {
		t.Errorf("catch_up for Carl = %q, want this map's research and nothing older", out)
	}
	// "Now" is this map's newest tick, not the old map's 9000100: from
	// there a since_tick window would start after every row this map has.
	if out := call(context.Background(), "catch_up", `{"since_tick":1}`); !strings.Contains(out, "NEW-tech") {
		t.Errorf("catch_up since tick 1 = %q, want this map's research", out)
	}
	if got := call(context.Background(), "last_event", `{"event":"player_left"}`); got != "none recorded" {
		t.Errorf("last_event(player_left) = %q, want none recorded: the only one is the old map's", got)
	}
}

// Question ids start again on every map. An answer whose qid only an earlier
// map's question carries is unreadable, not that question's private row.
func TestAnAnswerWithNoQuestionInItsEpochIsUnreadable(t *testing.T) {
	s := twoMapStore(t)
	var scope sql.NullString
	var private sql.NullInt64
	if err := s.db.QueryRow("SELECT scope, private FROM events WHERE tick = 900").Scan(&scope, &private); err != nil {
		t.Fatalf("answer row: %v", err)
	}
	if scope.String != "" || private.Int64 != 1 {
		t.Errorf("the new map's answer to qid 1 = scope %q private %d, want private under the empty key", scope.String, private.Int64)
	}
	for _, ctx := range []context.Context{context.Background(), scoped("team-4", true), scoped("team-9", true)} {
		out, err := findTool(t, s.Tools(), "recent_events").Call(ctx, json.RawMessage(`{"event":"answer"}`))
		if err != nil {
			t.Fatalf("recent_events: %v", err)
		}
		if _, rows := splitColumnar(t, out); len(rows) != 0 {
			t.Errorf("scope %+v read an answer no question of this map owns: %v", chatscope.From(ctx), rows)
		}
	}
}

// A session line names its map. Another map starts an epoch with no tick
// drop at all, and the same map keeps its epoch through a reload far back.
func TestSessionLinesNameTheMap(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	chat := func(tick int64, msg string) line {
		return line{"console_chat", tick, fmt.Sprintf(`{"player":"Ann","force":"player","message":%q,"scope":"global","private":false}`, msg)}
	}
	ingestLines(t, s,
		session(100, "map-A"), chat(200, "A-line"),
		session(300, "map-B"), chat(2_000_000, "B-line"),
		session(500_000, "map-B"), chat(500_100, "B-after-reload"),
	)
	out := string(callTool(t, findTool(t, s.Tools(), "recent_chat"), map[string]any{"limit": 20}))
	for want, shown := range map[string]bool{"A-line": false, "B-line": true, "B-after-reload": true} {
		if strings.Contains(out, want) != shown {
			t.Errorf("recent_chat shows %q = %v, want %v:\n%s", want, !shown, shown, out)
		}
	}
	if got := epochAt(t, s, 500_100); got != 2 {
		t.Errorf("after map-B reloaded far back the epoch is %d, want 2", got)
	}
}

// Two maps made from one fixed seed carry the same map id. The second one's
// session line comes in its first five minutes, below the first map's
// ticks, and starts its own epoch: the first map's private chat, under the
// same force and the same key, never reaches the second map's questions.
func TestASameSeedMapStartsItsOwnEpoch(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingest(t, s, session(4_000_000, "seed-0-777").jsonl())
	for _, l := range twoMaps {
		if strings.Contains(l, `"tick":600,`) {
			ingest(t, s, session(590, "seed-0-777").jsonl())
		}
		ingest(t, s, l)
	}
	if got := epochAt(t, s, 590); got != 2 {
		t.Fatalf("the second map's session line is in epoch %d, want 2", got)
	}
	for _, a := range []context.Context{context.Background(), scoped("team-4", true), scoped("team-9", true)} {
		if seen := readEverything(t, s, a); strings.Contains(seen, "OLD") {
			t.Errorf("scope %+v read the first map made from the same seed:\n%s", chatscope.From(a), seen)
		}
	}
	if seen := readEverything(t, s, scoped("team-4", true)); !strings.Contains(seen, "NEW-T4 secret") {
		t.Errorf("team-4 lost its own chat on the second map:\n%s", seen)
	}
}

// An autosave loaded after a crash goes back a few minutes on the same map:
// what was logged before it stays readable.
func TestARollbackStaysInItsEpoch(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingest(t, s, `{"event":"research_finished","tick":1000000,"data":{"force":"player","tech":"before-the-crash","level":1}}`)
	ingest(t, s, `{"event":"research_finished","tick":990000,"data":{"force":"player","tech":"after-the-reload","level":1}}`)
	out := string(callTool(t, findTool(t, s.Tools(), "recent_events"), map[string]any{"event": "research_finished"}))
	if !strings.Contains(out, "before-the-crash") || !strings.Contains(out, "after-the-reload") {
		t.Errorf("a rollback lost the rows before it:\n%s", out)
	}
}

// Open picks up where the file left off: the epoch, the tick and the map of
// the newest row. A store that forgot them would start a reopened file's
// next line in epoch 1 again, the first map's, and read that map back.
func TestOpenResumesTheCurrentEpoch(t *testing.T) {
	chat := func(tick int64, msg string) line {
		return line{"console_chat", tick, fmt.Sprintf(`{"player":"Ann","force":"player","message":%q,"scope":"global","private":false}`, msg)}
	}
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	ingestLines(t, s, chat(3_000_000, "first map"), session(100, "M2"), chat(2_000_000, "second map"))
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	recent := func(s *Store) string {
		out := callTool(t, findTool(t, s.Tools(), "recent_chat"), map[string]any{"limit": 20})
		_, rows := splitColumnar(t, out)
		var msgs []string
		for _, row := range rows {
			msgs = append(msgs, splitRow(row)[4])
		}
		return strings.Join(msgs, "|")
	}
	// The same map reloaded far back: only the map remembered keeps it.
	reopened := mustOpen(t, path)
	ingestLines(t, reopened, session(1_000_000, "M2"), chat(1_000_100, "second map, reloaded"))
	if got, want := recent(reopened), "second map, reloaded|second map"; got != want {
		t.Errorf("after reopening, the same map reloaded reads %q, want %q", got, want)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A plain line dropping into a new map's first minutes: only the tick
	// remembered sees the drop.
	again := mustOpen(t, path)
	defer again.Close()
	ingestLines(t, again, chat(250, "third map"))
	if got, want := recent(again), "third map"; got != want {
		t.Errorf("after reopening, a new map reads %q, want %q", got, want)
	}
}

// legacyLines is a pre-scope file spanning three maps: a new map's first
// minutes, a drop of more than thirty minutes, and a rollback that stays.
var legacyLines = []struct {
	tick  int64
	event string
	force string
	data  string
	epoch int64
}{
	{100, "ping", "", `{}`, 1},
	{200, "ping", "", `{}`, 1},
	{5_000_000, "ping", "", `{}`, 1},
	{300, "ping", "", `{}`, 2},
	{9_000_000, "ping", "", `{}`, 2},
	{8_000_000, "ping", "", `{}`, 3},
	{8_050_000, "ping", "", `{}`, 3},
	{8_000_500, "ping", "", `{}`, 3},
}

// The migration places every old row in id order by the rule Ingest uses,
// places nothing again on a second Open, and agrees with Ingest row for row.
func TestOpenPlacesOldRowsInTheirEpochs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	var rows []legacyRow
	for _, l := range legacyLines {
		rows = append(rows, legacyRow{tick: l.tick, event: l.event, force: l.force, data: l.data})
	}
	writeLegacyFile(t, path, rows)

	s := mustOpen(t, path)
	check := func(s *Store, when string) {
		t.Helper()
		for _, l := range legacyLines {
			if got := epochAt(t, s, l.tick); got != l.epoch {
				t.Errorf("%s: row at tick %d is in epoch %d, want %d", when, l.tick, got, l.epoch)
			}
		}
	}
	check(s, "migrated")
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again := mustOpen(t, path)
	check(again, "reopened")
	_ = again.Close()

	ingested := mustOpen(t, filepath.Join(t.TempDir(), "ingested.sqlite"))
	defer ingested.Close()
	for _, l := range legacyLines {
		ingest(t, ingested, line{l.event, l.tick, l.data}.jsonl())
	}
	check(ingested, "ingested")
}

// A service older than the epoch column may still write to a migrated file.
// The next Open places its rows after the ones before them, from the epoch,
// tick and map those left, and Ingest goes on from there.
func TestOpenPlacesRowsAnOlderServiceAddedLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	ingestLines(t, s, ping(1_000_000), ping(100), session(200, "M2"))
	// The session line reloads M2 far back, the drop after 900000 is a new
	// map.
	insertUnplaced(t, s, ping(300_000), session(100_000, "M2"), ping(100_020), ping(900_000), ping(50))
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := mustOpen(t, path)
	defer reopened.Close()
	for tick, want := range map[int64]int64{300_000: 2, 100_000: 2, 100_020: 2, 900_000: 2, 50: 3} {
		if got := epochAt(t, reopened, tick); got != want {
			t.Errorf("old-service row at tick %d: epoch %d, want %d", tick, got, want)
		}
	}
	ingestLines(t, reopened, ping(70))
	if got := epochAt(t, reopened, 70); got != 3 {
		t.Errorf("the next line after the placed rows is in epoch %d, want 3", got)
	}
}

// insertUnplaced stores lines the way a service older than the epoch column
// does: no epoch, until the next Open places them.
func insertUnplaced(t *testing.T, s *Store, lines ...line) {
	t.Helper()
	for _, l := range lines {
		data := l.data
		if data == "" {
			data = "{}"
		}
		if _, err := s.db.Exec(`INSERT INTO events (tick, event, player, force, data, received_at, scope, private)
			VALUES (?, ?, '', '', ?, '', 'global', 0)`, l.tick, l.event, data); err != nil {
			t.Fatalf("old-service insert: %v", err)
		}
	}
}

// A status row placed among rows an older service added later moves no tick
// in the migration either: the session line it got ahead of joins its epoch
// there, as it does in Ingest.
func TestOpenPlacesOlderServiceRowsAfterAStatusRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s := mustOpen(t, path)
	ingestLines(t, s, session(1_000_000, "M1"))
	insertUnplaced(t, s, ping(1_000_100))
	if _, err := s.NoteMap(s.Mark(), "M2", 900, 0); err != nil {
		t.Fatalf("NoteMap: %v", err)
	}
	insertUnplaced(t, s, session(300, "M2"), ping(400))
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := mustOpen(t, path)
	defer reopened.Close()
	for tick, want := range map[int64]int64{1_000_100: 1, 900: 2, 300: 2, 400: 2} {
		if got := epochAt(t, reopened, tick); got != want {
			t.Errorf("row at tick %d: epoch %d, want %d", tick, got, want)
		}
	}
}

// Open resumes from the tick of the current epoch's newest line of the
// file, never a status row's: none at all when a status row started the
// epoch, the file's own when one only named it.
func TestOpenResumesAfterAStatusRow(t *testing.T) {
	reopen := func(t *testing.T, path string, s *Store) *Store {
		t.Helper()
		if err := s.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		again := mustOpen(t, path)
		t.Cleanup(func() { _ = again.Close() })
		return again
	}
	noteMap := func(t *testing.T, s *Store, mapID string, tick int64) {
		t.Helper()
		if _, err := s.NoteMap(s.Mark(), mapID, tick, 0); err != nil {
			t.Fatalf("NoteMap: %v", err)
		}
	}
	t.Run("the session line a status row got ahead of joins its epoch", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "history.sqlite")
		s := mustOpen(t, path)
		ingestLines(t, s, session(1_000_000, "M1"), ping(1_000_100))
		noteMap(t, s, "M2", 900)
		s = reopen(t, path, s)
		ingestLines(t, s, session(300, "M2"))
		if got := epochAt(t, s, 300); got != 2 {
			t.Errorf("the session line after a reopen is in epoch %d, want 2, the status row's", got)
		}
	})
	t.Run("a status row naming an epoch leaves the file's tick to compare with", func(t *testing.T) {
		for next, want := range map[int64]int64{11_000: 1, 5_000: 2} {
			path := filepath.Join(t.TempDir(), "history.sqlite")
			s := mustOpen(t, path)
			ingestLines(t, s, ping(10_000))
			noteMap(t, s, "A", 12_000)
			s = reopen(t, path, s)
			ingestLines(t, s, ping(next))
			if got := epochAt(t, s, next); got != want {
				t.Errorf("a line at tick %d after a reopen is in epoch %d, want %d", next, got, want)
			}
		}
	})
}

// A legacy chat line on a map that only ever had one force was heard by
// every player there, so the migration makes it public. Anywhere a second
// force shows up, or any row says it was private, it stays its force's.
// Ingest never applies the rule: it cannot know what the epoch will hold.
func TestOneForceEpochsMakeLegacyChatPublic(t *testing.T) {
	type row struct {
		tick                 int64
		event, player, force string
		data                 string
		wantScope            string
		wantPrivate          int
	}
	epochs := [][]row{
		// Two forces chatting: private.
		{
			{100, "console_chat", "Ann", "team-1", `{"player":"Ann","force":"team-1","message":"T1 legacy"}`, "team-1", 1},
			{200, "console_chat", "Bob", "team-2", `{"player":"Bob","force":"team-2","message":"T2 legacy"}`, "team-2", 1},
		},
		// One force chatting, a second force only joining: private.
		{
			{50, "console_chat", "Ann", "player", `{"player":"Ann","force":"player","message":"quiet second force"}`, "player", 1},
			{60, "player_joined", "Zed", "team-2", `{"player":"Zed","force":"team-2"}`, "global", 0},
		},
		// One force, but a row that says it was private: private.
		{
			{40, "console_chat", "Ann", "player", `{"player":"Ann","force":"player","message":"a channel exists"}`, "player", 1},
			{45, "question", "Ann", "player", `{"qid":3,"player":"Ann","force":"player","text":"q","scope":"player","private":true}`, "player", 1},
		},
		// One force on every row, and the server console: public.
		{
			{30, "player_joined", "Bob", "player", `{"player":"Bob","force":"player"}`, "global", 0},
			{31, "console_chat", "Bob", "player", `{"player":"Bob","force":"player","message":"SOLO legacy"}`, "global", 0},
			{32, "console_chat", "Server", "", `{"player":"Server","message":"console"}`, "global", 0},
			{33, "research_finished", "", "player", `{"force":"player","tech":"automation","level":1}`, "global", 0},
		},
	}
	path := filepath.Join(t.TempDir(), "history.sqlite")
	var all []legacyRow
	for _, epoch := range epochs {
		for _, r := range epoch {
			all = append(all, legacyRow{r.tick, r.event, r.player, r.force, r.data, r.wantScope, r.wantPrivate})
		}
	}
	writeLegacyFile(t, path, all)
	s := mustOpen(t, path)
	defer s.Close()
	checkLegacyRows(t, s, all)
	if got := epochAt(t, s, 31); got != 4 {
		t.Fatalf("the one-force map is epoch %d, want 4: the fixture's ticks no longer start new maps", got)
	}
	if out := string(callTool(t, findTool(t, s.Tools(), "recent_chat"), nil)); !strings.Contains(out, "SOLO legacy") {
		t.Errorf("a public question cannot read the one-force map's chat:\n%s", out)
	}

	ingest(t, s, `{"event":"console_chat","tick":34,"data":{"player":"Bob","force":"player","message":"live legacy"}}`)
	if scope, private := visibilityOf(t, s, 34); scope != "player" || private != 1 {
		t.Errorf("an ingested legacy line in a one-force epoch = scope %q private %d, want private to player", scope, private)
	}
}

// A row with no epoch, as one written by a service older than the column
// would be until the next Open places it, is read by nobody. When it is the
// newest row, the current epoch is unknown and nothing is read at all.
func TestARowWithNoEpochIsNeverRead(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	insert := func(tick int64, msg string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO events (tick, event, player, force, data, received_at, scope, private)
			VALUES (?, 'console_chat', 'Nul', 'player', ?, '', 'global', 0)`,
			tick, fmt.Sprintf(`{"player":"Nul","message":%q,"scope":"global","private":false}`, msg)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insert(1016, "NO-EPOCH middle")
	ingest(t, s, `{"event":"console_chat","tick":1017,"data":{"player":"Alice","force":"team-4","message":"after it","scope":"global","private":false}}`)
	for _, a := range audiences() {
		seen := readEverything(t, s, a.ctx)
		if strings.Contains(seen, "NO-EPOCH") || strings.Contains(seen, "Nul") {
			t.Errorf("%s read a row with no epoch:\n%s", a.name, seen)
		}
		if !strings.Contains(seen, "after it") {
			t.Errorf("%s lost the rows of the current epoch:\n%s", a.name, seen)
		}
	}

	insert(1018, "NO-EPOCH newest")
	if seen := readEverything(t, s, scoped("team-4", true)); strings.Contains(seen, "public hello") || strings.Contains(seen, "NO-EPOCH") {
		t.Errorf("with no epoch on the newest row, something was still read:\n%s", seen)
	}
}

// Every history tool tells the model it covers the current map only, and the
// two that report on rows without listing them say that a private channel's
// rows count only for that channel: an empty or small answer is not proof.
func TestEveryHistoryToolSaysWhatItCovers(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	private := map[string]string{
		"last_event":   "A private channel's rows show only for that channel's own questions.",
		"count_events": "A private channel's rows count only for that channel's own questions.",
	}
	for _, tool := range s.Tools() {
		if !strings.HasSuffix(tool.Description, "History covers the current map only.") {
			t.Errorf("%s does not say it covers the current map only: %q", tool.Name, tool.Description)
		}
		if want, ok := private[tool.Name]; ok && !strings.Contains(tool.Description, want) {
			t.Errorf("%s does not say %q: %q", tool.Name, want, tool.Description)
		}
		if strings.Contains(tool.Description, "(id,") {
			t.Errorf("%s still describes an id column: %q", tool.Name, tool.Description)
		}
	}
}
