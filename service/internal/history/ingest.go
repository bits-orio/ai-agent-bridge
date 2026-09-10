package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// eventLine is one line of events.jsonl (companion-mod/README.md
// "events.jsonl line shape"): {"event":"player_died","tick":1234,
// "data":{"player":"Bob","force":"player","cause":"biter"}}. data is whatever
// shape the writing event handler chose; player and force are pulled out
// when present so recent_events, last_event and count_events can filter on
// them without parsing data again.
type eventLine struct {
	Event string          `json:"event"`
	Tick  int64           `json:"tick"`
	Data  json.RawMessage `json:"data"`
}

type eventDataFields struct {
	Player string `json:"player"`
	Force  string `json:"force"`
}

// Ingest records one events.jsonl line. A blank (or whitespace-only) line is
// ignored and returns nil, matching the tailer that feeds this method. Any
// other line that is not the documented shape (invalid JSON, "data" present
// but not an object, or an empty "event") returns an error; the caller skips
// that line rather than aborting the tail.
func (s *Store) Ingest(line []byte) error {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}

	var e eventLine
	if err := json.Unmarshal(trimmed, &e); err != nil {
		return fmt.Errorf("history: malformed event line: %w", err)
	}
	if e.Event == "" {
		return fmt.Errorf(`history: malformed event line: missing "event"`)
	}

	dataText := "{}"
	var fields eventDataFields
	dataTrimmed := bytes.TrimSpace(e.Data)
	if len(dataTrimmed) > 0 && !bytes.Equal(dataTrimmed, []byte("null")) {
		if err := json.Unmarshal(dataTrimmed, &fields); err != nil {
			return fmt.Errorf("history: malformed event line: %q data: %w", e.Event, err)
		}
		dataText = string(dataTrimmed)
	}

	_, err := s.db.Exec(
		`INSERT INTO events (tick, event, player, force, data, received_at) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Tick, e.Event, fields.Player, fields.Force, dataText, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("history: insert %q: %w", e.Event, err)
	}
	return nil
}
