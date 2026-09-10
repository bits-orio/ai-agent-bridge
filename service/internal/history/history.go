// Package history is the service's SQLite record of a save's events.jsonl
// (CONTEXT.md "History"; PLAN.md decision 4; docs/adr/0004-history-in-service.md).
// The companion appends deaths, alerts, chat, joins, research and rockets to
// events.jsonl; the transport tailer reads that file and feeds this package
// one line at a time, in file order, through Ingest. This package never reads
// the file itself and keeps no cursor: the tailer owns that.
package history

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure Go sqlite driver, registers as "sqlite"
)

// Store is the SQLite-backed history of one save. Safe for concurrent use:
// the underlying pool is limited to one connection, which also sidesteps
// SQLite's single-writer limitation without a busy-retry loop.
type Store struct {
	db *sql.DB
}

const schemaTable = `CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	tick INTEGER,
	event TEXT,
	player TEXT,
	force TEXT,
	data TEXT,
	received_at TEXT
)`

const schemaIndex = `CREATE INDEX IF NOT EXISTS idx_events_event_tick ON events(event, tick)`

// Open opens (creating if needed) the SQLite database at path and ensures the
// events table and its (event, tick) index exist. Reopening an existing file
// keeps its rows; Open never truncates.
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
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
