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

// knownKinds is every event kind the companion writes
// (companion-mod/scripts/events.lua, questions.lua), named in the
// descriptions so the model does not have to guess one.
const knownKinds = " Kinds recorded: player_joined, player_left, player_died, research_finished, rocket_launched, console_chat, question, answer, question_dropped, session; a kind not on the map yet answers with the kinds there are."

// ticksPerMinute converts since_minutes to ticks: 60 updates a second.
const ticksPerMinute = 60 * 60

// forceSchema is the force filter every event tool takes. It is optional
// and covers every force when left out: a filter the caller did not mean
// narrows a player's history to one team and answers 0.
var forceSchema = map[string]any{
	"type":        "string",
	"description": "Optional filter: only rows about this force. Leave it out to cover every force.",
}

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
				"Filter by event (one kind or several, comma-separated), force or player, and by since_minutes or since_tick for a window: what happened in the last hour is since_minutes 60 with no event. At most 20 rows; use count_events for a total. " +
				"A chat line or question said in a private channel shows up only when this question was asked in that same channel." + knownKinds + currentMapOnly,
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. player_died, or several comma-separated: player_died,research_finished. Omit for every key.",
				},
				"since_tick": map[string]any{
					"type":        "integer",
					"description": "Only events at or after this tick.",
				},
				"since_minutes": map[string]any{
					"type":        "integer",
					"description": "Only events from the last this many minutes of game time, counted back from the newest event.",
					"minimum":     1,
				},
				"force": forceSchema,
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
				"A private channel's rows show only for that channel's own questions." + knownKinds + currentMapOnly,
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. player_died.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Player name.",
				},
				"force": forceSchema,
			}, "event"),
			Call: s.lastEvent,
		},
		{
			Name: "count_events",
			Description: "How many times one event key was recorded, optionally for one player or force and only " +
				"from since_tick on: how many times a player logged off is player_left with that player. One header line \"count\" then one row holding the number. Use it for totals instead of listing rows. " +
				"A private channel's rows count only for that channel's own questions." + knownKinds + currentMapOnly,
			Schema: tools.ObjectSchema(map[string]any{
				"event": map[string]any{
					"type":        "string",
					"description": "Event key, e.g. rocket_launched.",
				},
				"player": map[string]any{
					"type":        "string",
					"description": "Player name. Omit for every player.",
				},
				"force": forceSchema,
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

// The force filter is never listed as required (forceSchema): the agent
// fills the asker's force in only for a tool that requires it
// (tools.Requires), so these tools see a force only when the model asked for
// one (F2). TODO(agent): should the agent come to need an explicit flag
// instead, it goes on the three event tools above.

func (s *Store) recentEvents(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event        string `json:"event"`
		Force        string `json:"force"`
		Player       string `json:"player"`
		SinceTick    int64  `json:"since_tick"`
		SinceMinutes int64  `json:"since_minutes"`
		Limit        int    `json:"limit"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}
	q := eventQuery{Force: in.Force, Player: in.Player, SinceTick: in.SinceTick, Limit: clampLimit(in.Limit)}
	kinds := splitKinds(in.Event)
	if len(kinds) == 1 {
		q.Event = kinds[0]
	} else {
		q.Events = kinds
	}
	if in.SinceMinutes > 0 {
		// The window runs back from the newest row this question may see,
		// the same "now" catch_up uses; the later of the two bounds wins.
		now, err := s.maxTick(ctx)
		if err != nil {
			return nil, fmt.Errorf("recent_events: %w", err)
		}
		q.SinceTick = max(q.SinceTick, now-in.SinceMinutes*ticksPerMinute, 1)
	}
	missing, err := s.unknownKinds(ctx, "recent_events", kinds)
	if err != nil {
		return nil, err
	}
	rows, err := s.queryEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("recent_events: %w", err)
	}
	out := columnar(eventHeader, eventRowsToTSV(rows))
	if len(missing) > 0 {
		out += "\nnote: no events of kind " + strings.Join(quoteAll(missing), ", ") + " are recorded on this map"
	}
	if len(rows) == 0 {
		out += s.forceNote(ctx, q)
	}
	return json.RawMessage(out), nil
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
	if _, err := s.unknownKinds(ctx, "last_event", []string{in.Event}); err != nil {
		return nil, err
	}
	q := eventQuery{Event: in.Event, Force: in.Force, Player: in.Player, Limit: 1}
	rows, err := s.queryEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("last_event: %w", err)
	}
	if len(rows) == 0 {
		return json.RawMessage("none recorded" + s.forceNote(ctx, q)), nil
	}
	return json.RawMessage(columnar(eventHeader, eventRowsToTSV(rows))), nil
}

func (s *Store) countEvents(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Event     string `json:"event"`
		Force     string `json:"force"`
		Player    string `json:"player"`
		SinceTick *int64 `json:"since_tick"`
	}
	if err := unmarshalArgs(args, &in); err != nil {
		return nil, fmt.Errorf("count_events: %w", err)
	}
	if in.Event == "" {
		return nil, errors.New(`count_events: "event" is required`)
	}
	if _, err := s.unknownKinds(ctx, "count_events", []string{in.Event}); err != nil {
		return nil, err
	}
	q := eventQuery{Event: in.Event, Force: in.Force, Player: in.Player}
	if in.SinceTick != nil {
		q.SinceTick = *in.SinceTick
	}
	count, err := s.countWhere(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("count_events: query: %w", err)
	}
	out := columnar("count", []string{strconv.FormatInt(count, 10)})
	if count == 0 {
		out += s.forceNote(ctx, q)
	}
	return json.RawMessage(out), nil
}

// countWhere counts the rows q matches that the question on ctx may read: a
// total over hidden rows would tell another force how much a private
// channel said. q.Limit plays no part. A since_tick of 0 or less bounds
// nothing, as in queryEvents.
func (s *Store) countWhere(ctx context.Context, q eventQuery) (int64, error) {
	visible, params := visibleTo(ctx)
	query := "SELECT COUNT(*) FROM events WHERE " + visible
	switch {
	case q.Event != "":
		query += " AND event = ?"
		params = append(params, q.Event)
	case len(q.Events) > 0:
		query += " AND event IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Events)), ",") + ")"
		for _, e := range q.Events {
			params = append(params, e)
		}
	}
	if q.Force != "" {
		query += " AND force = ?"
		params = append(params, q.Force)
	}
	if q.Player != "" {
		query += " AND player = ?"
		params = append(params, q.Player)
	}
	if q.SinceTick > 0 {
		query += " AND tick >= ?"
		params = append(params, q.SinceTick)
	}
	var count int64
	err := s.db.QueryRowContext(ctx, query, params...).Scan(&count)
	return count, err
}

// forceNote is what a filtered read that came back empty adds when the
// force filter alone hid the player's rows: the count without it, so the
// model reads "none on that force" and not "none at all". "" otherwise, or
// when the count fails: the plain result still stands.
func (s *Store) forceNote(ctx context.Context, q eventQuery) string {
	if q.Force == "" || q.Player == "" {
		return ""
	}
	force := q.Force
	q.Force = ""
	n, err := s.countWhere(ctx, q)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("\nnote: 0 for force %q; %d for player %q across all forces (leave force out to see them)", force, n, q.Player)
}

// unknownKinds checks the requested kinds against the kinds the current map
// has rows of, counted over what the question on ctx may read. When none of
// them has a single row, the call answers with an error naming the kinds
// there are, so the model stops guessing names and never reads a made-up
// kind's 0 as a count. When only some are missing, they come back for a
// note beside the rows of the rest.
func (s *Store) unknownKinds(ctx context.Context, tool string, kinds []string) ([]string, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	counts, err := s.kindCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	var missing []string
	for _, k := range kinds {
		if !hasKind(counts, k) {
			missing = append(missing, k)
		}
	}
	if len(missing) < len(kinds) {
		return missing, nil
	}
	parts := make([]string, len(counts))
	for i, c := range counts {
		parts[i] = fmt.Sprintf("%s %d", c.kind, c.n)
	}
	there := "none yet"
	if len(parts) > 0 {
		there = strings.Join(parts, ", ")
	}
	return nil, fmt.Errorf("%s: no events of kind %s are recorded on this map; kinds: %s", tool, strings.Join(quoteAll(missing), ", "), there)
}

// kindCount is one event kind and how many rows of it a question may read.
type kindCount struct {
	kind string
	n    int64
}

// kindCounts is every kind the question on ctx may read rows of in the
// current epoch, commonest first.
func (s *Store) kindCounts(ctx context.Context) ([]kindCount, error) {
	visible, params := visibleTo(ctx)
	rows, err := s.db.QueryContext(ctx, "SELECT event, COUNT(*) AS n FROM events WHERE "+visible+" GROUP BY event ORDER BY n DESC, event", params...)
	if err != nil {
		return nil, fmt.Errorf("kinds: %w", err)
	}
	defer rows.Close()
	var out []kindCount
	for rows.Next() {
		var c kindCount
		if err := rows.Scan(&c.kind, &c.n); err != nil {
			return nil, fmt.Errorf("kinds: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func hasKind(counts []kindCount, kind string) bool {
	for _, c := range counts {
		if c.kind == kind {
			return true
		}
	}
	return false
}

// splitKinds reads recent_events' event argument: one kind, or several
// separated by commas, blanks dropped. None means every kind.
func splitKinds(v string) []string {
	var out []string
	for _, k := range strings.Split(v, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, v := range ss {
		out[i] = strconv.Quote(v)
	}
	return out
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
	case questionEvent, "answer", "question_dropped":
		return "qid"
	case sessionEvent:
		return "last_id"
	}
	return ""
}
