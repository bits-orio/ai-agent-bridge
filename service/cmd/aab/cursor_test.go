package main

// Starting again after the game may have moved to another map or an older
// save (cursor.go). Every test here drives the poll loop the way run does,
// r.tick after r.greet, and never calls a look by hand: what reaches the
// game between two looks is exactly what these tests exist to see.

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// startedAgain is the one line startAgain logs.
const startedAgain = "the companion answers again after an outage"

// tickUntilStatus runs the poll loop through the next look at the
// companion's status, and one tick past it.
func tickUntilStatus(ctx context.Context, r *runner) {
	for {
		r.tick(ctx)
		if r.ticks%statusEvery == 0 {
			r.tick(ctx)
			return
		}
	}
}

// outage takes the server down for a few polls, the way a restart does.
func outage(ctx context.Context, r *runner, companion *fakeCompanion, polls int) {
	companion.crash()
	for range polls {
		r.tick(ctx)
	}
}

// notALook fails the test when the tick just run was a regular look at the
// status: the case under test is the one between two looks.
func notALook(t *testing.T, r *runner) {
	t.Helper()
	if r.ticks%statusEvery == 0 {
		t.Fatalf("tick %d was a regular status look; the test must show what happens between looks", r.ticks)
	}
}

// The review's case as it happens. Team-4 asks privately as question 12 and
// the server crashes as the answer goes out, so the service still holds it.
// The server comes back on the autosave from before question 12, and the
// first question after the reload, a team-9 player's in the open, is question
// 12 again. It is answered for itself on the first tick back, not after the
// next look, and team-4's answer reaches nobody.
func TestAHeldAnswerNeverReachesTheQuestionAfterARestart(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{12: "where is our outpost secret"})
	companion.gameTick, companion.mapID, companion.issued = 500_000, "M1", 11
	companion.askers = map[int64]fakeAsker{12: {player: "Bob", force: "team-4", scope: "team-4", private: true}}
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)

	companion.crashOnAnswer = true
	r.tick(ctx)
	if st := r.inFlight[12]; st == nil || st.done {
		t.Fatalf("question 12 is not held after its delivery failed: %+v", st)
	}
	for range 3 {
		r.tick(ctx)
	}

	companion.restart(499_000, map[int64]string{12: "what time is it here"})
	companion.issued = 11
	companion.askers = map[int64]fakeAsker{12: {player: "Cid", force: "team-9", scope: "global"}}
	r.tick(ctx)
	notALook(t, r)
	got := companion.delivered[12]
	if strings.Contains(got, "outpost secret") {
		t.Fatalf("LEAK: team-4's private answer reached team-9's question 12: %s", got)
	}
	if !strings.Contains(got, "what time is it here") {
		t.Errorf("the question after the reload got %q, want its own answer", got)
	}
	if n := strings.Count(log.String(), startedAgain); n != 1 {
		t.Errorf("logged the start again %d times, want once:\n%s", n, log.String())
	}
}

// The review's second path: the server comes back on another long-running
// save whose question ids sit above the old cursor, and team-4 there asks in
// private. That question starts team-4's session afresh: it never continues
// the session team-4 had on the map before.
func TestARestartOntoAnotherSaveStartsEverySessionAfresh(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.gameTick, companion.mapID = 500_000, "M1"
	companion.askers = map[int64]fakeAsker{1: {player: "Bob", force: "team-4", scope: "team-4", private: true}}
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	outage(ctx, r, companion, 2)
	companion.restart(900_000, map[int64]string{7: "ping"})
	companion.mapID = "M2"
	companion.askers = map[int64]fakeAsker{7: {player: "Dee", force: "team-4", scope: "team-4", private: true}}
	r.tick(ctx)
	notALook(t, r)
	if _, waiting := companion.pending[7]; waiting {
		t.Fatalf("the other save's question 7 was not answered on the first tick back:\n%s", log.String())
	}
	if n := strings.Count(log.String(), "new session team-4"); n != 2 {
		t.Errorf("%d fresh team-4 sessions, want 2: the other save's question continued the session before it:\n%s", n, log.String())
	}
}

