// The state one polling service keeps, and the loop that drives it. Only the
// poll loop touches a runner, so nothing here needs a lock of its own.

package main

import (
	"context"
	"log"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// heartbeatEvery is how often a service with nothing to do says so, which is how
// an operator tells "idle" from "wedged" in the log.
const heartbeatEvery = 5 * time.Minute

type runner struct {
	labelsFailed bool // the labels op failed on the last question; logged once per streak
	cfg          *config.Config
	rpc          *rpc.Client
	caller       *toolCaller
	store        *history.Store
	stats        *controlapi.Stats
	agent        *agent.Agent

	tools   []tools.Tool
	builtAt time.Time
	// catalogWaiting is true while questions are piling up behind a catalog the
	// service cannot read, so the reason is logged once per streak and not once
	// per question per tick.
	catalogWaiting bool

	// inFlight is one entry per question the service has picked up, kept until
	// the question is finished with or the companion stops offering it.
	inFlight map[int64]*delivery

	// after is the poll watermark: every question with an id at or below it is
	// finished with, delivered or given up on. Polling from it is what stops a
	// question the service abandoned from sitting at the head of every later
	// page, which used to make the service permanently deaf (second review-fix
	// contract 3). It is never persisted: a restart begins at 0 and the
	// companion decides again what is still unanswered. It starts again at 0
	// when the game goes back under it, a new map or an older save
	// (cursor.go).
	after int64

	// modVersion is the companion's version from its last status, which says
	// which answer shapes it can render (artFor).
	modVersion string

	answered     int
	lastActivity time.Time
	pollFailing  bool

	// page is the poll page size in use. It halves each time the companion
	// refuses a reply as too_large and doubles back toward pollLimit after every
	// poll that lands (second review-fix contract 4).
	page int

	// ticks counts polls, so the companion's status is read every statusEvery
	// of them (cursor.go). lastID, lastTick and lastMap are what that read saw
	// last: the highest question id issued, the game tick and the map's id.
	// Either of the first two going back, or another map, means the game
	// went back.
	ticks    int
	lastID   int64
	lastTick uint64
	lastMap  rpc.MapID

	// lost is true from a call that got no reply at all, a poll, a status
	// read or a delivery, or a reply over a new connection, until the
	// companion answers again. A restart is the only way a headless server
	// loads a save, so the first reply after it starts the service again
	// (cursor.go, startAgain) before anything is answered. conn is the RCON
	// connection the last checked reply came over (reconnected), and every
	// answer is held to it (deliver).
	lost bool
	conn uint64
}

// delivery is what the service knows about one question: the artifact the model
// produced, how many times it has been offered to the companion, and whether the
// question is finished with. A finished question may keep coming back in the poll
// reply (the companion never rendered it), and must not be answered twice.
type delivery struct {
	result   agent.Result
	attempts int
	done     bool
}

// greet says hello to the companion once, so an operator sees straight away
// whether RCON and the mod are both there. It also tells history which map
// the game is on: a map that changed while the service was down named
// itself in a session line the tailer, starting at the end of the file,
// never reads.
//
// When it cannot reach the companion, the first tick that can starts with a
// look at the status instead (cursor.go), which tells history the map.
func (r *runner) greet(ctx context.Context) {
	mark := r.store.Mark()
	st, err := r.rpc.Status(ctx)
	if !rpc.Unreachable(err) {
		r.reconnected() // any reply names the connection later ones compare with
	}
	if err != nil {
		r.stats.SetConnected(false)
		log.Printf("run: cannot reach the companion yet: %v", err)
		r.lost = r.lost || rpc.Unreachable(err)
		return
	}
	r.stats.SetConnected(true)
	r.stats.SetModVersion(st.ModVersion)
	r.see(st)
	r.noteMap(mark, st)
	log.Printf("run: companion %s on protocol %d, %d player(s), %d question(s) pending, %d asked so far, ask command %s",
		st.ModVersion, st.ProtocolVersion, st.PlayerCount, st.PendingCount, st.LastID, st.AskCommand)
}

func (r *runner) loop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval())
	defer ticker.Stop()
	heartbeat := time.NewTicker(heartbeatEvery)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		case <-heartbeat.C:
			r.heartbeat()
		}
	}
}

// heartbeat says the service is alive with nothing to do. It keeps quiet while
// questions are arriving: the per-question lines already say that.
func (r *runner) heartbeat() {
	if time.Since(r.lastActivity) < heartbeatEvery {
		return
	}
	r.lastActivity = time.Now()
	log.Printf("idle, %d questions answered", r.answered)
}

// pollFailed logs once per failure streak: a server that is down costs one line,
// not one line per poll. A call that got no reply at all, rather than a
// refusal, puts the companion out of reach (lost), so the next reply starts
// the service again.
func (r *runner) pollFailed(err error) {
	r.stats.SetConnected(false)
	if rpc.Unreachable(err) {
		r.lost = true
	}
	if r.pollFailing {
		return
	}
	r.pollFailing = true
	log.Printf("poll failed: %v", err)
}

func (r *runner) pollRecovered() {
	if r.pollFailing {
		log.Print("poll recovered, the companion is answering again")
	}
	r.pollFailing = false
}
