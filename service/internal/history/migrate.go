package history

// The scope, private and epoch columns arrived after live saves already held
// tens of thousands of rows. migrate adds them to a file that predates them,
// places every row in its map epoch (epoch.go) and classifies every row that
// has no visibility yet, with the same rules Ingest applies to a new line, so
// an old file and a new one read back alike. It is safe to run on every
// Open: a column already there is left alone, and a row already placed or
// classified is never touched again.

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// addedColumns are the columns migrate makes sure exist, in order.
var addedColumns = []struct{ name, typ string }{
	{"scope", "TEXT"},
	{"private", "INTEGER"},
	{"epoch", "INTEGER"},
}

// epochIndex serves every read, each of which stays inside one epoch.
const epochIndex = `CREATE INDEX IF NOT EXISTS idx_events_epoch_event ON events(epoch, event)`

func migrate(db *sql.DB) error {
	for _, col := range addedColumns {
		if err := addColumn(db, col.name, col.typ); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}
	// Epochs first: an answer looks for its question in its own epoch, and
	// a legacy chat line's visibility depends on what its epoch saw.
	if err := backfillEpochs(db); err != nil {
		return fmt.Errorf("place old rows in map epochs: %w", err)
	}
	if err := backfill(db); err != nil {
		return fmt.Errorf("classify old rows: %w", err)
	}
	if _, err := db.Exec(epochIndex); err != nil {
		return fmt.Errorf("create epoch index: %w", err)
	}
	return nil
}

// addColumn adds one column to events unless it is already there.
func addColumn(db *sql.DB, name, typ string) error {
	has, err := hasColumn(db, name)
	if err != nil || has {
		return err
	}
	_, err = db.Exec("ALTER TABLE events ADD COLUMN " + name + " " + typ)
	return err
}

func hasColumn(db *sql.DB, name string) (bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info('events')")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return false, err
		}
		if col == name {
			return true, nil
		}
	}
	return false, rows.Err()
}

// epochPageRows bounds how many rows the epoch backfill holds at once.
const epochPageRows = 5000

// unplaced is one row as the epoch backfill reads it. data is read only for
// a session line, the one line whose content can move an epoch, and a
// question, whose qid a later session line is held against.
type unplaced struct {
	id, tick int64
	event    string
	data     json.RawMessage
	epoch    sql.NullInt64
}

// backfillEpochs places every row that has no epoch yet, oldest first, by
// the rule Ingest applies to a new line, starting from where the rows before
// the first of them left off. One transaction, so a failure places nothing;
// a page of rows at a time, so a large file is never held whole.
func backfillEpochs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op once Commit has run
	var first sql.NullInt64
	if err := tx.QueryRow("SELECT MIN(id) FROM events WHERE epoch IS NULL").Scan(&first); err != nil || !first.Valid {
		return err
	}
	state, err := loadEpochState(tx, first.Int64)
	if err != nil {
		return err
	}
	update, err := tx.Prepare("UPDATE events SET epoch = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer update.Close()
	for after := first.Int64 - 1; ; {
		page, err := readEpochPage(tx, after)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return tx.Commit()
		}
		for _, r := range page {
			if r.epoch.Valid {
				state = state.placed(r.epoch.Int64, r.tick, r.event, r.data)
				continue
			}
			state = state.next(r.event, r.tick, r.data)
			if _, err := update.Exec(state.epoch, r.id); err != nil {
				return fmt.Errorf("row %d: %w", r.id, err)
			}
		}
		after = page[len(page)-1].id
	}
}

// readEpochPage reads the next page of rows after id `after`, in full before
// any of them is updated: one connection serves the read and the writes.
func readEpochPage(tx *sql.Tx, after int64) ([]unplaced, error) {
	rows, err := tx.Query(`SELECT id, tick, event, CASE WHEN event IN (?, ?) THEN data END, epoch
		FROM events WHERE id > ? ORDER BY id LIMIT ?`, sessionEvent, questionEvent, after, epochPageRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unplaced
	for rows.Next() {
		var r unplaced
		var tick sql.NullInt64
		var event, data sql.NullString
		if err := rows.Scan(&r.id, &tick, &event, &data, &r.epoch); err != nil {
			return nil, err
		}
		r.tick, r.event, r.data = tick.Int64, event.String, json.RawMessage(data.String)
		out = append(out, r)
	}
	return out, rows.Err()
}

// unclassified is one row that has no visibility yet.
type unclassified struct {
	id    int64
	epoch int64
	event string
	force string
	data  json.RawMessage
}

// backfill classifies every row whose private column is still NULL, oldest
// first and in one transaction, so a question is classified before the
// answer that inherits from it and a failure leaves no half-classified file.
//
// One rule here goes further than Ingest's. A legacy chat line on a map that
// only ever saw one force was heard by every player there, so it is public,
// not private to that force (oneForceEpochs). Ingest cannot know that of a
// line as it arrives; the migration reads the whole epoch first.
func backfill(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op once Commit has run
	pending, err := readUnclassified(tx)
	if err != nil || len(pending) == 0 {
		return err
	}
	oneForce, err := oneForceEpochs(tx)
	if err != nil {
		return err
	}
	update, err := tx.Prepare("UPDATE events SET scope = ?, private = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer update.Close()
	for _, r := range pending {
		v, err := rowVisibility(tx, r.epoch, r.id, r.event, r.force, r.data)
		if err != nil {
			return fmt.Errorf("row %d: %w", r.id, err)
		}
		if oneForce[r.epoch] && legacyChat(r.event, r.force, r.data) {
			v = public
		}
		if _, err := update.Exec(v.scope, v.flag(), r.id); err != nil {
			return fmt.Errorf("row %d: %w", r.id, err)
		}
	}
	return tx.Commit()
}

// oneForceEpochs is every epoch whose rows name exactly one force and none of
// which says it was private. On such a map every player shared that force.
// Any second force on any row, a join, a death, a research or a chat line,
// is a player or a team a force's chat was hidden from, and a row that states
// it was private is a channel someone was kept out of, so either leaves the
// epoch out. A private flag is any stated value but false, as declared reads
// it.
func oneForceEpochs(tx *sql.Tx) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT epoch FROM events WHERE epoch IS NOT NULL GROUP BY epoch
		HAVING COUNT(DISTINCT CASE WHEN force <> '' THEN force END) = 1
		AND TOTAL(CASE WHEN json_valid(data) THEN
			CASE WHEN json_type(data, '$.private') <> 'false' THEN 1 END END) = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var epoch int64
		if err := rows.Scan(&epoch); err != nil {
			return nil, err
		}
		out[epoch] = true
	}
	return out, rows.Err()
}

// readUnclassified reads every row backfill has to classify, in full, before
// the first update runs: one connection serves both, so the read is finished
// rather than left open beside the writes.
func readUnclassified(tx *sql.Tx) ([]unclassified, error) {
	rows, err := tx.Query("SELECT id, epoch, event, force, data FROM events WHERE private IS NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unclassified
	for rows.Next() {
		var r unclassified
		var epoch sql.NullInt64
		var event, force, data sql.NullString
		if err := rows.Scan(&r.id, &epoch, &event, &force, &data); err != nil {
			return nil, err
		}
		r.epoch, r.event, r.force, r.data = epoch.Int64, event.String, force.String, json.RawMessage(data.String)
		out = append(out, r)
	}
	return out, rows.Err()
}