// The review's case: the last look is at tick 100,000 with four questions
// asked. An autosave runs at tick 100,600, then question 5 is asked and
// answered, which moves the cursor to 5. The server crashes and reloads the
// autosave, and the asker types the question again: question 5 once more,
// at or below the cursor. The outage put the cursor back, so it is answered.
func TestARollbackShorterThanTheStatusIntervalAnswersTheQuestionAskedAgain(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(questionRing(4))
	companion.gameTick, companion.mapID = 100_000, "M1"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)
	companion.gameTick = 100_900
	companion.pending[5] = "lost in the crash"
	r.tick(ctx)
	if r.after != 5 {
		t.Fatalf("cursor %d before the crash, want 5", r.after)
	}

	outage(ctx, r, companion, 2)
	companion.restart(100_600, map[int64]string{5: "asked again after the reload"})
	companion.gameTick = 101_800
	r.tick(ctx)
	notALook(t, r)
	if _, waiting := companion.pending[5]; waiting {
		t.Fatalf("question 5, asked again after the reload, was never polled: cursor %d", r.after)
	}
	if got := companion.delivered[5]; !strings.Contains(got, "asked again after the reload") {
		t.Errorf("question 5 after the reload got %q", got)
	}
}

// The client dials again on its own, so a server that restarted between two
// polls answers the next as if nothing had happened, and no call fails. The
// connection's number moving on is an outage all the same: the page that
// came over the new connection is not answered, and the next tick starts
// again before anything is.
func TestAReplyOverANewConnectionIsAnOutage(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "question from the first map"})
	companion.gameTick, companion.mapID, companion.conn = 500_000, "M1", 1
	companion.answerErr = "bad_json" // refused, so the answer is held for a retry
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)
	if st := r.inFlight[1]; st == nil || st.done {
		t.Fatalf("question 1 is not held: %+v", st)
	}

	companion.answerErr = ""
	companion.restart(600, map[int64]string{1: "question from the second map"})
	companion.mapID = "M2"
	answers := companion.answers
	r.tick(ctx)
	if companion.answers != answers {
		t.Fatalf("LEAK: a page that came over a new connection was answered: %s", companion.delivered[1])
	}
	r.tick(ctx)
	notALook(t, r)
	if got := companion.delivered[1]; strings.Contains(got, "first map") || !strings.Contains(got, "second map") {
		t.Errorf("the second map's question 1 got %q", got)
	}
	if n := strings.Count(log.String(), startedAgain); n != 1 {
		t.Errorf("logged the start again %d times, want once:\n%s", n, log.String())
	}
}

// A restart can fall inside one question's run: the poll came over one
// connection, and by the time the answer is ready a restarted server answers
// on another, on a save where the same id is someone else's question. The
// answer is held to the connection its question was polled on, so it never
// lands there. It is dropped, and the question on the new save is answered
// for itself.
func TestAnAnswerIsNeverSentOverANewConnection(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "where is our outpost secret"})
	companion.gameTick, companion.mapID, companion.conn = 500_000, "M1", 1
	companion.askers = map[int64]fakeAsker{1: {player: "Bob", force: "team-4", scope: "team-4", private: true}}
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	restarted := false
	companion.onCall = func(op string) {
		// The catalog read comes after the poll and before the answer.
		if op == "providers" && !restarted {
			restarted = true
			companion.restart(400_000, map[int64]string{1: "what time is it here"})
			companion.askers = map[int64]fakeAsker{1: {player: "Cid", force: "team-9", scope: "global"}}
		}
	}
	r.tick(ctx)
	if !restarted {
		t.Fatal("the restart never happened: the test no longer puts it inside the run")
	}
	if got := companion.delivered[1]; got != "" {
		t.Fatalf("LEAK: the old save's answer landed on the new save's question 1: %s", got)
	}
	r.tick(ctx)
	if got := companion.delivered[1]; !strings.Contains(got, "what time is it here") {
		t.Errorf("the new save's question 1 got %q, want its own answer", got)
	}
}

