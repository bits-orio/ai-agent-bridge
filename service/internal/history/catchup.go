package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// catchUpEvents is what catch_up folds in beside chat
// (companion-mod/scripts/events.lua): everything a player away from the
// server would want to know happened, server-wide, not scoped to their own
// force (docs/design/phase4-spec.md section 15).
var catchUpEvents = []string{"research_finished", "rocket_launched", "player_died", "player_joined", "player_left"}

// ticksPerHour matches the engine's own nominal rate
// (companion-mod/scripts/tools/game_time.lua TICKS_PER_HOUR): 60 ticks a
// second, 3600 seconds an hour.
const ticksPerHour = 216000

// catchUpWindowTicks is catch_up's 24-hour cap, in ticks.
const catchUpWindowTicks = 24 * ticksPerHour

// catchUpRowBudget is the merged, newest-first row cap: research finished,
// rockets, deaths, joins, leaves and chat all compete for the same 15 rows.
const catchUpRowBudget = 15

// catchUpRawFetchLimit bounds how many raw rows of each kind (the five
// event keys combined, and console_chat separately) are read for one
// catch_up before the merge and the cut to catchUpRowBudget: generous
// enough that a busy 24-hour window still has plenty to rank from.
const catchUpRawFetchLimit = 200

const catchUpHeader = "tick\tevent\tplayer\tdetail"

// catchUp is the catch_up tool. Pure SQLite, no RCON: docs/design/phase4-spec.md
// section 15 asks for the window to run from the player's engine-side
// `last_online`, a LuaPlayer field this package cannot read without a live
// call. The service-side equivalent that keeps catch_up free is used
// instead: the tick of that player's own newest recorded player_left row.
// Likewise "now" has no live tick to read here; the tick of the newest row
// this store has seen of any kind stands in for it, the same kind of
// local substitution.
func (s *Store) catchUp(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Player string `json:"player"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}
	if in.Player == "" {
		return nil, errors.New(`catch_up: "player" is required`)
	}

	seen, err := s.everSeen(ctx, in.Player)
	if err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}
	if !seen {
		row := tsvRow("", "none", in.Player, "no record of this player")
		return json.RawMessage(columnar(catchUpHeader, []string{row})), nil
	}

	nowTick, err := s.maxTick(ctx)
	if err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}
	windowStart := nowTick - catchUpWindowTicks
	left, err := s.lastPlayerLeftTick(ctx, in.Player)
	if err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}
	// A player seen only joining, chatting or dying, never yet left
	// (still connected, or the log never caught their departure), has no
	// better local marker than the 24-hour floor alone.
	if left > windowStart {
		windowStart = left
	}

	eventRows, err := s.queryEvents(ctx, eventQuery{Events: catchUpEvents, SinceTick: windowStart, Limit: catchUpRawFetchLimit})
	if err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}
	chatLines, err := s.FilteredChat(ctx, windowStart, catchUpRawFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("catch_up: %w", err)
	}

	merged := mergeCatchUp(eventRows, chatLines)
	if len(merged) > catchUpRowBudget {
		merged = merged[:catchUpRowBudget]
	}
	rows := make([]string, len(merged))
	for i, m := range merged {
		rows[i] = tsvRow(strconv.FormatInt(m.tick, 10), m.event, m.player, m.detail)
	}
	return json.RawMessage(columnar(catchUpHeader, rows)), nil
}

type catchUpRow struct {
	tick   int64
	order  int // stable tie-break for equal ticks: events first, then chat, each in its own newest-first order
	event  string
	player string
	detail string
}

// mergeCatchUp folds the five event kinds and the filtered chat lines into
// one newest-first list ("merged by tick", section 15). Both inputs arrive
// newest first already; sorting by tick descending with a stable tie-break
// keeps that within each tick.
func mergeCatchUp(eventRows []eventRow, chatLines []ChatLine) []catchUpRow {
	merged := make([]catchUpRow, 0, len(eventRows)+len(chatLines))
	for i, r := range eventRows {
		merged = append(merged, catchUpRow{
			tick: r.Tick, order: i, event: r.Event, player: r.Player, detail: detailOf(r.Event, r.Data),
		})
	}
	base := len(eventRows)
	for i, c := range chatLines {
		merged = append(merged, catchUpRow{
			tick: c.Tick, order: base + i, event: "console_chat", player: c.Player, detail: c.Message,
		})
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].tick != merged[j].tick {
			return merged[i].tick > merged[j].tick
		}
		return merged[i].order < merged[j].order
	})
	return merged
}

// detailOf builds catch_up's own "detail" column from one event's own data
// payload (companion-mod/scripts/events.lua): the one extra fact a player
// catching up would want beside who and when. player_joined and
// player_left need nothing more than that, so they fall to the default.
func detailOf(event string, data json.RawMessage) string {
	switch event {
	case "player_died":
		var d struct {
			Cause string `json:"cause"`
		}
		_ = json.Unmarshal(data, &d)
		return d.Cause
	case "research_finished":
		var d struct {
			Force string `json:"force"`
			Tech  string `json:"tech"`
			Level int    `json:"level"`
		}
		_ = json.Unmarshal(data, &d)
		return fmt.Sprintf("%s (force %s, level %d)", d.Tech, d.Force, d.Level)
	case "rocket_launched":
		var d struct {
			Force   string `json:"force"`
			Surface string `json:"surface"`
		}
		_ = json.Unmarshal(data, &d)
		return fmt.Sprintf("force %s, surface %s", d.Force, d.Surface)
	default:
		return ""
	}
}

func (s *Store) everSeen(ctx context.Context, player string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE player = ?", player).Scan(&n); err != nil {
		return false, fmt.Errorf("look up %q: %w", player, err)
	}
	return n > 0, nil
}

// maxTick is the tick of the newest row this store has seen, of any kind:
// the local stand-in for "now" (this function's own doc comment on
// catchUp explains why). 0 on a store with no rows yet.
func (s *Store) maxTick(ctx context.Context) (int64, error) {
	var t sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT MAX(tick) FROM events").Scan(&t); err != nil {
		return 0, fmt.Errorf("max tick: %w", err)
	}
	return t.Int64, nil
}

// lastPlayerLeftTick is the tick of player's own newest player_left row, 0
// if it has none.
func (s *Store) lastPlayerLeftTick(ctx context.Context, player string) (int64, error) {
	var t sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT MAX(tick) FROM events WHERE event = 'player_left' AND player = ?", player,
	).Scan(&t)
	if err != nil {
		return 0, fmt.Errorf("last player_left for %q: %w", player, err)
	}
	return t.Int64, nil
}
