package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

const defaultRecentLimit = 10
const maxRecentLimit = 20

const eventHeader = "id\ttick\tevent\tplayer\tforce\tdata"

// Tools returns the service's five list tools (docs/design/phase4-spec.md
// sections 13 and 15): recent_events, last_event, count_events, recent_chat
// and catch_up. None sweeps an axis, so none carries the sweep envelope a
// tier 1 sweep tool does; each returns a header line plus tab-separated
// rows instead (section 13). Every one is bound to this Store and safe to
// hand to the agent loop as-is.
func (s *Store) Tools() []tools.Tool {
	return []tools.Tool{
		{
			Name: "recent_events",
			Description: "Recorded history events, newest first: one header line (id, tick, event, player, force, data) then one tab-separated row per event; data is that event's own JSON payload. " +
				"Filter by event, force or player. At most 20 rows; use count_events for a total.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. player_died or research_finished. Omit for every key.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Force name. Omit for the asker's force.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Player name. Omit for every player.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Rows to return, default 10, at most 20.",
					"minimum":     1,
					"maximum":     maxRecentLimit,
				},
			}),
			Call: s.recentEvents,
		},
		{
			Name: "last_event",
			Description: "The newest recorded occurrence of one event key, optionally for one player or force: when a player last died, when research last finished. " +
				"One header line (id, tick, event, player, force, data) then the one matching row, or the plain line \"none recorded\" when there is none.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. player_died.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Player name.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Force name. Omit for the asker's force.",
				},
			}, "event"),
			Call: s.lastEvent,
		},
		{
			Name: "count_events",
			Description: "How many times one event key was recorded, optionally for one force and only " +
				"from since_tick on. One header line \"count\" then one row holding the number. Use it for totals instead of listing rows.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. rocket_launched.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Force name. Omit for the asker's force.",
				},
				"since_tick": map[string]any{
					"type":        "integer",
					"description": "Count only events at or after this tick.",
				},
			}, "event"),
			Call: s.countEvents,
		},
		{
			Name: "recent_chat",
			Description: "The last N organic console chat lines, newest first: one header line (tick, player, message) then one tab-separated row per line. " +
				"Server console lines and a player's own questions to this bot are already dropped, and an immediate repeat of the line before it collapses to one. Default limit 10, at most 20.",
			Schema: tools.ObjectSchema(map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Rows to return, default 10, at most 20.",
					"minimum":     1,
					"maximum":     maxRecentLimit,
				},
			}),
			Call: s.recentChat,
		},
		{
			Name: "catch_up",
			Description: "What a player missed while away, server-wide, not just their own force: research finished, rockets launched, deaths, joins and leaves, plus the same filtered chat recent_chat reads. " +
				"The window runs from that player's last recorded departure, capped at 24 hours. One header line (tick, event, player, detail) then up to 15 tab-separated rows, newest first, merged by tick. " +
				"A player with no record at all gets one row saying so instead of an error.",
			Schema: tools.ObjectSchema(map[string]any{
				"player": map[string]any{
					"type":        "string",
					"description": "Player name.",
				},
			}, "player"),
			Call: s.catchUp,
		},
	}
}

func (s *Store) recentEvents(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event  string `json:"event"`
		Force  string `json:"force"`
		Player string `json:"player"`
		Limit  int    `json:"limit"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}

	rows, err := s.queryEvents(ctx, eventQuery{
		Event: in.Event, Force: in.Force, Player: in.Player, Limit: clampLimit(in.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}
	return json.RawMessage(columnar(eventHeader, eventRowsToTSV(rows))), nil
}

func (s *Store) lastEvent(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event  string `json:"event"`
		Player string `json:"player"`
		Force  string `json:"force"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("last_event: %w", err)
	}
	if in.Event == "" {
		return nil, errors.New(`last_event: "event" is required`)
	}

	rows, err := s.queryEvents(ctx, eventQuery{Event: in.Event, Force: in.Force, Player: in.Player, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("last_event: %w", err)
	}
	if len(rows) == 0 {
		return json.RawMessage("none recorded"), nil
	}
	return json.RawMessage(columnar(eventHeader, eventRowsToTSV(rows))), nil
}

func (s *Store) countEvents(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event     string `json:"event"`
		Force     string `json:"force"`
		SinceTick *int64 `json:"since_tick"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("count_events: %w", err)
	}
	if in.Event == "" {
		return nil, errors.New(`count_events: "event" is required`)
	}

	query := "SELECT COUNT(*) FROM events WHERE event = ?"
	params := []any{in.Event}
	if in.Force != "" {
		query += " AND force = ?"
		params = append(params, in.Force)
	}
	if in.SinceTick != nil {
		query += " AND tick >= ?"
		params = append(params, *in.SinceTick)
	}

	var count int64
	if err := s.db.QueryRowContext(ctx, query, params...).Scan(&count); err != nil {
		return nil, fmt.Errorf("count_events: query: %w", err)
	}
	return json.RawMessage(columnar("count", []string{strconv.FormatInt(count, 10)})), nil
}

// eventRowsToTSV renders recent_events/last_event's shared row shape: id,
// tick, event, player, force, data, tab-joined in that order.
func eventRowsToTSV(rows []eventRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = tsvRow(
			strconv.FormatInt(r.ID, 10),
			strconv.FormatInt(r.Tick, 10),
			r.Event,
			r.Player,
			r.Force,
			string(r.Data),
		)
	}
	return out
}
