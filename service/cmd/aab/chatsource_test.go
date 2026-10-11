// historyChat's own filtering (docs/design/phase4-spec.md section 15): drop
// "Server" lines, drop a line that is itself a chat-prefix question to the
// bot, collapse immediate repeats, oldest first. The fixture's player lines
// are public, logged the way a companion after 1.0.5 logs them, so a read
// with no scope sees them; chatsource_privacy_test.go covers private ones.

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
)

func newTestChat(t *testing.T) agent.ChatSource {
	t.Helper()
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lines := []string{
		`{"event":"console_chat","tick":100,"data":{"player":"Bob","force":"player","message":"hello","scope":"global","private":false}}`,
		// A server console line: never a player talking.
		`{"event":"console_chat","tick":101,"data":{"player":"Server","message":"[MTS] milestone"}}`,
		// A chat-prefix question and the "question" event the trigger logs
		// at the same tick, for the same player: the console_chat echo of it
		// must be dropped without the service ever learning the prefix.
		`{"event":"console_chat","tick":102,"data":{"player":"Bob","force":"player","message":"?what time is it","scope":"global","private":false}}`,
		`{"event":"question","tick":102,"data":{"qid":7,"player":"Bob","force":"player","text":"what time is it","scope":"global","private":false}}`,
		// An immediate repeat: collapses to one line.
		`{"event":"console_chat","tick":103,"data":{"player":"Bob","force":"player","message":"same line","scope":"global","private":false}}`,
		`{"event":"console_chat","tick":104,"data":{"player":"Bob","force":"player","message":"same line","scope":"global","private":false}}`,
		`{"event":"console_chat","tick":105,"data":{"player":"Alice","force":"player","message":"hi all","scope":"global","private":false}}`,
	}
	for _, line := range lines {
		if err := store.Ingest([]byte(line)); err != nil {
			t.Fatalf("ingest %q: %v", line, err)
		}
	}
	return newHistoryChat(store)
}

func TestHistoryChatFiltersServerBotQuestionsAndRepeats(t *testing.T) {
	chat := newTestChat(t)

	got := chat.RecentChat(context.Background(), 10)
	want := []agent.ChatLine{
		{Who: "Bob", Force: "player", Msg: "hello"},
		{Who: "Bob", Force: "player", Msg: "same line"},
		{Who: "Alice", Force: "player", Msg: "hi all"},
	}
	if len(got) != len(want) {
		t.Fatalf("RecentChat(10) = %+v, want %+v", got, want)
	}
	for i, line := range want {
		if got[i] != line {
			t.Errorf("line %d = %+v, want %+v", i, got[i], line)
		}
	}
}

// The n cap keeps the newest lines, oldest first, after filtering.
func TestHistoryChatRecentChatCapsToNewest(t *testing.T) {
	chat := newTestChat(t)

	got := chat.RecentChat(context.Background(), 2)
	want := []agent.ChatLine{
		{Who: "Bob", Force: "player", Msg: "same line"},
		{Who: "Alice", Force: "player", Msg: "hi all"},
	}
	if len(got) != len(want) {
		t.Fatalf("RecentChat(2) = %+v, want %+v", got, want)
	}
	for i, line := range want {
		if got[i] != line {
			t.Errorf("line %d = %+v, want %+v", i, got[i], line)
		}
	}
}

// A nil ChatSource (no recent_events tool found) and n<=0 both return no
// lines rather than panicking, the same "simply omit ch" contract every
// other Group A source keeps.
func TestHistoryChatNilAndZeroN(t *testing.T) {
	var nilChat *historyChat
	if lines := nilChat.RecentChat(context.Background(), 5); lines != nil {
		t.Errorf("a nil historyChat should return no lines, got %+v", lines)
	}
	chat := newTestChat(t)
	if lines := chat.RecentChat(context.Background(), 0); lines != nil {
		t.Errorf("RecentChat(0) should return no lines, got %+v", lines)
	}
}

// The briefing's chat names each speaker's force and marks a line said in a
// private channel, read through the adapter from the store itself.
func TestHistoryChatCarriesTheSpeakersForceAndPrivateLines(t *testing.T) {
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, line := range []string{
		`{"event":"console_chat","tick":5000,"data":{"player":"Pirion","force":"team-6","message":"we need more iron","scope":"team-6","private":true}}`,
		`{"event":"console_chat","tick":5100,"data":{"player":"Trophium","force":"team-2","message":"gg all","scope":"global","private":false}}`,
	} {
		if err := store.Ingest([]byte(line)); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	ctx := chatscope.With(context.Background(), chatscope.Scope{Key: "team-6", Private: true})
	got := newHistoryChat(store).RecentChat(ctx, 5)
	want := []agent.ChatLine{
		{Who: "Pirion", Force: "team-6", Private: true, Msg: "we need more iron"},
		{Who: "Trophium", Force: "team-2", Msg: "gg all"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("RecentChat = %+v, want %+v", got, want)
	}
}
