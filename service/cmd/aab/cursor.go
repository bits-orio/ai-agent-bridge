// Noticing that the game may be on another map or an older save. A restart
// is the only way a headless server loads a save, so any time the companion
// could not be reached counts as one (startAgain). A look at the companion's
// status, every statusEvery ticks, also tells history which map the game is
// on and catches a game that went back without an outage this service saw
// (checkCursor).

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// statusEvery is how many ticks pass between two looks at the companion's
// status: within half a minute at the default one-second interval, for one
// small read in thirty polls.
const statusEvery = 30

// look reads the companion's status ahead of the poll: every statusEvery
// ticks, and first thing after the companion could not be reached. It
// reports whether the tick goes on to poll, which it does not while the
// companion is still out of reach.
//
// The first reply after the companion was out of reach, or the first over a
// new connection, starts the service again (startAgain) before anything
// from that contact is answered. A reply that refuses the status says
// nothing about the map, but it is a reply: the companion is there, and the
// poll goes on.
func (r *runner) look(ctx context.Context) bool {
	mark := r.store.Mark()
	st, err := r.rpc.Status(ctx)
	if rpc.Unreachable(err) {
		r.pollFailed(err)
		return false
	}
	restarted := r.reconnected() || r.lost
	if err == nil {
		r.noteMap(mark, st)
	}
	switch {
	case restarted:
		r.startAgain(st, err)
	case err == nil:
		r.checkCursor(st)
	}
	if err == nil {
		r.see(st)
	}
	return true
}

// startAgain is what an outage costs once the companion answers again: a
// call that got no reply at all, or a reply over a new RCON connection. The
// server may have restarted meanwhile, and a restart may have loaded another
// map or an older save of this one: any question id may now name another
// question, asked in another scope, and any scope key other people. So the
// poll cursor goes back to 0, and the companion offers again whatever is
// still unanswered; what the service held per question id goes, an answer
// already paid for included, so that it can never reach another question;
// and so do the follow-up sessions. A network blip costs the same: a
// question answered twice and a follow-up that starts fresh. One line says
// so, in place of the line that says the poll recovered.
func (r *runner) startAgain(st rpc.StatusReply, statusErr error) {
	seen := "its status refused"
	if statusErr == nil {
		seen = describe(st)
	}
	log.Printf("poll: the companion answers again after an outage (%s): a restart may have loaded another map or an older save, so polling starts from the first question again, with no answer held and no session kept",
		seen)
	r.reset()
	r.lost = false
	r.pollFailing = false
	r.stats.SetConnected(true)
}

// describe is what one status read saw, for a log line.
func describe(st rpc.StatusReply) string {
	text := fmt.Sprintf("tick %d, %d question(s) asked", st.Tick, st.LastID)
	if st.MapID != "" {
		text = fmt.Sprintf("map %s, %s", st.MapID, text)
	}
	return text
}

// checkCursor starts the poll cursor again from 0 when a status shows the
// game went back under it with no outage in between: the highest question
// id issued fell below the cursor or below what the last look saw, the tick
// fell, or the map is another one. The cursor only ever moves forward, so
// after a new map, whose question ids start again at 1, every new question
// would sit at or below it and the service would answer nothing until
// restarted. The tick catches a new map whose ids have already climbed past
// the old cursor; the map catches a save of another map whose tick and ids
// have both climbed past what the last look saw. A headless server cannot
// load a save without a restart, which startAgain already answers for, so
// this is the net under it: whatever the service holds per question id goes
// too, and so do the sessions.
func (r *runner) checkCursor(st rpc.StatusReply) {
	moved, was := r.otherMap(st.MapID), r.lastMap
	if !moved && st.LastID >= r.after && st.LastID >= r.lastID && st.Tick >= r.lastTick {
		return
	}
	where := ""
	if moved {
		where = fmt.Sprintf(", map %s, was %s", st.MapID, was)
	}
	log.Printf("poll: the game went back (tick %d, last question id %d, poll cursor %d%s): a new map or an older save, polling from the start",
		st.Tick, st.LastID, r.after, where)
	r.reset()
}

// reset forgets everything the service holds about one map's questions: the
// poll cursor, what is held per question id, and the follow-up sessions,
// which belong to the map they were asked on.
func (r *runner) reset() {
	r.after = 0
	r.inFlight = map[int64]*delivery{}
	r.agent.ForgetSessions()
}

// reconnected reports whether the latest reply came over another RCON
// connection than the reply the last check saw, and remembers this one. The
// client dials again on its own when a connection breaks, so a server that
// restarted between two calls, a long model run for instance, answers the
// second as if nothing had happened: only the connection's number says so.
// The first connection, and a client that does not number them, report
// false.
func (r *runner) reconnected() bool {
	now := r.rpc.Connection()
	was := r.conn
	r.conn = now
	return was != 0 && now != was
}

// see keeps what one status read saw, for the next look to compare with. A
// reply that names no map leaves the last map named: a companion too old to
// send one says nothing about which map the game is on.
func (r *runner) see(st rpc.StatusReply) {
	r.lastID, r.lastTick = st.LastID, st.Tick
	if st.MapID != "" {
		r.lastMap = st.MapID
	}
}

// otherMap reports whether a status names a map other than the one the last
// look saw. A look that named none has nothing to compare with.
func (r *runner) otherMap(id rpc.MapID) bool {
	return id != "" && r.lastMap != "" && id != r.lastMap
}

// noteMap tells history which map the game is on, so that no read after a
// new map reaches an earlier map's rows, even when the session line naming
// it never reached history: written while the service was down, or never
// written, with events off (history.Store.NoteMap). The status is judged
// against mark, history as it stood before the status call went out, and
// carries how many questions the companion has issued, which history holds
// against the questions it logged. A companion too old to name its map
// changes nothing, and neither does the map history already has. It logs
// only when reads move off an earlier map's rows.
func (r *runner) noteMap(mark history.Mark, st rpc.StatusReply) {
	moved, err := r.store.NoteMap(mark, string(st.MapID), int64(st.Tick), st.LastID)
	if err != nil {
		log.Printf("history: could not record map %s from the companion's status: %v", st.MapID, err)
		return
	}
	if moved {
		log.Printf("history: a new map from the companion's status (map %s, tick %d), ahead of any session line: history reads start again from here",
			st.MapID, st.Tick)
	}
}
