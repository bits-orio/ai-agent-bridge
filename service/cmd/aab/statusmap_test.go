// The map the companion's status names reaches history (cursor.go noteMap,
// history.Store.NoteMap): a new map whose session line history never saw
// still ends every read of the earlier map, at greet and at every later look.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
)

// firstMap is a mature map's lines under the map id M1, as the tailer
// stored them: its session line, public chat and team-4's private chat.
var firstMap = []string{
	`{"event":"session","tick":4000000,"data":{"map_id":"M1"}}`,
	`{"event":"console_chat","tick":4000100,"data":{"player":"Alice","force":"team-4","message":"OLD public hello","scope":"global","private":false}}`,
	`{"event":"console_chat","tick":4000200,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 secret","scope":"team-4","private":true}}`,
}

func ingestAll(t *testing.T, store *history.Store, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if err := store.Ingest([]byte(l)); err != nil {
			t.Fatalf("ingest %q: %v", l, err)
		}
	}
}

// heard is what a question in one scope reads of history: recent_chat,
// recent_events and the briefing's chat.
func heard(t *testing.T, store *history.Store, key string, private bool) string {
	t.Helper()
	ctx := chatscope.With(context.Background(), chatscope.Scope{Key: key, Private: private})
	var all strings.Builder
	for _, tool := range store.Tools() {
		if tool.Name != "recent_chat" && tool.Name != "recent_events" {
			continue
		}
		out, err := tool.Call(ctx, json.RawMessage(`{"limit":20}`))
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		all.Write(out)
		all.WriteString("\n")
	}
	for _, line := range store.RecentChatLines(ctx, 20) {
		all.WriteString(line.Message + "\n")
	}
	return all.String()
}

// sessionRows is how many session rows the current map's history holds.
func sessionRows(t *testing.T, store *history.Store) int {
	t.Helper()
	for _, tool := range store.Tools() {
		if tool.Name == "recent_events" {
			out, err := tool.Call(context.Background(), json.RawMessage(`{"event":"session","limit":20}`))
			if err != nil {
				t.Fatalf("recent_events: %v", err)
			}
			return strings.Count(string(out), "\n")
		}
	}
	t.Fatal("no recent_events tool")
	return 0
}

// The server moved to a new map while the service was down. Its session
// line came before the end of the file the tailer starts at, so history
// never sees it. The status at greet names the map, and no question on the
// new map reads the earlier one. The session line, should it arrive after
// all, at a lower tick than the status, joins the same epoch.
func TestAMapChangedWhileTheServiceWasDownEndsTheEarlierMapsHistory(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick, companion.mapID = 600, "M2"
	r := newTestRunner(t, companion)
	ingestAll(t, r.store, firstMap...)

	r.greet(context.Background())
	for _, who := range []struct {
		key     string
		private bool
	}{{chatscope.GlobalKey, false}, {"team-4", true}} {
		if got := heard(t, r.store, who.key, who.private); strings.Contains(got, "OLD") {
			t.Errorf("a %s question on the new map reads the earlier map:\n%s", who.key, got)
		}
	}
	if !strings.Contains(log.String(), "history: a new map from the companion's status (map M2, tick 600)") {
		t.Errorf("the move to the new map was not logged:\n%s", log.String())
	}

	ingestAll(t, r.store,
		`{"event":"session","tick":300,"data":{"map_id":"M2"}}`,
		`{"event":"console_chat","tick":310,"data":{"player":"Bob","force":"team-4","message":"NEW-T4 secret","scope":"team-4","private":true}}`)
	if got := heard(t, r.store, "team-4", true); !strings.Contains(got, "NEW-T4 secret") || strings.Contains(got, "OLD") {
		t.Errorf("team-4 reads the wrong map once the session line came in:\n%s", got)
	}
	if n := sessionRows(t, r.store); n != 2 {
		t.Errorf("the new map's history holds %d session rows, want 2, the status row and the session line in one epoch", n)
	}
}

// With events off the new map writes no session line at all. A later look
// at the status, not only greet, tells history, and greet on the map history
// already has moves nothing.
func TestEveryLookAtTheStatusTellsHistoryTheMap(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick, companion.mapID = 4_000_300, "M1"
	r := newTestRunner(t, companion)
	ingestAll(t, r.store, firstMap...)
	ctx := context.Background()

	r.greet(ctx)
	if got := heard(t, r.store, "team-4", true); !strings.Contains(got, "OLD-T4 secret") {
		t.Errorf("greet on the map history already has lost its rows:\n%s", got)
	}
	if strings.Contains(log.String(), "history: a new map") {
		t.Errorf("greet on the map history already has logged a new map:\n%s", log.String())
	}

	companion.newMap(600, nil)
	companion.mapID = "M2"
	tickUntilStatus(ctx, r)
	if got := heard(t, r.store, "team-4", true); strings.Contains(got, "OLD") {
		t.Errorf("a question on the new map reads the earlier map:\n%s", got)
	}
}