// A status read that gets no reply is an outage like a poll that gets none:
// that tick polls nothing, and the next reply starts the service again
// before anything is answered.
func TestAStatusReadWithNoReplyIsAnOutage(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(questionRing(2))
	companion.gameTick, companion.mapID = 1000, "M1"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	for r.ticks%statusEvery != statusEvery-1 {
		r.tick(ctx)
	}
	companion.unreachable = map[string]bool{"status": true}
	companion.pending[3] = "ping"
	polls := companion.polls
	r.tick(ctx)
	if companion.polls != polls || !r.lost {
		t.Fatalf("after a status read with no reply: %d poll(s), lost %v; want no poll and the companion out of reach", companion.polls-polls, r.lost)
	}
	companion.unreachable = nil
	r.tick(ctx)
	if _, waiting := companion.pending[3]; waiting {
		t.Errorf("question 3 waited past the tick the companion answered again")
	}
	if n := strings.Count(log.String(), startedAgain); n != 1 {
		t.Errorf("logged the start again %d times, want once:\n%s", n, log.String())
	}
}

// When greet cannot reach the companion, the first tick starts with a look,
// even when the companion is back by then: history learns the map at once,
// not thirty polls later, and nothing from before is kept.
func TestTheFirstTickAfterAFailedGreetLooksFirst(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.gameTick, companion.mapID = 4_000, "M1"
	companion.crash()
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	companion.down = false
	r.tick(ctx)
	if r.lastMap != "M1" {
		t.Errorf("the first look after greet failed saw map %q, want M1", r.lastMap)
	}
	if _, waiting := companion.pending[1]; waiting {
		t.Errorf("question 1 was not answered once the companion answered")
	}
	if n := strings.Count(log.String(), startedAgain); n != 1 {
		t.Errorf("logged the start again %d times, want once:\n%s", n, log.String())
	}
}

// A refusal is still a reply: the companion was there all along. A refused
// status read and a refused poll start nothing again, keep the cursor, and
// keep the session a follow-up continues.
func TestARefusalIsNoOutage(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.gameTick, companion.mapID = 1000, "M1"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.statusErr, companion.pollErr = rpc.CodeBadOp, "bad_json"
	tickUntilStatus(ctx, r)
	if r.lost {
		t.Fatal("a refusal put the companion out of reach")
	}
	companion.statusErr, companion.pollErr = "", ""
	companion.pending[2] = "ping"
	r.tick(ctx)
	if r.after != 2 {
		t.Errorf("cursor = %d, want 2", r.after)
	}
	text := log.String()
	if strings.Contains(text, startedAgain) || strings.Contains(text, "the game went back") {
		t.Errorf("a refusal started the service again:\n%s", text)
	}
	if n := strings.Count(text, "new session global"); n != 1 {
		t.Errorf("%d fresh global sessions, want 1: the follow-up lost its session over a refusal:\n%s", n, text)
	}
}

// With no outage the service saw, a new map whose question ids start again
// at 1, below the cursor, is caught at the next look: the service polls from
// the start, says so once, and answers the new map's questions.
func TestTheCursorStartsAgainWhenQuestionIDsDo(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(questionRing(5))
	companion.gameTick = 500_000
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	if r.after != 5 || r.answered != 5 {
		t.Fatalf("cursor %d after %d answers, want 5 and 5", r.after, r.answered)
	}

	companion.newMap(600, map[int64]string{1: "ping", 2: "ping"})
	r.tick(ctx)
	if r.answered != 5 {
		t.Fatalf("answered %d before the status was read: the test no longer shows the deaf cursor", r.answered)
	}
	tickUntilStatus(ctx, r)
	if r.answered != 7 || len(companion.pending) != 0 {
		t.Errorf("answered %d with %d still pending, want the new map's two answered", r.answered, len(companion.pending))
	}
	if r.after != 2 {
		t.Errorf("cursor = %d, want 2 after the new map's questions", r.after)
	}
	if n := strings.Count(log.String(), "the game went back"); n != 1 {
		t.Errorf("logged the reset %d times, want once:\n%s", n, log.String())
	}
}

