package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
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

// entry is one events row on its way in: data is the data object's JSON
// text, "{}" for a line that had none, and fields what was pulled out of it.
type entry struct {
	event  string
	tick   int64
	data   string
	fields eventDataFields
}

// Ingest records one events.jsonl line. A blank (or whitespace-only) line is
// ignored and returns nil, matching the tailer that feeds this method. Any
// other line that is not the documented shape (invalid JSON, "data" present
// but not an object, or an empty "event") returns an error; the caller skips
// that line rather than aborting the tail. Every row is stored with the map
// epoch it belongs to (epoch.go) and who may read it back (visibility.go),
// both decided once, here, from what the line says and the line before it.
func (s *Store) Ingest(line []byte) error {
	_, err := s.IngestRow(line)
	return err
}

// Ingested is what IngestRow made of one line: the row it stored, the epoch
// it went into, and whether every question may read it back. Public is false
// for a private row, and for one the guards (guard.go) held or quarantined:
// the line may be another map's, so nothing may speak from it.
type Ingested struct {
	ID     int64 // 0 for a blank line, which stores nothing
	Event  string
	Tick   int64
	Force  string
	Data   json.RawMessage
	Epoch  int64
	Public bool
}

// IngestRow is Ingest, reporting the row it stored.
func (s *Store) IngestRow(line []byte) (Ingested, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return Ingested{}, nil
	}
	r, err := parseLine(trimmed)
	if err != nil {
		return Ingested{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data := json.RawMessage(r.data)
	mode, at := s.lineMode(r.event, data, s.at.next(r.event, r.tick, data))
	return s.insert(r, at, mode)
}

// Mark is where history stood at one moment, taken before a status call
// goes out so the reply is judged against the file as it was then (NoteMap).
type Mark struct {
	at epochState
}

// Mark is where history stands now: the epoch, the tick of its newest line
// of the file and the highest question id it has logged.
func (s *Store) Mark() Mark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Mark{at: s.at}
}

// statusRow is the data of a session row NoteMap records.
type statusRow struct {
	MapID  string `json:"map_id"`
	Source string `json:"source"`
	LastID int64  `json:"last_id"`
}

// NoteMap records that the companion's status reply put the game on map
// mapID at tick, with lastID the highest question id it had issued, for when
// the file has not said so: the session line naming the map was written
// while the service was down, or never written, with events off. The row is
// a session row of the service's own, data {"map_id": mapID, "source":
// "status", "last_id": lastID}, placed, classified and stored the way Ingest
// stores a line (epoch.go says how it moves the epoch).
//
// before is the Mark taken before the status call went out, and the reply is
// judged against it: its file tick and its highest question id. The tailer
// runs beside the call, so a line it stored meanwhile was written after the
// status, at a higher tick, with any question in it issued after the status
// counted. Judged against that line, the status would read as a drop into a
// young map's first minutes, or as the companion having issued fewer
// questions than history logged, and start an epoch for nothing. When the
// file moved to another epoch meanwhile, nothing is recorded: the file has
// spoken more recently than this status, and the next look reads again.
//
// It is recorded only when it starts an epoch or names the current one, so
// every status read may call this: the map the current epoch already has
// records nothing, and so does an empty map id, from a companion too old to
// send one. It reports whether reads moved off an earlier epoch's rows: a
// new epoch on a store that had one.
func (s *Store) NoteMap(before Mark, mapID string, tick, lastID int64) (bool, error) {
	if mapID == "" {
		return false, nil
	}
	data, _ := json.Marshal(statusRow{MapID: mapID, Source: statusSource, LastID: lastID}) // strings and numbers always encode
	r := entry{event: sessionEvent, tick: tick, data: string(data)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at.epoch != before.at.epoch {
		return false, nil
	}
	// The map is the file's as it is now: within one epoch it only ever
	// gains a name.
	judged := before.at
	judged.mapID = s.at.mapID
	at := judged.next(r.event, r.tick, data)
	if at.epoch == s.at.epoch {
		// It joins the current epoch, whose file tick and questions may have
		// moved on since the mark: those stand, and the row can only give
		// the epoch a name it lacked.
		named := s.at
		named.mapID = at.mapID
		at = named
	}
	moved := s.at.epoch != 0 && at.epoch != s.at.epoch
	record, err := s.noteStatus(mapID, at) // guard.go: may release a held epoch instead
	if err != nil || !record {
		return false, err
	}
	_, err = s.insert(r, at, stored)
	return moved, err
}

// parseLine reads one non-blank events.jsonl line, or says why it is not the
// documented shape.
func parseLine(line []byte) (entry, error) {
	var e eventLine
	if err := json.Unmarshal(line, &e); err != nil {
		return entry{}, fmt.Errorf("history: malformed event line: %w", err)
	}
	if e.Event == "" {
		return entry{}, fmt.Errorf(`history: malformed event line: missing "event"`)
	}
	r := entry{event: e.Event, tick: e.Tick, data: "{}"}
	dataTrimmed := bytes.TrimSpace(e.Data)
	if len(dataTrimmed) > 0 && !bytes.Equal(dataTrimmed, []byte("null")) {
		if err := json.Unmarshal(dataTrimmed, &r.fields); err != nil {
			return entry{}, fmt.Errorf("history: malformed event line: %q data: %w", e.Event, err)
		}
		r.data = string(dataTrimmed)
	}
	return r, nil
}

// insert stores one row in the epoch `at` places it in, with who may read it
// back, and moves the store on to `at`. mode is what the guards (guard.go)
// made of it: a held row is stored with heldOffset on its private flag, and
// a quarantined one unreadable, in the current epoch, moving nothing. The
// caller holds s.mu.
func (s *Store) insert(r entry, at epochState, mode rowMode) (Ingested, error) {
	// Every row already stored is older than this one, so the bound on an
	// answer's question lookup is no bound at all.
	vis, err := rowVisibility(s.db, at.epoch, math.MaxInt64, r.event, r.fields.Force, json.RawMessage(r.data))
	if err != nil {
		return Ingested{}, fmt.Errorf("history: classify %q: %w", r.event, err)
	}
	flag := vis.flag()
	switch mode {
	case held:
		flag += heldOffset
	case quarantined:
		vis, flag, at = unreadable, unreadable.flag(), s.at
	}
	res, err := s.db.Exec(
		`INSERT INTO events (tick, event, player, force, data, received_at, scope, private, epoch) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.tick, r.event, r.fields.Player, r.fields.Force, r.data, time.Now().UTC().Format(time.RFC3339), vis.scope, flag, at.epoch,
	)
	if err != nil {
		return Ingested{}, fmt.Errorf("history: insert %q: %w", r.event, err)
	}
	s.at = at
	id, _ := res.LastInsertId()
	return Ingested{
		ID: id, Event: r.event, Tick: r.tick, Force: r.fields.Force, Data: json.RawMessage(r.data),
		Epoch: at.epoch, Public: flag == 0,
	}, nil
}
