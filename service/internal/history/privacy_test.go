package history

// No read hands a private channel's rows to a question someone else will hear
// (visibility.go, CONTEXT.md "History"). The fixture plants secret lines for
// two private channels, then every tool the store serves, the briefing's
// chat and the raw filter are read under every kind of question scope.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
)

// Markers planted in private rows. Each audience below states which ones it
// must see somewhere and which it must never see anywhere.
const (
	t4Secret      = "SECRET-T4"      // in every team-4 private row
	t9Secret      = "SECRET-T9"      // in the team-9 private row
	keylessSecret = "SECRET-KEYLESS" // private rows a provider gave no usable key
)

// plantSecrets ingests the privacy fixture: public chat, joins, deaths,
// research and a rocket; team-4's private chat in the scoped shape and in the
// legacy shape, its private chat-prefix question with the echo and the
// answer; team-9's private chat; a player, Ghost, known only from team-4's
// private chat; two private lines a provider gave no usable key; a public
// question and its answer; and a server console line.
func plantSecrets(t *testing.T, s *Store) {
	t.Helper()
	for _, l := range []string{
		`{"event":"player_joined","tick":900,"data":{"player":"Bob","force":"team-4"}}`,
		`{"event":"player_joined","tick":901,"data":{"player":"Cid","force":"team-9"}}`,
		`{"event":"console_chat","tick":1000,"data":{"player":"Alice","force":"team-4","message":"public hello","scope":"global","private":false}}`,
		`{"event":"console_chat","tick":1001,"data":{"player":"Bob","force":"team-4","message":"SECRET-T4 chat","scope":"team-4","private":true}}`,
		`{"event":"console_chat","tick":1002,"data":{"player":"Bob","force":"team-4","message":"SECRET-T4 legacy"}}`,
		`{"event":"console_chat","tick":1003,"data":{"player":"Bob","force":"team-4","message":"?SECRET-T4 question","scope":"team-4","private":true}}`,
		`{"event":"question","tick":1003,"data":{"qid":11,"player":"Bob","force":"team-4","text":"SECRET-T4 question","scope":"team-4","private":true}}`,
		`{"event":"answer","tick":1004,"data":{"qid":11,"shape":"summary"}}`,
		`{"event":"console_chat","tick":1005,"data":{"player":"Cid","force":"team-9","message":"SECRET-T9 chat","scope":"team-9","private":true}}`,
		`{"event":"console_chat","tick":1006,"data":{"player":"Ghost","force":"team-4","message":"SECRET-T4 ghost","scope":"team-4","private":true}}`,
		`{"event":"console_chat","tick":1007,"data":{"player":"Dee","force":"team-5","message":"SECRET-KEYLESS global","scope":"global","private":true}}`,
		`{"event":"console_chat","tick":1008,"data":{"player":"Dee","force":"team-5","message":"SECRET-KEYLESS none","private":true}}`,
		`{"event":"question","tick":1009,"data":{"qid":12,"player":"Alice","force":"team-4","text":"public question","scope":"global","private":false}}`,
		`{"event":"answer","tick":1010,"data":{"qid":12,"shape":"summary"}}`,
		`{"event":"console_chat","tick":1011,"data":{"player":"Server","message":"server says hi"}}`,
		`{"event":"player_died","tick":1012,"data":{"player":"Bob","force":"team-4","cause":"biter"}}`,
		`{"event":"research_finished","tick":1013,"data":{"force":"team-4","tech":"automation","level":1}}`,
		`{"event":"rocket_launched","tick":1014,"data":{"force":"team-9","surface":"nauvis"}}`,
		`{"event":"player_left","tick":1015,"data":{"player":"Bob","force":"team-4"}}`,
	} {
		ingest(t, s, l)
	}
}

// audience is one kind of question and what it may and may not read.
type audience struct {
	name  string
	ctx   context.Context
	sees  []string
	never []string
}

func scoped(key string, private bool) context.Context {
	return chatscope.With(context.Background(), chatscope.Scope{Key: key, Private: private})
}

