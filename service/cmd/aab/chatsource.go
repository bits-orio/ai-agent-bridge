// The briefing's chat source: the last few organic chat lines, read from the
// service's own history rather than over RCON.
//
// The filter that decides what "organic" means lives in internal/history,
// beside the recent_chat and catch_up tools that need exactly the same thing.
// This file is only the adapter, and deliberately holds no filter of its own.
// The first version of it carried a copy, and the copy had already drifted
// from the original on how deep into raw history to read before filtering.

package main

import (
	"context"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
)

// historyChat adapts the store to the narrow interface the briefing asks for.
type historyChat struct {
	store *history.Store
}

// newHistoryChat wires the store in as the briefing's chat source. A nil store
// yields a nil ChatSource, which the briefing already treats as "omit ch", the
// same as any other source that did not land.
func newHistoryChat(store *history.Store) agent.ChatSource {
	if store == nil {
		return nil
	}
	return &historyChat{store: store}
}

// RecentChat returns at most n filtered chat lines, oldest first. It honours
// the context it is handed: the history database runs on one connection the
// event tailer also writes on, so an uncancellable read here could hold up a
// question.
func (h *historyChat) RecentChat(ctx context.Context, n int) []agent.ChatLine {
	if h == nil || h.store == nil || n <= 0 {
		return nil
	}
	lines := h.store.RecentChatLines(ctx, n)
	if len(lines) == 0 {
		return nil
	}
	out := make([]agent.ChatLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, agent.ChatLine{Who: line.Player, Msg: line.Message})
	}
	return out
}