// A new map whose ids have already climbed past the old cursor still went
// back: the tick says so at the next look. Polling from the cursor would
// skip its first questions for ever.
func TestTheCursorStartsAgainWhenTheTickGoesBack(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(questionRing(3))
	companion.gameTick = 500_000
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.newMap(600, questionRing(5))
	tickUntilStatus(ctx, r)
	if len(companion.pending) != 0 {
		t.Errorf("questions %v were never answered: the cursor skipped the new map's first ids", companion.pending)
	}
}

// The follow-up sessions belong to the map they were asked on, whichever way
// the service learns the game moved.
func TestTheGameGoingBackEndsEverySession(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.gameTick = 500_000
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.newMap(600, map[int64]string{1: "ping"})
	tickUntilStatus(ctx, r)
	if n := strings.Count(log.String(), "new session global"); n != 2 {
		t.Errorf("%d fresh sessions, want 2: the new map's question continued the old map's session:\n%s", n, log.String())
	}
}

// A save of another map can be further on than the last look saw, in tick
// and in question ids alike. Only the map says the game moved, and polling
// from the old cursor would skip the questions at or below it for ever.
func TestAnotherMapStartsTheCursorAgainWhenTickAndIDsClimb(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(questionRing(3))
	companion.gameTick, companion.mapID = 1000, "M1"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)
	if r.after != 3 {
		t.Fatalf("cursor = %d after the first map's questions, want 3", r.after)
	}

	companion.newMap(2_000_000, map[int64]string{2: "ping", 7: "ping"})
	companion.mapID = "M2"
	tickUntilStatus(ctx, r)
	if len(companion.pending) != 0 {
		t.Errorf("questions %v were never answered: the cursor skipped them", companion.pending)
	}
	if !strings.Contains(log.String(), "map M2, was M1") {
		t.Errorf("the reset does not say the map changed:\n%s", log.String())
	}
}

// The map greet saw is what the first look compares with, as are its tick
// and question ids.
func TestTheFirstLookComparesWithTheMapGreetSaw(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(questionRing(2))
	companion.gameTick, companion.mapID = 1000, "M1"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.newMap(2_000_000, map[int64]string{1: "ping", 5: "ping"})
	companion.mapID = "M2"
	tickUntilStatus(ctx, r)
	if len(companion.pending) != 0 {
		t.Errorf("questions %v were never answered on another map than greet saw", companion.pending)
	}
}

// Only two named maps that differ count. A look that names none, from a
// companion too old to send one, has nothing to compare and leaves the last
// map named for the next look to compare with.
func TestOnlyAnotherNamedMapStartsTheCursorAgain(t *testing.T) {
	for _, c := range []struct {
		name  string
		maps  []string // what each look at the status names, in order
		reset bool
	}{
		{"the same map", []string{"M1", "M1"}, false},
		{"a companion that stops naming its map", []string{"M1", ""}, false},
		{"a companion that starts naming its map", []string{"", "M1"}, false},
		{"another map, after a look that named none", []string{"M1", "", "M2"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := captureLog(t)
			companion := newFakeCompanion(questionRing(2))
			companion.gameTick = 1000
			r := newTestRunner(t, companion)
			ctx := context.Background()
			r.tick(ctx)
			for _, id := range c.maps {
				companion.gameTick += 100
				companion.mapID = id
				tickUntilStatus(ctx, r)
			}
			if reset := strings.Contains(log.String(), "the game went back"); reset != c.reset {
				t.Errorf("looks naming %q: a reset %v, want %v:\n%s", c.maps, reset, c.reset, log.String())
			}
		})
	}
}

// Nothing changes while the game goes on: the tick and the ids only grow, so
// the cursor moves on, the session a follow-up continues stays, and nothing
// is logged.
func TestTheCursorStaysWhileTheGameGoesOn(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(questionRing(4))
	companion.gameTick, companion.mapID, companion.conn = 1000, "M1", 1
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.gameTick = 2000
	tickUntilStatus(ctx, r)
	companion.pending[5] = "ping"
	r.tick(ctx)
	if r.after != 5 {
		t.Errorf("cursor = %d, want 5: a game that went on was read as one that went back", r.after)
	}
	text := log.String()
	if strings.Contains(text, "the game went back") || strings.Contains(text, startedAgain) {
		t.Errorf("a reset was logged:\n%s", text)
	}
	if n := strings.Count(text, "new session global"); n != 1 {
		t.Errorf("%d fresh global sessions, want 1:\n%s", n, text)
	}
}

