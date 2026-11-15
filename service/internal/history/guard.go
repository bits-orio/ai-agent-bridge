package history

// Two guards on an epoch whose map a status row named (Store.NoteMap), for
// the two ways a line of the file can be another map's than the status said.
//
// Quarantine (S5). An epoch a status row starts has no tick of its own, so
// every line after it joins it, the lines the tailer still owed from before
// the status included: an SFTP stall, say, while the server moved on to a new
// map. Those are the old map's, and nothing in a line says so. So until the
// epoch's own session line arrives, or the tailer says it has delivered every
// line written before the status (CaughtUp), each line joining the epoch is
// stored unreadable and moves nothing: neither the tick nor the question
// count. Those lines are lost to every read, the price of not knowing whose
// they are; it lasts one poll of the tailer when the file is reachable.
//
// Hold (S1). A status row says which map the game is on now. A session line
// of the file that names another map, while no session line of the file has
// named the status's map yet, is either stale (an old session replayed from
// the top of the file, the old map's private chat behind it) or a real
// switch made after the status. The two cannot be told apart from the line,
// so the epoch it starts is held: every row stored in it is unreadable until
// a status names that same map, which releases them all, nothing lost. A
// status naming any other map moves on to a new epoch and leaves the held
// rows unreadable for good. So a stale session never becomes the current
// epoch's readable rows, and a real switch back to an older save is read
// after one status look, at most statusEvery polls later. Any other map than
// the status's is held, not only one seen before: an unseen one may be just
// as stale, and holding it costs only that delay.
//
// The held state is in the rows themselves (heldOffset), so it survives a
// restart. The quarantine does not need to: a tailer started again starts at
// the end of the file, past every line it owed.

import (
	"database/sql"
	"encoding/json"
	"time"
)

// heldOffset is added to the private column of a row stored in a held epoch:
// 2 for a public row, 3 for a private one. visibleTo reads only 0, and 1 under
// the question's own key, so a held row matches no read until the release
// takes the offset off again.
const heldOffset = 2

// rowMode is how insert stores one row.
type rowMode int

const (
	stored      rowMode = iota // as its visibility says
	held                       // in a held epoch: unreadable until released
	quarantined                // owed from before a status: unreadable for good, moving nothing
)

// guards is the part of the store's state the two guards keep.
type guards struct {
	// statusNamed is true while the current epoch's map is one a status row
	// named and no session line of the file has named it since.
	statusNamed bool
	// quarantine is true from a status row starting an epoch until the
	// tailer has caught up past it (quarantineAt) or the epoch's own session
	// line arrives.
	quarantine   bool
	quarantineAt time.Time
	// held is true while the current epoch is held.
	held bool
}

// lineMode decides how one line of the file is stored, given the state at it
// leaves, and moves the guards on. The caller holds s.mu.
func (s *Store) lineMode(event string, data json.RawMessage, at epochState) (rowMode, epochState) {
	fileMap := sessionMap(event, data)
	switch {
	case fileMap != "" && s.g.statusNamed && s.at.mapID != "" && fileMap != s.at.mapID:
		s.g = guards{held: true}
		if at.epoch == s.at.epoch {
			at = epochState{epoch: s.at.epoch + 1, tick: at.tick, mapID: fileMap}
		}
		return held, at
	case s.g.quarantine && at.epoch == s.at.epoch:
		if fileMap == s.at.mapID {
			s.g.quarantine, s.g.statusNamed = false, false
			return stored, at
		}
		return quarantined, s.at
	}
	if at.epoch != s.at.epoch {
		s.g.quarantine, s.g.statusNamed = false, false
	}
	if fileMap != "" && fileMap == at.mapID {
		s.g.statusNamed = false
	}
	if s.g.held {
		return held, at
	}
	return stored, at
}

// noteStatus moves the guards on for a status row NoteMap is about to record,
// or for one that records nothing; at is the state it leaves. It reports
// whether the row is still to be recorded. The caller holds s.mu.
func (s *Store) noteStatus(mapID string, at epochState) (bool, error) {
	switch {
	case s.g.held && at.epoch == s.at.epoch && mapID == s.at.mapID:
		// A status names the map the held epoch's session line named: the
		// switch was real, and every row held for it reads from now on.
		if _, err := s.db.Exec("UPDATE events SET private = private - ? WHERE epoch = ? AND private >= ?", heldOffset, s.at.epoch, heldOffset); err != nil {
			return false, err
		}
		s.g.held = false
		return false, nil
	case at.epoch != s.at.epoch:
		s.g = guards{statusNamed: true, quarantine: true, quarantineAt: time.Now()}
	case at.mapID != s.at.mapID:
		s.g.statusNamed = true
	}
	return at != s.at, nil
}

// CaughtUp tells history that every line of the file written before at has
// been delivered (transport.Tailer.OnCaughtUp). A quarantine started before
// at ends: what the tailer delivers from now on was written after the status
// that started it.
func (s *Store) CaughtUp(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.g.quarantine && at.After(s.g.quarantineAt) {
		s.g.quarantine = false
	}
}

// loadGuards reads back the guards an earlier run left on the current
// epoch: held when any of its rows still is, and status-named when a status
// row named its map and no session line of the file has. The quarantine
// starts off.
func loadGuards(q rowQueryer, at epochState) (guards, error) {
	var g guards
	if at.epoch == 0 {
		return g, nil
	}
	if err := q.QueryRow("SELECT EXISTS (SELECT 1 FROM events WHERE epoch = ? AND private >= ?)", at.epoch, heldOffset).Scan(&g.held); err != nil {
		return g, err
	}
	if g.held || at.mapID == "" {
		return g, nil
	}
	rows, err := q.Query("SELECT data FROM events WHERE epoch = ? AND event = ?", at.epoch, sessionEvent)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	byStatus, byFile := false, false
	for rows.Next() {
		var data sql.NullString
		if err := rows.Scan(&data); err != nil {
			return g, err
		}
		d := json.RawMessage(data.String)
		if sessionMap(sessionEvent, d) != at.mapID {
			continue
		}
		if fromStatus(sessionEvent, d) {
			byStatus = true
		} else {
			byFile = true
		}
	}
	g.statusNamed = byStatus && !byFile
	return g, rows.Err()
}
