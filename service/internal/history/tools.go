package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// eventRow is one events row as returned to the model: compact JSON, data
// embedded as an object rather than a re-escaped string. Empty player and
// force are omitted rather than sent as "".
type eventRow struct {
	ID     int64           `json:"id"`
	Tick   int64           `json:"tick"`
	Event  string          `json:"event"`
	Player string          `json:"player,omitempty"`
	Force  string          `json:"force,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

const defaultRecentLimit = 10
const maxRecentLimit = 20

// Tools returns the three history tools (docs/design/phase1-2-spec.md
// "history"): recent_events, last_event and count_events. Each is bound to
// this Store and safe to hand to the agent loop as-is.
func (s *Store) Tools() []tools.Tool {
	return []tools.Tool{
		{
			Name: "recent_events",
			Description: "Returns the most recently recorded history events, newest first, each with " +
				"its tick, event key, player, force and data. Filter by event, force or player to " +
				"narrow the list; omit all three for the most recent events of any kind. Capped at " +
				"20 rows (10 by default); call count_events instead if you only need a total.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key to filter to, for example player_died or research_finished. Omitted matches every event key.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Force name to filter to. Omitted matches every force.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Player name to filter to. Omitted matches every player.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum rows to return, newest first. Defaults to 10, capped at 20.",
					"minimum":     1,
					"maximum":     maxRecentLimit,
				},
			}),
			Call: s.recentEvents,
		},
		{
			Name: "last_event",
			Description: "Returns the single newest recorded occurrence of one event key, optionally " +
				"restricted to a player or a force. Use this for questions like when a player last " +
				"died or when a force last finished research. Returns a clear none-recorded result " +
				"when the event has never been recorded for that filter.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key to find the newest occurrence of, for example player_died.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Restrict to this player's events.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Restrict to this force's events.",
				},
			}, "event"),
			Call: s.lastEvent,
		},
		{
			Name: "count_events",
			Description: "Counts how many times one event key has been recorded, optionally restricted " +
				"to a force and to ticks at or after since_tick. Use this for totals, such as how many " +
				"times a force has launched a rocket, rather than listing every matching row.",
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key to count, for example rocket_launched.",
				},
				"force": map[string]any{
					"type":        "string",
					"description": "Restrict the count to this force.",
				},
				"since_tick": map[string]any{
					"type":        "integer",
					"description": "Only count events recorded at or after this game tick.",
				},
			}, "event"),
			Call: s.countEvents,
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

	limit := in.Limit
	if limit <= 0 {
		limit = defaultRecentLimit
	}
	if limit > maxRecentLimit {
		limit = maxRecentLimit
	}

	query := "SELECT id, tick, event, player, force, data FROM events WHERE 1=1"
	var params []any
	if in.Event != "" {
		query += " AND event = ?"
		params = append(params, in.Event)
	}
	if in.Force != "" {
		query += " AND force = ?"
		params = append(params, in.Force)
	}
	if in.Player != "" {
		query += " AND player = ?"
		params = append(params, in.Player)
	}
	query += " ORDER BY id DESC LIMIT ?"
	params = append(params, limit)

	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("recent_events: query: %w", err)
	}
	defer rows.Close()

	out := []eventRow{}
	for rows.Next() {
		r, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("recent_events: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}

	return json.Marshal(out)
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

	query := "SELECT id, tick, event, player, force, data FROM events WHERE event = ?"
	params := []any{in.Event}
	if in.Player != "" {
		query += " AND player = ?"
		params = append(params, in.Player)
	}
	if in.Force != "" {
		query += " AND force = ?"
		params = append(params, in.Force)
	}
	query += " ORDER BY id DESC LIMIT 1"

	row := s.db.QueryRowContext(ctx, query, params...)
	r, err := scanEventRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return json.Marshal(map[string]string{"result": "none recorded"})
	}
	if err != nil {
		return nil, fmt.Errorf("last_event: scan: %w", err)
	}
	return json.Marshal(r)
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
	return json.Marshal(map[string]int64{"count": count})
}

// unmarshalArgs decodes tool arguments, treating a nil or empty payload as
// "no arguments given" rather than an error, since every field these tools
// take is optional except where checked separately.
func unmarshalArgs(args json.RawMessage, out any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, out); err != nil {
		return fmt.Errorf("bad arguments: %w", err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanEventRow(row rowScanner) (eventRow, error) {
	var r eventRow
	var data string
	if err := row.Scan(&r.ID, &r.Tick, &r.Event, &r.Player, &r.Force, &data); err != nil {
		return eventRow{}, err
	}
	r.Data = json.RawMessage(data)
	return r, nil
}
