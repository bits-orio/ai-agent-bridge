package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// ChatLine is one organic console_chat line, already filtered the way
// recent_chat and catch_up both need it (docs/design/phase4-spec.md
// section 15): no server console lines, no line that is itself a question
// to the bot, no immediate repeat of the line before it. Force is the
// speaker's, and Private says the line was said in a private channel, a
// force's own chat for instance. Only a question asked in that same channel
// ever reads a private line (visibleTo), so the flag tells the reader nothing
// it could not already read; it tells a team's own chat from the open one.
type ChatLine struct {
	Tick    int64
	Player  string
	Force   string
	Private bool
	Message string
}

// consoleChatData is a console_chat row's own data field
// (companion-mod/scripts/events.lua's M.on_console_chat).
type consoleChatData struct {
	Message string `json:"message"`
}

// chatMoment ties a console_chat row to the "question" event its own
// chat-prefix trigger logs at the same tick for the same player
// (companion-mod/scripts/questions.lua via chat.lua).
type chatMoment struct {
	tick   int64
	player string
}

// chatFetchLimit is how many raw console_chat (and, separately, question)
// rows FilteredChat reads before filtering: generous headroom over
// maxRecentLimit so dropping "Server" lines, the bot's own triggered
// questions and immediate repeats still leaves a full page of organic
// lines standing.
const chatFetchLimit = 4 * maxRecentLimit

const chatHeader = "tick\tplayer\tforce\tprivate\tmessage"

// FilteredChat returns organic console_chat lines, newest first (section
// 15): "Server" lines dropped, a line that is itself an echo of a
// chat-prefix question dropped (matched by (tick, player) against the
// "question" event the trigger logs at the same moment, whatever prefix
// produced it, so this package never needs to learn it), and an immediate
// repeat of the line before it collapsed to one.
//
// This is the one filter recent_chat, catch_up and the briefing's chat
// (RecentChatLines) all read through. Both of its reads go through
// queryEvents, so it only ever sees the rows the question on ctx may see:
// a private channel's lines and questions reach it only for a question
// asked in that same channel (visibility.go).
//
// sinceTick floors the raw read at that tick when positive (catch_up's own
// use); 0 means no floor. fetchLimit bounds how many raw console_chat rows
// are read before filtering.
func (s *Store) FilteredChat(ctx context.Context, sinceTick int64, fetchLimit int) ([]ChatLine, error) {
	chatRows, err := s.queryEvents(ctx, eventQuery{Event: "console_chat", SinceTick: sinceTick, Limit: fetchLimit})
	if err != nil {
		return nil, fmt.Errorf("chat: %w", err)
	}
	questionRows, err := s.queryEvents(ctx, eventQuery{Event: "question", SinceTick: sinceTick, Limit: fetchLimit})
	if err != nil {
		return nil, fmt.Errorf("chat: %w", err)
	}
	askedAt := make(map[chatMoment]bool, len(questionRows))
	for _, q := range questionRows {
		askedAt[chatMoment{tick: q.Tick, player: q.Player}] = true
	}

	// chatRows arrives newest first; a run of identical adjacent-in-time
	// lines collapses to one the same way whichever direction it is
	// walked, so no reversal is needed to collapse repeats correctly.
	out := make([]ChatLine, 0, len(chatRows))
	for _, row := range chatRows {
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
		if last := len(out) - 1; last >= 0 && out[last].Player == row.Player && out[last].Message == data.Message {
			continue
		}
		out = append(out, ChatLine{Tick: row.Tick, Player: row.Player, Force: row.Force, Private: row.Private, Message: data.Message})
	}
	return out, nil
}

// RecentChatLines is the briefing's view of the same filtered chat the
// recent_chat tool serves: at most n lines, oldest first, which is the order
// the briefing's `ch` key carries them in. It exists so nothing outside this
// package has to know the fetch depth the filter needs, or reimplement the
// filter itself. An error reads as no chat, because the briefing is best
// effort and a missing `ch` is the documented way to say a source did not
// land. ctx must carry the question's scope, as every read here does.
func (s *Store) RecentChatLines(ctx context.Context, n int) []ChatLine {
	if s == nil || n <= 0 {
		return nil
	}
	lines, err := s.FilteredChat(ctx, 0, chatFetchLimit)
	if err != nil || len(lines) == 0 {
		return nil
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	// FilteredChat hands back newest first; the briefing reads oldest first.
	out := make([]ChatLine, len(lines))
	for i, line := range lines {
		out[len(lines)-1-i] = line
	}
	return out
}

// recentChat is the recent_chat tool: the last `limit` organic chat lines,
// newest first, header line then one tab-separated row per line (tick,
// player, the speaker's force, whether the line was private, message).
func (s *Store) recentChat(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Limit int `json:"limit"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("recent_chat: %w", err)
	}
	limit := clampLimit(in.Limit)

	lines, err := s.FilteredChat(ctx, 0, chatFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("recent_chat: %w", err)
	}
	if len(lines) > limit {
		lines = lines[:limit] // already newest first: keep the newest `limit`
	}

	rows := make([]string, len(lines))
	for i, l := range lines {
		rows[i] = tsvRow(strconv.FormatInt(l.Tick, 10), l.Player, l.Force, yesNo(l.Private), l.Message)
	}
	return json.RawMessage(columnar(chatHeader, rows)), nil
}

// yesNo is a flag as a cell of these tools' rows.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
