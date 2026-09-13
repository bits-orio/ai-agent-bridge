package history

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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

func ingest(t *testing.T, s *Store, line string) {
	t.Helper()
	if err := s.Ingest([]byte(line)); err != nil {
		t.Fatalf("ingest %q: %v", line, err)
	}
}

// splitColumnar breaks a tool's columnar reply (section 13: a header line
// then one tab-separated line per row, no envelope) into the header and
// the rows below it. A reply with no rows still has a header and an empty,
// non-nil rows slice, never an error.
func splitColumnar(t *testing.T, out json.RawMessage) (header string, rows []string) {
	t.Helper()
	lines := strings.Split(string(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("columnar reply has no header: %q", out)
	}
	return lines[0], lines[1:]
}

func splitRow(row string) []string {
	return strings.Split(row, "\t")
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
		ingest(t, s, line)
	}
	for _, l := range []string{
		`{"event":"player_died","tick":30,"data":{"player":"Bob","force":"player","cause":"biter"}}`,
		`{"event":"player_died","tick":31,"data":{"player":"Alice","force":"player","cause":"biter"}}`,
		`{"event":"player_died","tick":40,"data":{"player":"Bob","force":"player","cause":"train"}}`,
		`{"event":"other","tick":50,"data":{}}`,
	} {
		ingest(t, s, l)
	}
	return s.Tools()
}

// eventRowTicks parses a recent_events/last_event columnar reply (header
// "id\ttick\tevent\tplayer\tforce\tdata") into the tick column of each row,
// checking the header along the way.
func eventRowTicks(t *testing.T, out json.RawMessage) []int64 {
	t.Helper()
	header, rows := splitColumnar(t, out)
	if header != eventHeader {
		t.Fatalf("header = %q, want %q", header, eventHeader)
	}
	ticks := make([]int64, 0, len(rows))
	for _, row := range rows {
		fields := splitRow(row)
		if len(fields) != 6 {
			t.Fatalf("row %q has %d fields, want 6", row, len(fields))
		}
		tick, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("row %q: bad tick: %v", row, err)
		}
		ticks = append(ticks, tick)
	}
	return ticks
}

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

