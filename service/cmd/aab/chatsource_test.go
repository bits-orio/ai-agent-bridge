// historyChat's own filtering (docs/design/phase4-spec.md section 15): drop
// "Server" lines, drop a line that is itself a chat-prefix question to the
// bot, collapse immediate repeats, oldest first.

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
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
		`{"event":"console_chat","tick":100,"data":{"player":"Bob","force":"player","message":"hello"}}`,
		// A server console line: never a player talking.
		`{"event":"console_chat","tick":101,"data":{"player":"Server","message":"[MTS] milestone"}}`,
		// A chat-prefix question and the "question" event the trigger logs
		// at the same tick, for the same player: the console_chat echo of it
		// must be dropped without the service ever learning the prefix.
		`{"event":"console_chat","tick":102,"data":{"player":"Bob","force":"player","message":"?what time is it"}}`,
		`{"event":"question","tick":102,"data":{"qid":7,"player":"Bob","force":"player","text":"what time is it","scope":"global","private":false}}`,
		// An immediate repeat: collapses to one line.
		`{"event":"console_chat","tick":103,"data":{"player":"Bob","force":"player","message":"same line"}}`,
		`{"event":"console_chat","tick":104,"data":{"player":"Bob","force":"player","message":"same line"}}`,
		`{"event":"console_chat","tick":105,"data":{"player":"Alice","force":"player","message":"hi all"}}`,
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
		{Who: "Bob", Msg: "hello"},
		{Who: "Bob", Msg: "same line"},
		{Who: "Alice", Msg: "hi all"},
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
		{Who: "Bob", Msg: "same line"},
		{Who: "Alice", Msg: "hi all"},
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
