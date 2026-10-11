package history

// Map epochs (CONTEXT.md "History"). One history file outlives the map it
// began on: the server starts a new map, or loads an old save, and the
// service keeps tailing into the same file. Every map names its forces and
// players the same way, so a row from an earlier map reads as this map's,
// and an earlier map's private chat as this map's under the same key. Every
// row therefore records its epoch, the run of rows one map logged, and every
// read is restricted to the current epoch, the newest row's (visibility.go).
//
// The epoch moves on in three ways:
//   - A companion that writes a session line, the first line of every game
//     session, names its map. A map other than the epoch's starts a new
//     epoch. The same map keeps it through a save loaded far back, but not
//     through a drop into a map's first five minutes. Two maps made from one
//     fixed seed carry the same id (companion-mod/scripts/map_id.lua), and a
//     map that young is a new one.
//   - Every other line is judged by its tick, the only signal an older
//     companion leaves. A drop into a new map's first five minutes, or a drop
//     of more than thirty minutes, starts a new epoch. A smaller drop is an
//     autosave loaded after a crash: the same map, so the same epoch.
//   - The companion's status reply names its map too. When the file has not
//     said so, because the session line was written while the service was
//     down, or never written, with events off, the service records a session
//     row of its own at the status's tick (Store.NoteMap). It moves the epoch
//     as that session line would have. It is no line of the file, though, so
//     no line is compared with its tick: the lines the tailer has yet to
//     deliver were written before it, lower, and would read as a drop. An
//     epoch it starts has no tick until a line of the file arrives, so the
//     session line it got ahead of joins that epoch. So would a line the
//     tailer still owes from before the map changed, should it come that
//     late: nothing in a line tells the two maps apart. The status is judged
//     against the file as it stood before the status call went out
//     (Store.Mark): a line the tailer stored while the call was out was
//     written after the status, higher, and would read as the status dropping
//     below it.
//
// A session row that says how many questions the companion has issued, its
// last_id, is no continuation of an epoch that has logged a higher question
// id: the count never goes back while one map goes on. The status row always
// says it; a session line says it once the companion writes it. So a map made
// from the same seed, carrying the same id, starts its own epoch however late
// the service first sees it, provided the map before it logged a higher
// question id than the new one has issued by then. A rollback to a save from
// before some logged questions starts one too, and its earlier rows go
// unread: fail closed, the price of telling the two apart with nothing else
// to go on.
//
// The first session line on an epoch an older companion logged names no map
// to compare with, so the tick decides: a map upgraded in place keeps its
// history and the epoch takes the map's name from then on.

import (
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	// newMapTicks is a new map's first five minutes: a tick under it, after
	// a later one, is a new map, whatever map id it carries.
	newMapTicks = 5 * 60 * 60
	// rollbackTicks is thirty minutes: a drop of more is no autosave
	// rollback.
	rollbackTicks = 30 * 60 * 60
)

// sessionEvent is the line a companion writes first in every game session,
// with data {"map_id": ...} naming the map.
const sessionEvent = "session"

// statusSource marks a session row the service recorded itself from the
// companion's status reply, data {"map_id": ..., "source": "status",
// "last_id": ...} (Store.NoteMap). A companion never writes a source.
const statusSource = "status"

// questionEvent is the line the companion logs for every question asked,
// data {"qid": ...} among the rest.
const questionEvent = "question"

// epochState is where the newest row left the epochs: its epoch, the tick
// the next line of the file is compared with, the epoch's map once a session
// row named one, and the highest question id the epoch has logged. The tick
// is that of the epoch's newest line of the file, 0 while it has none. The
// zero value is a store with no rows.
type epochState struct {
	epoch int64
	tick  int64
	mapID string
	qid   int64
}

// next is the state after one more row. The row's own epoch is the
// result's.
func (s epochState) next(event string, tick int64, data json.RawMessage) epochState {
	return s.after(s.judge(event, tick, data), event, data)
}