// The tailer runs beside the status call. A line it stores while the call is
// out was written after the status, a few ticks higher, and on a map in its
// first five minutes a status judged against that line reads as a drop into
// a new map, hiding the map's own rows. Every look marks history before the
// call goes out, so nothing moves: at greet, at a regular look, and at the
// look after an outage.
func TestALineStoredWhileTheStatusCallIsOutMovesNothing(t *testing.T) {
	for _, when := range []string{"greet", "a regular look", "the look after an outage"} {
		t.Run(when, func(t *testing.T) {
			log := captureLog(t)
			companion := newFakeCompanion(nil)
			companion.gameTick, companion.mapID = 4000, "A"
			r := newTestRunner(t, companion)
			ctx := context.Background()
			ingestAll(t, r.store, `{"event":"session","tick":1000,"data":{"map_id":"A"}}`)
			line := 0
			race := func() {
				line++
				companion.gameTick += 60
				ingestAll(t, r.store, fmt.Sprintf(`{"event":"console_chat","tick":%d,"data":{"player":"Bob","force":"team-4","message":"T4 early secret %d","scope":"team-4","private":true}}`,
					companion.gameTick+2, line))
			}
			switch when {
			case "greet":
				companion.onStatus = race
				r.greet(ctx)
			case "a regular look":
				r.greet(ctx)
				companion.onStatus = race
				tickUntilStatus(ctx, r)
			default:
				r.greet(ctx)
				companion.crash()
				r.tick(ctx)
				companion.down = false
				companion.onStatus = race
				r.tick(ctx)
			}
			if line == 0 {
				t.Fatal("no line was stored while a status call was out")
			}
			if strings.Contains(log.String(), "history: a new map") {
				t.Errorf("a status a few ticks behind a line stored meanwhile started a new map:\n%s", log.String())
			}
			if got := heard(t, r.store, "team-4", true); !strings.Contains(got, "T4 early secret 1") {
				t.Errorf("team-4 lost the map's own line:\n%s", got)
			}
		})
	}
}

// Round two of a server that resets every round from one fixed seed carries
// round one's map id, and the service first sees it at minute eleven, past
// the five-minute rule. Its companion has issued no question yet, fewer than
// round one logged, so history starts round two afresh: its team-4 never
// reads round one's team-4.
func TestASameSeedRoundIsANewMapToHistory(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick, companion.mapID = 40_000, "seed-0-777"
	r := newTestRunner(t, companion)
	ingestAll(t, r.store,
		`{"event":"session","tick":1000,"data":{"map_id":"seed-0-777"}}`,
		`{"event":"console_chat","tick":72000,"data":{"player":"Bob","force":"team-4","message":"OLD-T4 secret","scope":"team-4","private":true}}`,
		`{"event":"question","tick":72100,"data":{"qid":4,"player":"Bob","force":"team-4","text":"q","scope":"team-4","private":true}}`)

	r.greet(context.Background())
	ingestAll(t, r.store, `{"event":"console_chat","tick":40100,"data":{"player":"Cid","force":"team-4","message":"NEW-T4 line","scope":"team-4","private":true}}`)
	if got := heard(t, r.store, "team-4", true); strings.Contains(got, "OLD-T4") || !strings.Contains(got, "NEW-T4 line") {
		t.Errorf("round two's team-4 reads the wrong round:\n%s", got)
	}
	if !strings.Contains(log.String(), "history: a new map from the companion's status (map seed-0-777, tick 40000)") {
		t.Errorf("the new round was not logged:\n%s", log.String())
	}
}

// A restart between greet and the first poll comes back over a new
// connection, with nothing failed. The first tick sees the connection
// change, the next looks first, and history learns the new map before any
// question on it reads the old map's rows.
func TestARestartRightAfterGreetIsSeen(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick, companion.mapID, companion.conn = 4_000_300, "M1", 1
	r := newTestRunner(t, companion)
	ingestAll(t, r.store, firstMap...)
	ctx := context.Background()
	r.greet(ctx)

	companion.restart(600, map[int64]string{1: "ping"})
	companion.mapID = "M2"
	r.tick(ctx)
	r.tick(ctx)
	if got := heard(t, r.store, "team-4", true); strings.Contains(got, "OLD") {
		t.Errorf("LEAK: two ticks after a restart onto M2, team-4 still reads M1's rows:\n%s", got)
	}
	if !strings.Contains(log.String(), "history: a new map from the companion's status (map M2") {
		t.Errorf("the new map was never noted:\n%s", log.String())
	}
}
