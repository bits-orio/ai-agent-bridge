package history

// The counts a milestone toast is decided from (cmd/aab, toasts.go). Each
// reads one epoch only, the one the row being judged was stored in, and only
// rows up to that row, so a line judged twice gets the same answer.
//
// The counts take every row of the epoch, whatever its visibility: the
// number is never shown, only compared, and a row the guards made unreadable
// (guard.go) may be this map's. Counting it errs toward silence (a first
// rocket read as a second), never toward announcing a milestone twice.

// CurrentEpoch is the epoch the newest row is in: the map history reads now.
func (s *Store) CurrentEpoch() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at.epoch
}

// RocketsUpTo counts force's rocket_launched rows in epoch with an id up to
// and including upTo.
func (s *Store) RocketsUpTo(epoch int64, force string, upTo int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE event = 'rocket_launched' AND epoch = ? AND force = ? AND id <= ?`,
		epoch, force, upTo).Scan(&n)
	return n, err
}

// RocketForcesUpTo counts the forces with a rocket_launched row in epoch up
// to and including upTo.
func (s *Store) RocketForcesUpTo(epoch int64, upTo int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT force) FROM events WHERE event = 'rocket_launched' AND epoch = ? AND id <= ? AND force != ''`,
		epoch, upTo).Scan(&n)
	return n, err
}

// ResearchedBefore reports whether any force finished tech, at any level, in
// epoch before row before.
func (s *Store) ResearchedBefore(epoch int64, tech string, before int64) (bool, error) {
	var found bool
	err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE event = 'research_finished' AND epoch = ? AND id < ?
		AND CASE WHEN json_valid(data) THEN json_extract(data, '$.tech') END = ?)`,
		epoch, before, tech).Scan(&found)
	return found, err
}