// judge places a row by the map it names, by its tick, and by how many
// questions it says the companion has issued.
func (s epochState) judge(event string, tick int64, data json.RawMessage) epochState {
	mapID := sessionMap(event, data)
	if s.epoch == 0 {
		return epochState{epoch: 1, tick: tick, mapID: mapID}
	}
	if s.otherMap(mapID, tick) || s.fewerQuestions(event, data) {
		return epochState{epoch: s.epoch + 1, tick: tick, mapID: mapID}
	}
	if mapID == "" {
		mapID = s.mapID
	}
	return epochState{epoch: s.epoch, tick: tick, mapID: mapID, qid: s.qid}
}

// otherMap reports whether a row at tick, naming map mapID or "" for none,
// is on another map than the epoch's: by the map when both name one, the
// same map dropping into its first five minutes included, and by the tick
// rule otherwise.
func (s epochState) otherMap(mapID string, tick int64) bool {
	if mapID != "" && s.mapID != "" {
		return mapID != s.mapID || freshMap(s.tick, tick)
	}
	return newMap(s.tick, tick)
}

// fewerQuestions reports whether a session row says the companion has issued
// fewer questions than the epoch has logged: its last_id below the highest
// question id of the epoch. The companion's count never goes back while one
// map goes on, so the row is another map or a save from before those
// questions.
func (s epochState) fewerQuestions(event string, data json.RawMessage) bool {
	last, ok := sessionLastID(event, data)
	return ok && last < s.qid
}

// placed is the state after a row that already has an epoch: a row an
// earlier Open or Ingest placed keeps it, and the rows after it follow on.
func (s epochState) placed(epoch, tick int64, event string, data json.RawMessage) epochState {
	n := epochState{epoch: epoch, tick: tick, mapID: sessionMap(event, data)}
	if epoch == s.epoch {
		n.qid = s.qid
		if n.mapID == "" {
			n.mapID = s.mapID
		}
	}
	return s.after(n, event, data)
}

// after is n, the state one row left, with the question id a question row
// raises it to and the tick a status row leaves. A status row is no line of
// the file, so it leaves the tick of the epoch it joined where the file left
// it, and an epoch it started has none yet.
func (s epochState) after(n epochState, event string, data json.RawMessage) epochState {
	if id, ok := questionID(event, data); ok && id > n.qid {
		n.qid = id
	}
	if !fromStatus(event, data) {
		return n
	}
	n.tick = 0
	if n.epoch == s.epoch {
		n.tick = s.tick
	}
	return n
}

// freshMap reports whether a row at tick, after one at prev, dropped into a
// map's first five minutes: a new map, whatever its id.
func freshMap(prev, tick int64) bool {
	return tick < prev && tick < newMapTicks
}

// newMap reports whether a row at tick, after one at prev, is on another
// map by the tick rule alone.
func newMap(prev, tick int64) bool {
	return freshMap(prev, tick) || (tick < prev && prev-tick > rollbackTicks)
}

// fromStatus reports whether a row is a session row the service recorded
// from a status reply, rather than a line of the file.
func fromStatus(event string, data json.RawMessage) bool {
	if event != sessionEvent {
		return false
	}
	var d struct {
		Source string `json:"source"`
	}
	return json.Unmarshal(data, &d) == nil && d.Source == statusSource
}