// audiences covers fail-closed (no scope at all), the global channel, the
// two ways a provider can hand out an unusable private scope, a public
// question that carries a channel's key, and the two private channels.
func audiences() []audience {
	everySecret := []string{t4Secret, t9Secret, keylessSecret}
	return []audience{
		{name: "no scope on the context", ctx: context.Background(), never: everySecret},
		{name: "global", ctx: scoped(chatscope.GlobalKey, false), never: everySecret},
		{name: "public with team-4's key", ctx: scoped("team-4", false), never: everySecret},
		{name: "private with the global key", ctx: scoped(chatscope.GlobalKey, true), never: everySecret},
		{name: "private with no key", ctx: scoped("", true), never: everySecret},
		{name: "team-4 private", ctx: scoped("team-4", true), sees: []string{t4Secret}, never: []string{t9Secret, keylessSecret}},
		{name: "team-9 private", ctx: scoped("team-9", true), sees: []string{t9Secret}, never: []string{t4Secret, keylessSecret}},
	}
}

// argVariants are the arguments every tool is called with: enough to reach
// each row by event, player, force and window. A variant a tool cannot take
// is skipped, but every tool must accept at least one.
var argVariants = []map[string]any{
	nil,
	{"limit": 20},
	{"event": "console_chat", "limit": 20},
	{"event": "question", "limit": 20},
	{"event": "answer", "limit": 20},
	{"event": "console_chat", "force": "team-4", "limit": 20},
	{"event": "console_chat", "force": "team-9", "limit": 20},
	{"event": "console_chat", "player": "Bob", "limit": 20},
	{"event": "console_chat", "player": "Ghost"},
	{"event": "question", "player": "Bob"},
	{"player": "Bob"},
	{"player": "Ghost"},
	{"since_tick": 1},
	{"since_tick": 1, "limit": 20},
}

// readEverything calls every tool the store serves with every variant, then
// the briefing's chat and the raw filter, and returns all of it as one text.
// A tool that answers no variant at all fails the test: whoever adds a tool
// adds the argument it needs here, rather than leaving it unread.
func readEverything(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	var all strings.Builder
	for _, tool := range s.Tools() {
		answered := 0
		for _, args := range argVariants {
			raw, _ := json.Marshal(args)
			if args == nil {
				raw = nil
			}
			out, err := tool.Call(ctx, raw)
			if err != nil {
				continue
			}
			answered++
			fmt.Fprintf(&all, "%s %s:\n%s\n", tool.Name, raw, out)
		}
		if answered == 0 {
			t.Errorf("%s answered none of the argument variants: add one it takes", tool.Name)
		}
	}
	for _, line := range s.RecentChatLines(ctx, 50) {
		fmt.Fprintf(&all, "briefing chat: %s %s\n", line.Player, line.Message)
	}
	lines, err := s.FilteredChat(ctx, 0, 200)
	if err != nil {
		t.Fatalf("FilteredChat: %v", err)
	}
	for _, line := range lines {
		fmt.Fprintf(&all, "filtered chat: %s %s\n", line.Player, line.Message)
	}
	return all.String()
}

// TestNoReadHandsAPrivateRowToAnotherScope is the rule itself: a private row
// reaches a question only when that question was asked privately in the same
// scope, through every tool, the briefing's chat and the filter alike.
func TestNoReadHandsAPrivateRowToAnotherScope(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)

	for _, a := range audiences() {
		t.Run(a.name, func(t *testing.T) {
			seen := readEverything(t, s, a.ctx)
			for _, m := range a.never {
				if strings.Contains(seen, m) {
					t.Errorf("%s read %q:\n%s", a.name, m, seen)
				}
			}
			for _, m := range a.sees {
				if !strings.Contains(seen, m) {
					t.Errorf("%s never read its own %q:\n%s", a.name, m, seen)
				}
			}
			if !strings.Contains(seen, "public hello") || !strings.Contains(seen, "public question") {
				t.Errorf("%s lost a public row:\n%s", a.name, seen)
			}
		})
	}
}

