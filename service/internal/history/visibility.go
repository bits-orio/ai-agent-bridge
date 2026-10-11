package history

// Who may read a row back (CONTEXT.md "History"). Every events row records
// the scope it was said in, a key and whether it is private, and the map
// epoch it was logged in (epoch.go). Only the current epoch is read at all.
// Within it, a private row reaches an answer only when that answer is heard
// by the same private scope alone; every other row is public. The rule is
// applied here, in SQL, on every read of event content, and never left to
// the prompt: a model told not to repeat a line has still read it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
)

// visibility is who may read one events row back: the scope it was said in.
type visibility struct {
	scope   string
	private bool
}

// public is every row that is not private to anyone.
var public = visibility{scope: chatscope.GlobalKey}

// flag is the private column's value.
func (v visibility) flag() int {
	if v.private {
		return 1
	}
	return 0
}

// declaredScope is the pair a companion after 1.0.5 writes beside a chat
// line, and every companion since scope providers existed writes beside a
// question. Raw, so a value of the wrong type is read, never refused, and a
// line is never dropped over it.
type declaredScope struct {
	Private json.RawMessage `json:"private"`
	Scope   json.RawMessage `json:"scope"`
}

// declared reads the scope a row's own data states, and false when it states
// none. Only a literal false is public: any other value in the private field
// reads as private, the safe side of a writer's mistake. A private row with
// no usable key matches no question's scope, so nobody reads it back.
func declared(data json.RawMessage) (visibility, bool) {
	var d declaredScope
	if json.Unmarshal(data, &d) != nil || d.Private == nil {
		return visibility{}, false
	}
	var key string
	_ = json.Unmarshal(d.Scope, &key) // a missing or non-string scope stays ""
	if string(d.Private) == "false" {
		if key == "" {
			key = chatscope.GlobalKey
		}
		return visibility{scope: key}, true
	}
	return visibility{scope: key, private: true}, true
}

// classify decides one row's visibility from what the companion logged, for
// every row that is not an answer inheriting its question's
// (answerVisibility).
//
// A row that states its own scope keeps it, whatever the event: today that
// is a chat line from a companion after 1.0.5, and every question. A chat
// line from a companion up to 1.0.5 states none. Factorio hands a plain line
// to the speaker's force alone, so such a line is private to the force it
// was logged with. Only a line with no force is public: that is the server
// console's own, which every player saw. The migration alone knows better
// for one case: an epoch that only ever saw one force (migrate.go). A
// question that states no scope predates scope providers, when every
// question was global, and every other event is a fact about the game,
// public.
func classify(event, force string, data json.RawMessage) visibility {
	if v, ok := declared(data); ok {
		return v
	}
	if legacyChat(event, force, data) {
		return visibility{scope: force, private: true}
	}
	return public
}

// queryer is what a visibility lookup reads through: the store's own handle
// when a line is ingested, the migration's transaction when old rows are
// classified.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// questionByQID finds the newest question row before a given id carrying a
// given qid in one epoch. Question ids start again on every map, so a
// question from another epoch is never the answer's. The CASE keeps one
// malformed data value from failing the lookup.
const questionByQID = `SELECT scope, private FROM events
	WHERE event = 'question' AND epoch = ? AND id < ?
	AND CASE WHEN json_valid(data) THEN json_extract(data, '$.qid') END = ?
	ORDER BY id DESC LIMIT 1`

// unreadable is private under the empty key, which no question can carry:
// chatscope.Scope.PrivateKey refuses an empty key, so every read passes over
// such a row.
var unreadable = visibility{private: true}

// answerVisibility is an answer row's visibility: its question's. An answer
// row holds no text, but a public one would still tell every force that a
// private question was answered at that tick. The search stays inside the
// answer's epoch and before the answer itself. An answer with no question
// there, or no qid at all, is unreadable: its question may have been private,
// and nothing is left to say whose.
func answerVisibility(q queryer, epoch, before int64, data json.RawMessage) (visibility, error) {
	var d struct {
		QID *int64 `json:"qid"`
	}
	if json.Unmarshal(data, &d) != nil || d.QID == nil {
		return unreadable, nil
	}
	var scope sql.NullString
	var private sql.NullInt64
	err := q.QueryRow(questionByQID, epoch, before, *d.QID).Scan(&scope, &private)
	if errors.Is(err, sql.ErrNoRows) {
		return unreadable, nil
	}
	if err != nil {
		return visibility{}, err
	}
	return visibility{scope: scope.String, private: !private.Valid || private.Int64 != 0}, nil
}

// rowVisibility is the one rule Ingest applies to a new line and the
// migration applies to an old row, so the two can never disagree. epoch is
// the row's own.
func rowVisibility(q queryer, epoch, before int64, event, force string, data json.RawMessage) (visibility, error) {
	if _, stated := declared(data); event == "answer" && !stated {
		return answerVisibility(q, epoch, before, data)
	}
	return classify(event, force, data), nil
}

// legacyChat reports whether a row is a chat line from a companion up to
// 1.0.5: a player's line, logged with a force and no scope of its own.
func legacyChat(event, force string, data json.RawMessage) bool {
	_, stated := declared(data)
	return event == "console_chat" && force != "" && !stated
}

// currentEpoch is the epoch of the newest row: the map the server is on now.
// A newest row with no epoch yet makes it NULL, and NULL equals nothing.
const currentEpoch = "(SELECT epoch FROM events ORDER BY id DESC LIMIT 1)"

// visibleTo is the one condition every read of event content passes through:
// rows of the current epoch only, and of those, public rows always and
// private rows only for a question asked privately in the same scope. ctx
// carries that question's scope (chatscope.With); a ctx without one reads as
// a public question. A row not yet placed in an epoch, or not yet
// classified, matches no branch and is never read.
func visibleTo(ctx context.Context) (string, []any) {
	inEpoch := "epoch = " + currentEpoch + " AND "
	if key, ok := chatscope.From(ctx).PrivateKey(); ok {
		return inEpoch + "(private = 0 OR (private = 1 AND scope = ?))", []any{key}
	}
	return inEpoch + "private = 0", nil
}