// sessionMap is the map a session line names, "" for any other line or a
// session line with no usable map_id. A string is taken as written and a
// number as its JSON text, so the same writer always yields the same value.
func sessionMap(event string, data json.RawMessage) string {
	if event != sessionEvent {
		return ""
	}
	var d struct {
		MapID json.RawMessage `json:"map_id"`
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(d.MapID, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(d.MapID, &n) == nil {
		return n.String()
	}
	return ""
}

// sessionLastID is the last_id a session row carries, how many questions the
// companion had issued when the row was written, and false when it carries
// none. Only an integer counts: a value of any other type says nothing.
func sessionLastID(event string, data json.RawMessage) (int64, bool) {
	if event != sessionEvent {
		return 0, false
	}
	var d struct {
		LastID *int64 `json:"last_id"`
	}
	if json.Unmarshal(data, &d) != nil || d.LastID == nil {
		return 0, false
	}
	return *d.LastID, true
}

// questionID is the qid a question row carries, and false for any other row
// or a qid that is not an integer. epochQID reads the stored rows the same
// way.
func questionID(event string, data json.RawMessage) (int64, bool) {
	if event != questionEvent {
		return 0, false
	}
	var d struct {
		QID *int64 `json:"qid"`
	}
	if json.Unmarshal(data, &d) != nil || d.QID == nil {
		return 0, false
	}
	return *d.QID, true
}

// loadEpochState is the state the rows before id `before` left: the newest
// placed row's epoch, the tick of that epoch's newest line of the file, the
// map a session row of that epoch named and its highest question id. Open
// passes no bound and starts Ingest from it; the migration passes the first
// row it has to place.
func loadEpochState(q rowQueryer, before int64) (epochState, error) {
	var s epochState
	err := q.QueryRow("SELECT epoch FROM events WHERE id < ? AND epoch IS NOT NULL ORDER BY id DESC LIMIT 1", before).
		Scan(&s.epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return epochState{}, nil
	}
	if err != nil {
		return epochState{}, err
	}
	if s.tick, err = fileTick(q, s.epoch, before); err != nil {
		return epochState{}, err
	}
	if s.qid, err = epochQID(q, s.epoch, before); err != nil {
		return epochState{}, err
	}
	s.mapID, err = epochMap(q, s.epoch, before)
	return s, err
}

// epochQID is the highest question id epoch logged before id `before`, 0
// when it logged none: a qid read as questionID reads it, an integer or
// nothing.
func epochQID(q rowQueryer, epoch, before int64) (int64, error) {
	var qid sql.NullInt64
	err := q.QueryRow(`SELECT MAX(CASE WHEN json_valid(data) THEN
			CASE WHEN json_type(data, '$.qid') = 'integer' THEN json_extract(data, '$.qid') END END)
		FROM events WHERE epoch = ? AND event = ? AND id < ?`, epoch, questionEvent, before).Scan(&qid)
	return qid.Int64, err
}

// fileTick is the tick of epoch's newest line of the file before id
// `before`, the tick next compares the following line with. A status row
// moves no tick (after), so the ones after that line are passed over, and an
// epoch that holds only status rows has no tick, 0. Epochs only grow with
// the id, so the first row of an older epoch ends the search.
func fileTick(q rowQueryer, epoch, before int64) (int64, error) {
	rows, err := q.Query(`SELECT epoch, tick, event, CASE WHEN event = ? THEN data END FROM events
		WHERE id < ? AND epoch IS NOT NULL ORDER BY id DESC`, sessionEvent, before)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var rowEpoch, tick sql.NullInt64
		var event, data sql.NullString
		if err := rows.Scan(&rowEpoch, &tick, &event, &data); err != nil {
			return 0, err
		}
		if rowEpoch.Int64 != epoch {
			return 0, nil
		}
		if !fromStatus(event.String, json.RawMessage(data.String)) {
			return tick.Int64, nil
		}
	}
	return 0, rows.Err()
}

// epochMap is the map a session row of epoch, before id `before`, named.
// Every session row one epoch holds names the same map, since another map
// starts another epoch, so the newest usable one is the epoch's.
func epochMap(q rowQueryer, epoch, before int64) (string, error) {
	rows, err := q.Query("SELECT data FROM events WHERE epoch = ? AND event = ? AND id < ? ORDER BY id DESC", epoch, sessionEvent, before)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var data sql.NullString
		if err := rows.Scan(&data); err != nil {
			return "", err
		}
		if id := sessionMap(sessionEvent, json.RawMessage(data.String)); id != "" {
			return id, nil
		}
	}
	return "", rows.Err()
}

// rowQueryer reads rows through the store's handle or the migration's
// transaction.
type rowQueryer interface {
	queryer
	Query(query string, args ...any) (*sql.Rows, error)
}