// TestEachReadShowsAPrivateChannelItsOwnRows: the same channel's private rows
// do come back, through each route a question reads chat by, not merely
// somewhere among them.
func TestEachReadShowsAPrivateChannelItsOwnRows(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	ctx := scoped("team-4", true)
	ts := s.Tools()

	call := func(name string, args map[string]any) string {
		raw, _ := json.Marshal(args)
		out, err := findTool(t, ts, name).Call(ctx, raw)
		if err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		return string(out)
	}
	for _, c := range []struct {
		route string
		got   string
		want  []string
	}{
		{"recent_chat", call("recent_chat", map[string]any{"limit": 20}), []string{"SECRET-T4 chat", "SECRET-T4 legacy", "SECRET-T4 ghost", "public hello"}},
		{"recent_events", call("recent_events", map[string]any{"event": "question"}), []string{"SECRET-T4 question", "public question"}},
		{"last_event", call("last_event", map[string]any{"event": "console_chat", "player": "Bob"}), []string{"SECRET-T4 question"}},
		{"catch_up", call("catch_up", map[string]any{"since_tick": 1}), []string{"SECRET-T4 chat", "public hello"}},
		{"catch_up for a player only private chat knows", call("catch_up", map[string]any{"player": "Ghost"}), []string{"SECRET-T4 ghost"}},
		{"briefing chat", fmt.Sprint(s.RecentChatLines(ctx, 10)), []string{"SECRET-T4 chat", "SECRET-T4 legacy"}},
	} {
		for _, want := range c.want {
			if !strings.Contains(c.got, want) {
				t.Errorf("%s under team-4's own scope is missing %q:\n%s", c.route, want, c.got)
			}
		}
	}
	if got := call("recent_chat", map[string]any{"limit": 20}); strings.Contains(got, "?SECRET-T4 question") {
		t.Errorf("the echo of team-4's own chat-prefix question should still be dropped:\n%s", got)
	}
}

// TestCatchUpSaysNoRecordOfAPlayerOnlyPrivateChatKnows: a player seen only in
// another channel's private chat reads as never seen, not as someone with a
// record and an empty window.
func TestCatchUpSaysNoRecordOfAPlayerOnlyPrivateChatKnows(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	catchUp := findTool(t, s.Tools(), "catch_up")
	for _, ctx := range []context.Context{context.Background(), scoped(chatscope.GlobalKey, false), scoped("team-9", true)} {
		out, err := catchUp.Call(ctx, json.RawMessage(`{"player":"Ghost"}`))
		if err != nil {
			t.Fatalf("catch_up: %v", err)
		}
		if _, rows := splitColumnar(t, out); len(rows) != 1 || !strings.Contains(rows[0], "no record of this player") {
			t.Errorf("catch_up for Ghost under %v = %q, want the one no-record row", chatscope.From(ctx), out)
		}
	}
}

// TestAPrivateLineNeverMovesAPublicWindow: catch_up's "now" is the newest
// row the question may see. Were it the newest row of all, a private line a
// day after everything public would push the public window past every
// public row, and an empty answer would say a private channel spoke since.
func TestAPrivateLineNeverMovesAPublicWindow(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	ingest(t, s, `{"event":"player_left","tick":500,"data":{"player":"Dave","force":"team-9"}}`)
	ingest(t, s, `{"event":"research_finished","tick":1000,"data":{"force":"team-9","tech":"automation","level":1}}`)
	ingest(t, s, fmt.Sprintf(`{"event":"console_chat","tick":%d,"data":{"player":"Bob","force":"team-4","message":"SECRET-T4 late","scope":"team-4","private":true}}`,
		1000+catchUpWindowTicks+10))
	catchUp := findTool(t, s.Tools(), "catch_up")

	for _, c := range []struct {
		name         string
		ctx          context.Context
		wantResearch bool
		wantSecret   bool
	}{
		{"global", scoped(chatscope.GlobalKey, false), true, false},
		{"team-9 private", scoped("team-9", true), true, false},
		{"team-4 private", scoped("team-4", true), false, true},
	} {
		out, err := catchUp.Call(c.ctx, json.RawMessage(`{"player":"Dave"}`))
		if err != nil {
			t.Fatalf("catch_up: %v", err)
		}
		if got := strings.Contains(string(out), "automation"); got != c.wantResearch {
			t.Errorf("%s: research shown = %v, want %v:\n%s", c.name, got, c.wantResearch, out)
		}
		if got := strings.Contains(string(out), t4Secret); got != c.wantSecret {
			t.Errorf("%s: team-4's line shown = %v, want %v:\n%s", c.name, got, c.wantSecret, out)
		}
	}
}

// TestCountEventsCountsOnlyVisibleRows: a total over hidden rows would tell
// another channel how much a private one said.
func TestCountEventsCountsOnlyVisibleRows(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	count := findTool(t, s.Tools(), "count_events")

	// Public chat: Alice's line and the server's. team-4 adds its four
	// private lines, team-9 its one. The keyless pair counts for nobody.
	for _, c := range []struct {
		name                     string
		ctx                      context.Context
		chat, questions, answers int64
	}{
		{"no scope", context.Background(), 2, 1, 1},
		{"global", scoped(chatscope.GlobalKey, false), 2, 1, 1},
		{"private with the global key", scoped(chatscope.GlobalKey, true), 2, 1, 1},
		{"team-4 private", scoped("team-4", true), 6, 2, 2},
		{"team-9 private", scoped("team-9", true), 3, 1, 1},
	} {
		for event, want := range map[string]int64{"console_chat": c.chat, "question": c.questions, "answer": c.answers} {
			out, err := count.Call(c.ctx, json.RawMessage(`{"event":"`+event+`"}`))
			if err != nil {
				t.Fatalf("count_events %s: %v", event, err)
			}
			_, rows := splitColumnar(t, out)
			if got, _ := strconv.ParseInt(rows[0], 10, 64); got != want {
				t.Errorf("%s: count_events(%s) = %d, want %d", c.name, event, got, want)
			}
		}
	}
}