// A save loaded with fewer questions than the last look saw went back too,
// even when its tick is later: the ids below the cursor name the other
// save's questions.
func TestFewerQuestionsThanLastSeenMeansTheGameWentBack(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "question from the first save", 2: "another"})
	companion.gameTick = 10_000
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)

	companion.newMap(2_000_000, map[int64]string{1: "question from the second save"})
	tickUntilStatus(ctx, r)
	if got := companion.delivered[1]; strings.Contains(got, "first save") || !strings.Contains(got, "second save") {
		t.Errorf("the second save's question 1 got %s", got)
	}
}

// The startup line names the config's voice by the agent's own label, so a
// personality: false in the config reads as off, not as a voice called false.
func TestTheStartupLogNamesTheConfiguredVoice(t *testing.T) {
	for configured, want := range map[string]string{
		"false":                  "voice: off, from agent.personality",
		"factorio":               "voice: factorio, from agent.personality",
		"Talk like a ship's cat": "voice: custom, from agent.personality",
	} {
		log := captureLog(t)
		logVoice(configured)
		if !strings.Contains(log.String(), want) {
			t.Errorf("logVoice(%q) logged %q, want %q", configured, log.String(), want)
		}
	}
}

// The page stops at the first answer that gets no reply: the server is gone,
// and every question after it would be paid for with nowhere to send it, or
// sent after a restart to whoever holds its id by then.
func TestThePageStopsWhenTheCompanionGoesOutOfReach(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(questionRing(3))
	companion.gameTick = 1000
	companion.crashOnAnswer = true
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	r.tick(ctx)
	if r.answered != 1 || !r.lost {
		t.Errorf("ran %d question(s) with the companion out of reach after the first, lost %v; want 1 and true", r.answered, r.lost)
	}
}

// The notice sent in place of an artifact the companion refused is held to
// the connection the question was polled on too: after a restart it would
// mark someone else's question answered with a notice meant for this one.
func TestARefusalNoticeIsNeverSentOverANewConnection(t *testing.T) {
	captureLog(t)
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.gameTick, companion.mapID, companion.conn = 1000, "M1", 1
	companion.badShape = "summary"
	r := newTestRunner(t, companion)
	ctx := context.Background()
	r.greet(ctx)
	restarted := false
	companion.onCall = func(op string) {
		// The refused answer arrives, and the server restarts behind it.
		if op == "answer" && !restarted {
			restarted = true
			companion.restart(500, map[int64]string{1: "someone else's question"})
		}
	}
	r.tick(ctx)
	if !restarted {
		t.Fatal("the restart never happened")
	}
	if got := companion.delivered[1]; got != "" {
		t.Errorf("LEAK: the refusal notice landed on the new save's question 1: %s", got)
	}
	if !r.lost {
		t.Error("a notice refused by a changed connection left the companion in reach")
	}
}

// The first connection the service ever sees is no reconnect: a refused
// greet, or none at all, leaves nothing to compare with, and the first page
// is answered at once.
func TestTheFirstConnectionIsNoReconnect(t *testing.T) {
	for _, greet := range []string{"refused", "none"} {
		t.Run(greet, func(t *testing.T) {
			log := captureLog(t)
			companion := newFakeCompanion(map[int64]string{1: "ping"})
			companion.gameTick, companion.conn = 1000, 1
			r := newTestRunner(t, companion)
			ctx := context.Background()
			if greet == "refused" {
				companion.statusErr = rpc.CodeBadOp
				r.greet(ctx)
				companion.statusErr = ""
			}
			r.tick(ctx)
			if _, waiting := companion.pending[1]; waiting || strings.Contains(log.String(), startedAgain) {
				t.Errorf("the first page waited or started again:\n%s", log.String())
			}
		})
	}
}
