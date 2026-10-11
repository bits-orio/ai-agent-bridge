package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

const defaultRecentLimit = 10
const maxRecentLimit = 20

// eventHeader is the row shape recent_events and last_event share. It has no
// row id: ids count up across every channel, so the gaps between the rows a
// question may see would count another channel's private rows.
const eventHeader = "tick\tevent\tplayer\tforce\tdata"

// currentMapOnly ends every history tool's description.
const currentMapOnly = " History covers the current map only."

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
			Description: "Recorded history events, newest first: one header line (tick, event, player, force, data) then one tab-separated row per event; data is that event's own JSON payload. " +
				"Filter by event, force or player, and by since_tick for a window. At most 20 rows; use count_events for a total. " +
				"A chat line or question said in a private channel shows up only when this question was asked in that same channel." + currentMapOnly,
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. player_died or research_finished. Omit for every key.",
				},
				"since_tick": map[string]any{
					"type":        "integer",
					"description": "Only events at or after this tick.",
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
				"One header line (tick, event, player, force, data) then the one matching row, or the plain line \"none recorded\" when there is none. " +
				"A private channel's rows show only for that channel's own questions." + currentMapOnly,
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
				"from since_tick on. One header line \"count\" then one row holding the number. Use it for totals instead of listing rows. " +
				"A private channel's rows count only for that channel's own questions." + currentMapOnly,
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
			Description: "The last N organic console chat lines, newest first: one header line (tick, player, force, private, message) then one tab-separated row per line; force is the speaker's, and private says yes for a line said in a private channel, a force's own chat for instance. " +
				"Server console lines and a player's own questions to this bot are already dropped, and an immediate repeat of the line before it collapses to one. Default limit 10, at most 20. " +
				"A line said in a private channel shows up only when this question was asked in that same channel, so a short or empty list can mean the rest was private, not that nobody spoke." + currentMapOnly,
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
			Description: "What happened on the server, not just on one force: research finished, rockets launched, deaths, joins and leaves, plus the same filtered chat recent_chat reads. " +
				"The window runs from since_tick when given (what happened in the last hour: the current tick less 216000), otherwise from that player's last recorded departure, what they missed while away; capped at 24 hours either way. " +
				"One header line (tick, event, player, force, private, detail) then up to 15 tab-separated rows, newest first, merged by tick; force is the force the row is about, the speaker's for a chat line, and private says yes for a chat line said in a private channel. " +
				"A player with no record at all gets one row saying so instead of an error. " +
				"Its chat follows recent_chat's rule: a private channel's lines show up only when this question was asked in that same channel." + currentMapOnly,
			Schema: tools.ObjectSchema(map[string]any{
				"player": map[string]any{
					"type":        "string",
					"description": "Player name: the window starts at their last departure. Not needed with since_tick.",
				},
				"since_tick": map[string]any{
					"type":        "integer",
					"description": "Start of the window as a tick; wins over the player's departure when both are given.",
				},
			}),
			Call: s.catchUp,
		},
	}
}

func (s *Store) recentEvents(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event     string `json:"event"`
		Force     string `json:"force"`
		Player    string `json:"player"`
		SinceTick int64  `json:"since_tick"`
		Limit     int    `json:"limit"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}

	rows, err := s.queryEvents(ctx, eventQuery{
		Event: in.Event, Force: in.Force, Player: in.Player, SinceTick: in.SinceTick, Limit: clampLimit(in.Limit),
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

	// Only the rows this question may read are counted: a total over hidden
	// rows would tell another force how much a private channel said.
	visible, params := visibleTo(ctx)
	query := "SELECT COUNT(*) FROM events WHERE " + visible + " AND event = ?"
	params = append(params, in.Event)
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

// eventRowsToTSV renders recent_events/last_event's shared row shape: tick,
// event, player, force, data, tab-joined in that order.
func eventRowsToTSV(rows []eventRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = tsvRow(
			strconv.FormatInt(r.Tick, 10),
			r.Event,
			r.Player,
			r.Force,
			modelData(r.Event, r.Data),
		)
	}
	return out
}

// modelData is a row's data as the model reads it. A question's or an
// answer's qid stays in the file and out of the reply, for the same reason
// row ids do: question ids count up across every channel. A session row's
// last_id, how many questions had been issued (Store.NoteMap), counts them
// the same way and stays out too. Data that does not parse, which Ingest
// never stores, reads as empty rather than raw.
func modelData(event string, data json.RawMessage) string {
	hidden := countingField(event)
	if hidden == "" {
		return string(data)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return "{}"
	}
	if _, ok := fields[hidden]; !ok {
		return string(data)
	}
	delete(fields, hidden)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // a question's text reads as the player typed it
	if enc.Encode(fields) != nil {
		return "{}"
	}
	return strings.TrimSpace(b.String())
}

// countingField is the field of a row's data that counts questions across
// every channel, "" for a row that has none.
func countingField(event string) string {
	switch event {
	case questionEvent, "answer":
		return "qid"
	case sessionEvent:
		return "last_id"
	}
	return ""
}