// TestAnUnclassifiedRowIsNeverRead: a row whose private column is still NULL,
// as one written by a service older than the column would be until the next
// Open classifies it, is read by nobody, its own channel included. It sits
// in the current epoch, so only the missing private flag keeps it out.
func TestAnUnclassifiedRowIsNeverRead(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	if _, err := s.db.Exec(`INSERT INTO events (tick, event, player, force, data, received_at, scope, epoch)
		VALUES (2000, 'console_chat', 'Nul', 'team-4', '{"player":"Nul","message":"UNCLASSIFIED-ROW"}', '', 'team-4', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for _, a := range audiences() {
		seen := readEverything(t, s, a.ctx)
		if strings.Contains(seen, "UNCLASSIFIED-ROW") || strings.Contains(seen, "Nul") {
			t.Errorf("%s read a row with no visibility yet:\n%s", a.name, seen)
		}
		if !strings.Contains(seen, "public hello") {
			t.Errorf("%s read nothing at all, so this test proves nothing:\n%s", a.name, seen)
		}
	}
}

// TestNoReplyCarriesARowIDOrAQID: row ids and question ids count up across
// every channel, so the gaps between the ones a question may see would count
// another channel's private rows. Neither reaches the model; the file keeps
// both.
func TestNoReplyCarriesARowIDOrAQID(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	defer s.Close()
	plantSecrets(t, s)
	for _, a := range audiences() {
		seen := readEverything(t, s, a.ctx)
		if strings.Contains(seen, `"qid"`) {
			t.Errorf("%s read a qid:\n%s", a.name, seen)
		}
	}
	ctx := scoped("team-4", true)
	out, err := findTool(t, s.Tools(), "recent_events").Call(ctx, json.RawMessage(`{"limit":20}`))
	if err != nil {
		t.Fatalf("recent_events: %v", err)
	}
	header, rows := splitColumnar(t, out)
	if header != "tick\tevent\tplayer\tforce\tdata" {
		t.Errorf("header = %q, want no id column", header)
	}
	var question, answer string
	for _, row := range rows {
		f := splitRow(row)
		if len(f) != 5 {
			t.Fatalf("row %q has %d fields, want 5", row, len(f))
		}
		if _, err := strconv.ParseInt(f[0], 10, 64); err != nil || f[0] == "" {
			t.Errorf("row %q does not lead with its tick", row)
		}
		switch f[1] {
		case "question":
			question = f[4]
		case "answer":
			answer = f[4]
		}
	}
	if want := `{"force":"team-4","player":"Bob","private":true,"scope":"team-4","text":"SECRET-T4 question"}`; question != want {
		t.Errorf("the question's data = %q, want everything but the qid", question)
	}
	if answer != `{"shape":"summary"}` {
		t.Errorf("the answer's data = %q, want the shape alone", answer)
	}
	var kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE json_extract(data, '$.qid') IS NOT NULL`).Scan(&kept); err != nil || kept != 4 {
		t.Errorf("the file keeps %d qids (%v), want all 4", kept, err)
	}
}

// A question's text keeps what the player typed: a qid taken out never
// re-escapes the rest.
func TestModelDataKeepsTheTextAsTyped(t *testing.T) {
	got := modelData("question", json.RawMessage(`{"qid":3,"text":"is 2 < 3 & 5 > 4?"}`))
	if want := `{"text":"is 2 < 3 & 5 > 4?"}`; got != want {
		t.Errorf("modelData = %q, want %q", got, want)
	}
	if got := modelData("console_chat", json.RawMessage(`{"qid":3,"message":"m"}`)); got != `{"qid":3,"message":"m"}` {
		t.Errorf("a chat line's data changed: %q", got)
	}
	if got := modelData("answer", json.RawMessage(`not json`)); got != "{}" {
		t.Errorf("unparseable answer data = %q, want {}", got)
	}
}