func TestRecentEvents(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ts := seed(t, s)
	recent := findTool(t, ts, "recent_events")

	t.Run("default limit is 10, newest first", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping"})
		got := eventRowTicks(t, out)
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
		got := eventRowTicks(t, out)
		if want := []int64{25, 24, 23}; !equalInt64(got, want) {
			t.Errorf("ticks = %v, want %v", got, want)
		}
	})

	t.Run("limit above the cap clamps to 20", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping", "limit": 100})
		got := eventRowTicks(t, out)
		if len(got) != maxRecentLimit {
			t.Fatalf("len = %d, want %d (capped)", len(got), maxRecentLimit)
		}
		if got[0] != 25 || got[len(got)-1] != 6 {
			t.Errorf("ticks span %v..%v, want 25..6", got[0], got[len(got)-1])
		}
	})

	t.Run("player filter", func(t *testing.T) {
		out := callTool(t, recent, map[string]any{"event": "ping", "player": "Bob", "limit": 20})
		got := eventRowTicks(t, out)
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
		header, rows := splitColumnar(t, out)
		if header != eventHeader {
			t.Errorf("header = %q, want %q", header, eventHeader)
		}
		if len(rows) != 0 {
			t.Errorf("rows = %v, want none (header alone is a valid empty result)", rows)
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
		got := eventRowTicks(t, out)
		if len(got) != 1 || got[0] != 40 {
			t.Errorf("ticks = %v, want [40] (Bob's newest death, not tick 30)", got)
		}
	})

	t.Run("none recorded for an unmatched filter", func(t *testing.T) {
		out := callTool(t, last, map[string]any{"event": "player_died", "player": "Nobody"})
		if string(out) != "none recorded" {
			t.Errorf("result = %q, want %q", out, "none recorded")
		}
	})

	t.Run("none recorded for an unknown event key", func(t *testing.T) {
		out := callTool(t, last, map[string]any{"event": "never_happened"})
		if string(out) != "none recorded" {
			t.Errorf("result = %q, want %q", out, "none recorded")
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
		header, rows := splitColumnar(t, out)
		if header != "count" {
			t.Fatalf("header = %q, want %q", header, "count")
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want exactly one", rows)
		}
		n, err := strconv.ParseInt(rows[0], 10, 64)
		if err != nil {
			t.Fatalf("row %q: %v", rows[0], err)
		}
		return n
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
	ingest(t, s1, `{"event":"player_joined","tick":5,"data":{"player":"Alice","force":"player"}}`)
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
	header, rows := splitColumnar(t, out)
	if header != eventHeader || len(rows) != 1 {
		t.Fatalf("reply = %q, want one row under %q", out, eventHeader)
	}
	fields := splitRow(rows[0])
	if fields[3] != "Alice" || fields[1] != "5" {
		t.Errorf("row = %v, want player Alice at tick 5", fields)
	}
}

// chatFixture ingests the same shape of console_chat / question rows the
// filter has to sort out: a server console line, a chat-prefix question and
// its own console_chat echo at the same tick, an immediate repeat, and one
// distinct organic line, in that order (ticks 100..105).
func chatFixture(t *testing.T, s *Store) {
	t.Helper()
	for _, l := range []string{
		`{"event":"console_chat","tick":100,"data":{"player":"Bob","force":"player","message":"hello"}}`,
		`{"event":"console_chat","tick":101,"data":{"player":"Server","message":"[MTS] milestone"}}`,
		`{"event":"console_chat","tick":102,"data":{"player":"Bob","force":"player","message":"?what time is it"}}`,
		`{"event":"question","tick":102,"data":{"qid":7,"player":"Bob","force":"player","text":"what time is it","scope":"global","private":false}}`,
		`{"event":"console_chat","tick":103,"data":{"player":"Bob","force":"player","message":"same line"}}`,
		`{"event":"console_chat","tick":104,"data":{"player":"Bob","force":"player","message":"same line"}}`,
		`{"event":"console_chat","tick":105,"data":{"player":"Alice","force":"player","message":"hi all"}}`,
	} {
		ingest(t, s, l)
	}
}

// TestFilteredChatDropsServerBotQuestionsAndRepeats covers the three things
// the filter behind recent_chat (and catch_up) must drop, per section 15:
// a "Server" line, a console_chat row that is itself the echo of a
// chat-prefix question (matched against the "question" event its trigger
// logs at the same tick), and an immediate repeat.
func TestFilteredChatDropsServerBotQuestionsAndRepeats(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	chatFixture(t, s)

	got, err := s.FilteredChat(context.Background(), 0, chatFetchLimit)
	if err != nil {
		t.Fatalf("FilteredChat: %v", err)
	}
	want := []ChatLine{
		{Tick: 105, Player: "Alice", Message: "hi all"},
		{Tick: 104, Player: "Bob", Message: "same line"},
		{Tick: 100, Player: "Bob", Message: "hello"},
	}
	if len(got) != len(want) {
		t.Fatalf("FilteredChat = %+v, want %+v", got, want)
	}
	for i, line := range want {
		if got[i] != line {
			t.Errorf("line %d = %+v, want %+v", i, got[i], line)
		}
	}
}

// TestRecentChat exercises the same fixture through the tool itself: the
// columnar shape, the default limit, and an explicit limit trimming to the
// newest rows (chat arrives already newest first).
func TestRecentChat(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	chatFixture(t, s)
	recentChat := findTool(t, s.Tools(), "recent_chat")

	t.Run("default limit, columnar, newest first", func(t *testing.T) {
		out := callTool(t, recentChat, nil)
		header, rows := splitColumnar(t, out)
		if header != chatHeader {
			t.Fatalf("header = %q, want %q", header, chatHeader)
		}
		if len(rows) != 3 {
			t.Fatalf("rows = %v, want 3 organic lines", rows)
		}
		if got := splitRow(rows[0]); got[1] != "Alice" || got[2] != "hi all" {
			t.Errorf("row 0 = %v, want Alice/hi all (newest)", got)
		}
	})

	t.Run("limit trims to the newest rows", func(t *testing.T) {
		out := callTool(t, recentChat, map[string]any{"limit": 1})
		_, rows := splitColumnar(t, out)
		if len(rows) != 1 {
			t.Fatalf("rows = %v, want exactly 1", rows)
		}
		if got := splitRow(rows[0]); got[1] != "Alice" {
			t.Errorf("row = %v, want Alice's line (the newest)", got)
		}
	})
}

// TestRecentChatSanitisesEmbeddedTab covers the one sanitising rule section
// 13 sets: a tab already inside a value (here, a chat message) is replaced
// with a single space before the row is built, so the row never carries
// more tabs than recent_chat's three columns need.
func TestRecentChatSanitisesEmbeddedTab(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	// The backtick literal's \t is two literal characters, the JSON escape
	// for a tab, exactly what a real chat message carrying one would send
	// (JSON itself forbids an unescaped literal tab byte inside a string).
	ingest(t, s, `{"event":"console_chat","tick":200,"data":{"player":"Bob","force":"player","message":"hello\tworld"}}`)
	recentChat := findTool(t, s.Tools(), "recent_chat")

	out := callTool(t, recentChat, nil)
	_, rows := splitColumnar(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly 1", rows)
	}
	fields := splitRow(rows[0])
	if len(fields) != 3 {
		t.Fatalf("row %q split into %d fields on tab, want 3 (the embedded tab must become a space)", rows[0], len(fields))
	}
	if fields[2] != "hello world" {
		t.Errorf("message = %q, want %q", fields[2], "hello world")
	}
}

// TestCatchUp covers the window rule: it runs from the player's own last
// player_left tick, but never further back than the 24-hour cap even when
// that tick is older than a day. Chat and the tracked event kinds merge by
// tick, newest first.
func TestCatchUp(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	catchUp := findTool(t, s.Tools(), "catch_up")

	const left = int64(1000)
	ingest(t, s, `{"event":"player_joined","tick":900,"data":{"player":"Dave","force":"player"}}`)
	ingest(t, s, fmt.Sprintf(`{"event":"player_left","tick":%d,"data":{"player":"Dave","force":"player"}}`, left))
	// Before Dave left: never something he "missed", must not appear.
	ingest(t, s, `{"event":"player_died","tick":950,"data":{"player":"Alice","force":"player","cause":"biter"}}`)

	// Push "now" (the newest tick this store has seen, of any kind) far
	// enough past left that the 24-hour cap, not Dave's own departure,
	// draws the window's actual start.
	nowTick := left + catchUpWindowTicks + 10000
	windowStart := nowTick - catchUpWindowTicks // = left + 10000

	// After Dave left, but still outside the capped window: must be
	// dropped by the cap even though it is after his own departure.
	ingest(t, s, fmt.Sprintf(`{"event":"research_finished","tick":%d,"data":{"force":"player","tech":"automation","level":1}}`, left+500))
	// Inside the capped window: must appear.
	chatTick := windowStart + 200
	ingest(t, s, fmt.Sprintf(`{"event":"console_chat","tick":%d,"data":{"player":"Eve","force":"player","message":"hi"}}`, chatTick))
	// This is the row that sets "now" itself, also inside the window.
	ingest(t, s, fmt.Sprintf(`{"event":"rocket_launched","tick":%d,"data":{"force":"player","surface":"nauvis"}}`, nowTick))

	out := callTool(t, catchUp, map[string]any{"player": "Dave"})
	header, rows := splitColumnar(t, out)
	if header != catchUpHeader {
		t.Fatalf("header = %q, want %q", header, catchUpHeader)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2 (the pre-departure death and the capped-out research must be excluded)", rows)
	}

	rocket := splitRow(rows[0])
	if rocket[0] != strconv.FormatInt(nowTick, 10) || rocket[1] != "rocket_launched" {
		t.Errorf("row 0 = %v, want the rocket_launched row first (newest)", rocket)
	}
	chat := splitRow(rows[1])
	if chat[0] != strconv.FormatInt(chatTick, 10) || chat[1] != "console_chat" || chat[2] != "Eve" || chat[3] != "hi" {
		t.Errorf("row 1 = %v, want Eve's chat line second", chat)
	}
}

// TestCatchUpRowBudget checks the 15-row cut: more than 15 candidate rows
// in the window collapse to the newest 15.
func TestCatchUpRowBudget(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	catchUp := findTool(t, s.Tools(), "catch_up")

	ingest(t, s, `{"event":"player_left","tick":1000,"data":{"player":"Dave","force":"player"}}`)
	for i := 0; i < 20; i++ {
		tick := 1000 + int64(i) + 1
		ingest(t, s, fmt.Sprintf(`{"event":"player_joined","tick":%d,"data":{"player":"P%d","force":"player"}}`, tick, i))
	}

	out := callTool(t, catchUp, map[string]any{"player": "Dave"})
	_, rows := splitColumnar(t, out)
	if len(rows) != catchUpRowBudget {
		t.Fatalf("rows = %d, want %d (the row budget)", len(rows), catchUpRowBudget)
	}
	// Newest first: the last-ingested join (tick 1020) leads.
	if got := splitRow(rows[0])[0]; got != "1020" {
		t.Errorf("row 0 tick = %s, want 1020 (newest)", got)
	}
}

// TestCatchUpNeverSeen: a player with no row at all in the log gets a
// notice row rather than an error or a silently empty list.
func TestCatchUpNeverSeen(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	seed(t, s) // Alice and Bob exist; "Ghost" never does.
	catchUp := findTool(t, s.Tools(), "catch_up")

	out := callTool(t, catchUp, map[string]any{"player": "Ghost"})
	header, rows := splitColumnar(t, out)
	if header != catchUpHeader {
		t.Fatalf("header = %q, want %q", header, catchUpHeader)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly one notice row", rows)
	}
	fields := splitRow(rows[0])
	want := []string{"", "none", "Ghost", "no record of this player"}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("field %d = %q, want %q (row %v)", i, fields[i], want[i], fields)
		}
	}

	if _, err := catchUp.Call(context.Background(), []byte(`{}`)); err == nil {
		t.Error("got nil error, want error for missing \"player\"")
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
