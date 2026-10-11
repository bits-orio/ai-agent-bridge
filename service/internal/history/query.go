package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// eventRow is one events row as read out of SQLite, with no row id: nothing
// the model reads may carry one (tools.go, eventHeader). data stays raw JSON
// text: recent_events hands it to the model with only a question's or an
// answer's qid taken out (modelData), and catch_up's detailOf is the only
// other reader that looks inside it. Private is the row's own private
// column: a row read back at all is one the question may see (visibleTo), so
// saying it was private tells nobody anything they could not read.
type eventRow struct {
	Tick    int64
	Event   string
	Player  string
	Force   string
	Data    json.RawMessage
	Private bool
}

// eventQuery selects rows from the events table, newest first (ORDER BY id
// DESC, which is also tick order: the tailer inserts in file order). Event
// and Events are never both set: Event is an exact match on one key,
// Events, catch_up's own use, matches any of several keys at once. Force,
// Player and SinceTick are left out of the WHERE clause when zero-valued.
// Limit bounds how many rows come back; 0 means no bound.
type eventQuery struct {
	Event     string
	Events    []string
	Force     string
	Player    string
	SinceTick int64
	Limit     int
}

// queryEvents reads the rows the question on ctx may see (visibleTo): the
// current map epoch's, and of those only the public ones and its own
// channel's. The condition leads the WHERE clause whatever the query asks
// for.
func (s *Store) queryEvents(ctx context.Context, q eventQuery) ([]eventRow, error) {
	visible, params := visibleTo(ctx)
	query := "SELECT tick, event, player, force, data, private FROM events WHERE " + visible
	switch {
	case q.Event != "":
		query += " AND event = ?"
		params = append(params, q.Event)
	case len(q.Events) > 0:
		placeholders := make([]string, len(q.Events))
		for i, e := range q.Events {
			placeholders[i] = "?"
			params = append(params, e)
		}
		query += " AND event IN (" + strings.Join(placeholders, ",") + ")"
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
	query += " ORDER BY id DESC"
	if q.Limit > 0 {
		query += " LIMIT ?"
		params = append(params, q.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	out := []eventRow{}
	for rows.Next() {
		r, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return out, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanEventRow(row rowScanner) (eventRow, error) {
	var r eventRow
	var data string
	var private sql.NullInt64
	if err := row.Scan(&r.Tick, &r.Event, &r.Player, &r.Force, &data, &private); err != nil {
		return eventRow{}, err
	}
	r.Data = json.RawMessage(data)
	r.Private = !private.Valid || private.Int64 != 0 // never NULL once read (visibleTo); private if it were
	return r, nil
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

// clampLimit applies the family's shared default-10-max-20 rule
// (recent_events, recent_chat) to a caller-supplied limit.
func clampLimit(n int) int {
	if n <= 0 {
		return defaultRecentLimit
	}
	if n > maxRecentLimit {
		return maxRecentLimit
	}
	return n
}
