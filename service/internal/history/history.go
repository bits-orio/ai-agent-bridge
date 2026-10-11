// Package history is the service's SQLite record of a save's events.jsonl
// (CONTEXT.md "History"; PLAN.md decision 4; docs/adr/0004-history-in-service.md).
// The companion appends deaths, alerts, chat, joins, research and rockets to
// events.jsonl; the transport tailer reads that file and feeds this package
// one line at a time, in file order, through Ingest. This package never reads
// the file itself and keeps no cursor: the tailer owns that. The only rows
// not from the file are the session rows NoteMap records: the map the
// companion's status names, when the file has not said it (epoch.go).
package history

import (
	"database/sql"
	"fmt"
	"math"
	"sync"

	_ "modernc.org/sqlite" // pure Go sqlite driver, registers as "sqlite"
)

// Store is the SQLite-backed history of one save. Safe for concurrent use:
// the underlying pool is limited to one connection, which also sidesteps
// SQLite's single-writer limitation without a busy-retry loop.
type Store struct {
	db *sql.DB

	// mu serialises Ingest and NoteMap, so each row is judged against the
	// state the row before it left.
	mu sync.Mutex
	// at is where the newest row left the map epochs (epoch.go), read from
	// the file on Open and moved on by every row Ingest or NoteMap stores.
	at epochState
}

// schemaTable is the events table. scope and private say who may read a row
// back (visibility.go), epoch which map it was logged on (epoch.go); a file
// made before they existed gains them in migrate.
const schemaTable = `CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	tick INTEGER,
	event TEXT,
	player TEXT,
	force TEXT,
	data TEXT,
	received_at TEXT,
	scope TEXT,
	private INTEGER,
	epoch INTEGER
)`

const schemaIndex = `CREATE INDEX IF NOT EXISTS idx_events_event_tick ON events(event, tick)`

// Open opens (creating if needed) the SQLite database at path and ensures the
// events table and its indexes exist. Reopening an existing file keeps its
// rows; Open never truncates. A file from before rows carried a scope and an
// epoch is migrated in place: the columns are added and every old row is
// placed and classified before Open returns, so no read ever meets a row
// without them.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	// One connection: SQLite serialises writers per connection anyway, and this
	// avoids SQLITE_BUSY from the pool handing out a second connection while a
	// write is in flight.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	if _, err := db.Exec(schemaTable); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: create events table: %w", err)
	}
	if _, err := db.Exec(schemaIndex); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: create events index: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: migrate %s: %w", path, err)
	}
	at, err := loadEpochState(db, math.MaxInt64)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("history: read the current epoch of %s: %w", path, err)
	}
	return &Store{db: db, at: at}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
