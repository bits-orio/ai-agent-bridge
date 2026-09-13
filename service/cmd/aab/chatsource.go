// The briefing's chat source (agent.ChatSource): the last few organic chat
// lines, read out of the history store the same way the model's own history
// tools already do (docs/design/phase4-spec.md section 15).
//
// This goes through the store's own recent_events tool rather than a new
// exported Store method or a query of its own, so no change is needed in
// internal/history (owned elsewhere this pass) to get there.

package main

import (
	"context"
	"encoding/json"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
)

// chatFetchLimit is how many raw console_chat (and, separately, question)
// rows historyChat reads before filtering: recent_events' own ceiling
// (internal/history/tools.go maxRecentLimit), generous enough that dropping
// "Server" lines, the bot's own triggered questions and immediate repeats
// still leaves a handful of organic lines standing.
const chatFetchLimit = 20

// historyChat adapts the history store into agent.ChatSource.
type historyChat struct {
	recentEvents func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// newHistoryChat finds the store's own recent_events tool. A store that ever
// stopped offering one (it always does today) yields a nil ChatSource, which
// Assemble already treats as "omit ch", the same as any other Group A source
// that did not land.
func newHistoryChat(store *history.Store) agent.ChatSource {
	for _, t := range store.Tools() {
		if t.Name == "recent_events" {
			return &historyChat{recentEvents: t.Call}
		}
	}
	return nil
}

// historyEventRow mirrors recent_events' own reply shape
// (internal/history/tools.go's eventRow): id, tick, event, player, force,
// data, player and data used here.
type historyEventRow struct {
	ID     int64           `json:"id"`
	Tick   int64           `json:"tick"`
	Player string          `json:"player,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// consoleChatData is a console_chat row's own data field
// (companion-mod/scripts/events.lua's M.on_console_chat).
type consoleChatData struct {
	Message string `json:"message"`
}

// RecentChat is agent.ChatSource: the last n organic chat lines, oldest
// first. "Organic" excludes three things a raw console_chat read would
// otherwise carry (docs/design/phase4-spec.md section 15):
//
//   - the server's own console lines, recorded with player "Server"
//     (companion-mod/scripts/events.lua line 52);
//   - a line that is itself a chat-prefix question to the bot. The service
//     never learns the operator's configured aab-chat-prefix, and does not
//     need to: the companion logs a "question" event at the same tick and
//     for the same player as the console_chat line the chat-prefix trigger
//     fired from (companion-mod/scripts/questions.lua via chat.lua), so a
//     console_chat row sharing a (tick, player) with a recorded question is
//     dropped as that question's own echo, whatever prefix produced it;
//   - an immediate repeat of the line before it, once the two drops above
//     have already been applied.
//
// ctx is the one Assemble already built its own budget's deadline into
// (agent.ChatSource's own contract, briefing.go) and is used as is, for both
// trips below: the history store's connection is shared with the event
// tailer, so a read that hangs on it must still be able to unwind when the
// briefing's budget runs out, rather than hang the question on a
// context.Background() that never cancels.
func (h *historyChat) RecentChat(ctx context.Context, n int) []agent.ChatLine {
	if h == nil || n <= 0 {
		return nil
	}
	chatRows, ok := h.recentEventRows(ctx, "console_chat")
	if !ok {
		return nil
	}
	asked, _ := h.recentEventRows(ctx, "question")
	askedAt := make(map[chatMoment]bool, len(asked))
	for _, q := range asked {
		askedAt[chatMoment{tick: q.Tick, player: q.Player}] = true
	}

	// chatRows arrives newest first (recent_events' own ORDER BY id DESC);
	// walk it back to front to build the oldest-first line the ChatSource
	// contract promises, collapsing an immediate repeat as we go.
	lines := make([]agent.ChatLine, 0, n)
	for i := len(chatRows) - 1; i >= 0; i-- {
		row := chatRows[i]
		if row.Player == "" || row.Player == "Server" {
			continue
		}
		if askedAt[chatMoment{tick: row.Tick, player: row.Player}] {
			continue
		}
		var data consoleChatData
		if json.Unmarshal(row.Data, &data) != nil || data.Message == "" {
			continue
		}
		if last := len(lines) - 1; last >= 0 && lines[last].Who == row.Player && lines[last].Msg == data.Message {
			continue
		}
		lines = append(lines, agent.ChatLine{Who: row.Player, Msg: data.Message})
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// chatMoment is what ties a console_chat row to the "question" event its own
// chat-prefix trigger logged in the same handler call, hence the same tick.
type chatMoment struct {
	tick   int64
	player string
}

// recentEventRows calls the store's recent_events tool for one event key and
// decodes its reply. The second return is false on any failure (the tool
// erroring, or a reply that will not parse), the same best-effort contract
// every other briefing source keeps: a source that did not land is left out,
// never a reason to fail the question.
func (h *historyChat) recentEventRows(ctx context.Context, event string) ([]historyEventRow, bool) {
	args, err := json.Marshal(map[string]any{"event": event, "limit": chatFetchLimit})
	if err != nil {
		return nil, false
	}
	raw, err := h.recentEvents(ctx, args)
	if err != nil {
		return nil, false
	}
	var rows []historyEventRow
	if json.Unmarshal(raw, &rows) != nil {
		return nil, false
	}
	return rows, true
}
