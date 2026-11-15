// Milestone toasts: one unprompted line for the whole server when something
// notable happens, a force's first rocket, its tenth, hundredth and
// thousandth, or the first force on the map to finish a headline technology.
//
// Two goroutines take part, and the split is what keeps a toast honest. The
// tailer goroutine judges each event line as history stores it (judge, from
// counts in that row's own map epoch) and only queues a candidate (toastFeed).
// The poll loop sends at most one a tick, after the tick answered its page,
// over the connection that tick verified, and only after every rule that can
// make a toast wrong has passed (toaster.why): another map's epoch, an old
// tick, an outage, nobody online, the hourly cap. Nothing judged before the
// tailer first caught up is queued, so a service that starts, or starts
// again, never announces what happened while it was away.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// The milestones, as the log and the ledger's toast field name them.
const (
	toastFirstRocket = "first_rocket"
	toastRockets     = "rockets"
	toastFirstTech   = "first_tech"
)

// rocketMarks are the counts past the first that a force's rockets are
// announced at.
var rocketMarks = map[int]bool{10: true, 100: true, 1000: true}

// staleTicks is how far behind the newest status tick an event may be and
// still be news: five game minutes.
const staleTicks = 18_000

// toastModelDeadline bounds the model's one line: a toast that waits longer
// is late, and the plain line goes out instead.
const toastModelDeadline = 15 * time.Second

// feedCap bounds the queued candidates. More than this between two ticks is
// a burst nobody wants announced line by line.
const feedCap = 8

// milestone is one thing worth a toast.
type milestone struct {
	kind   string
	force  string
	tech   string
	count  int // the force's rockets, for the rocket kinds
	forces int // forces with a rocket now, this one included
}

// judgeInput is what one event row and its epoch's counts say, all a
// milestone is decided from.
type judgeInput struct {
	event        string
	force        string
	tech         string
	rockets      int  // the force's rocket rows in the epoch, this one included
	rocketForces int  // forces with a rocket row in the epoch, up to this one
	techBefore   bool // a research_finished row for tech came earlier in the epoch
}

// milestoneFor decides whether one event is a milestone. Pure: every count
// it needs is in in.
func milestoneFor(in judgeInput, techs []string) (milestone, bool) {
	if in.force == "" {
		return milestone{}, false
	}
	switch in.event {
	case "rocket_launched":
		switch {
		case in.rockets == 1:
			return milestone{kind: toastFirstRocket, force: in.force, count: 1, forces: in.rocketForces}, true
		case rocketMarks[in.rockets]:
			return milestone{kind: toastRockets, force: in.force, count: in.rockets, forces: in.rocketForces}, true
		}
	case "research_finished":
		if in.tech != "" && !in.techBefore && headline(in.tech, techs) {
			return milestone{kind: toastFirstTech, force: in.force, tech: in.tech}, true
		}
	}
	return milestone{}, false
}

// headline reports whether tech matches one of the configured patterns.
func headline(tech string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, tech); ok {
			return true
		}
	}
	return false
}

// plainLine is the templated toast. Force names go in as names: the
// companion swaps in labels when it renders, as it does for every answer.
func plainLine(m milestone) string {
	switch m.kind {
	case toastFirstRocket:
		line := m.force + " launched its first rocket."
		if m.forces > 1 {
			line += fmt.Sprintf(" That makes %d forces in space.", m.forces)
		}
		return line
	case toastRockets:
		return fmt.Sprintf("%s has launched %s rockets.", m.force, thousands(m.count))
	case toastFirstTech:
		return m.force + " is the first to research " + agent.TechnologyLink(m.tech) + "."
	}
	return ""
}

// thousands writes n with a comma between each group of three digits.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// candidate is a milestone waiting to be sent, with the epoch and tick of
// the line that produced it.
type candidate struct {
	m     milestone
	epoch int64
	tick  int64
}

// judge reads the counts one stored row needs and decides it. Only a public
// row may produce a toast: a private one is nobody else's news, and one the
// history guards held or quarantined may be another map's.
func judge(store *history.Store, row history.Ingested, techs []string) (candidate, bool, error) {
	if row.ID == 0 || !row.Public || (row.Event != "rocket_launched" && row.Event != "research_finished") {
		return candidate{}, false, nil
	}
	in := judgeInput{event: row.Event, force: row.Force}
	var err error
	switch row.Event {
	case "rocket_launched":
		if in.rockets, err = store.RocketsUpTo(row.Epoch, row.Force, row.ID); err != nil {
			return candidate{}, false, err
		}
		if in.rocketForces, err = store.RocketForcesUpTo(row.Epoch, row.ID); err != nil {
			return candidate{}, false, err
		}
	case "research_finished":
		var d struct {
			Tech string `json:"tech"`
		}
		_ = json.Unmarshal(row.Data, &d)
		in.tech = d.Tech
		if in.tech == "" || !headline(in.tech, techs) {
			return candidate{}, false, nil
		}
		if in.techBefore, err = store.ResearchedBefore(row.Epoch, in.tech, row.ID); err != nil {
			return candidate{}, false, err
		}
	}
	m, ok := milestoneFor(in, techs)
	return candidate{m: m, epoch: row.Epoch, tick: row.Tick}, ok, nil
}

// toastFeed is the queue between the tailer goroutine and the poll loop.
type toastFeed struct {
	mu      sync.Mutex
	live    bool // the tailer has caught up once: what it delivers from now on is news
	pending []candidate
}

// caughtUp marks the end of the tailer's first pass.
func (f *toastFeed) caughtUp() {
	f.mu.Lock()
	f.live = true
	f.mu.Unlock()
}

// isLive reports whether lines arriving now are news.
func (f *toastFeed) isLive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live
}

// offer queues one candidate, or drops it when the queue is full.
func (f *toastFeed) offer(c candidate) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) >= feedCap {
		return false
	}
	f.pending = append(f.pending, c)
	return true
}

// take is the oldest queued candidate.
func (f *toastFeed) take() (candidate, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return candidate{}, false
	}
	c := f.pending[0]
	f.pending = f.pending[1:]
	return c, true
}

// drop forgets every queued candidate and reports how many there were.
func (f *toastFeed) drop() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.pending)
	f.pending = nil
	return n
}

// ingestLine is the tailer's per-line hook: store the line, and queue a
// toast when the row is a milestone and the line is live. feed nil (toasts
// off) only stores.
func ingestLine(store *history.Store, feed *toastFeed, techs []string, line []byte) error {
	row, err := store.IngestRow(line)
	if err != nil || feed == nil || !feed.isLive() {
		return err
	}
	c, ok, err := judge(store, row, techs)
	if err != nil {
		return fmt.Errorf("toast: could not judge an event: %w", err)
	}
	if !ok {
		return nil
	}
	if feed.offer(c) {
		log.Printf("toast: queued %s: %s", c.m.kind, plainLine(c.m))
	} else {
		log.Printf("toast: dropped %s, %d already waiting: %s", c.m.kind, feedCap, plainLine(c.m))
	}
	return nil
}

// toaster is the poll loop's side: the rules a send must pass and what they
// remember. Only the poll loop touches it, apart from feed.
type toaster struct {
	cfg         config.ToastsConfig
	feed        *toastFeed
	sent        []time.Time // sends in the last hour, oldest first
	unsupported bool        // say answered bad_op on this connection
}

// newToaster is nil for mode off: nothing is judged, queued or sent.
func newToaster(cfg config.ToastsConfig) *toaster {
	if cfg.Mode == "off" {
		return nil
	}
	return &toaster{cfg: cfg, feed: &toastFeed{}}
}

// sendState is what the poll loop knows at the moment of sending.
type sendState struct {
	lost       bool
	players    int
	epoch      int64  // history's current epoch
	statusTick uint64 // the newest status tick
}

// why is the reason c must not be sent now, "" when it may. The hourly cap
// is checked last, so a toast dropped for another reason spends nothing.
func (t *toaster) why(c candidate, st sendState, now time.Time) string {
	switch {
	case t.unsupported:
		return "the companion does not take say on this connection"
	case st.lost:
		return "the companion is out of reach"
	case c.epoch != st.epoch:
		return "it belongs to another map than the one history reads now"
	case st.statusTick > uint64(max(c.tick, 0)) && st.statusTick-uint64(max(c.tick, 0)) > staleTicks:
		return "the event is more than five game minutes old"
	case st.players <= 0 && !t.cfg.WhenEmpty:
		return "nobody is online"
	}
	t.forget(now)
	if t.cfg.MaxPerHour > 0 && len(t.sent) >= t.cfg.MaxPerHour {
		return fmt.Sprintf("%d already sent in the last hour (toasts.max_per_hour)", len(t.sent))
	}
	return ""
}

// forget drops sends older than an hour.
func (t *toaster) forget(now time.Time) {
	kept := t.sent[:0]
	for _, at := range t.sent {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	t.sent = kept
}

// sendToast sends at most one queued toast, at the end of a tick that
// answered its page: candidates that may not go are dropped with a line
// each, until one goes or none is left.
func (r *runner) sendToast(ctx context.Context) {
	t := r.toasts
	if t == nil {
		return
	}
	for !r.lost && ctx.Err() == nil {
		c, ok := t.feed.take()
		if !ok {
			return
		}
		st := sendState{lost: r.lost, players: r.lastPlayers, epoch: r.store.CurrentEpoch(), statusTick: r.lastTick}
		if reason := t.why(c, st, time.Now()); reason != "" {
			log.Printf("toast: dropped %s, %s: %s", c.m.kind, reason, plainLine(c.m))
			continue
		}
		r.say(ctx, c)
		return
	}
}

// say writes the toast's line and sends it over the connection the tick
// verified.
func (r *runner) say(ctx context.Context, c candidate) {
	t := r.toasts
	line := plainLine(c.m)
	if t.cfg.Mode == "model" {
		line = r.modelLine(ctx, c, line)
	}
	err := r.rpc.SayOn(ctx, r.conn, agent.Summary(line))
	switch {
	case err == nil:
		t.sent = append(t.sent, time.Now())
		log.Printf("toast: said %s: %s", c.m.kind, logSafe(line))
	case errors.Is(err, rpc.ErrSayUnsupported):
		t.unsupported = true
		log.Printf("toast: the companion does not take say (an older companion, or aab-allow-say is off), staying quiet until it reconnects: %v", err)
	case rpc.Unreachable(err):
		r.lost = true
		log.Printf("toast: not sent, no reply or a new connection: %v", err)
	default:
		log.Printf("toast: the companion refused it: %v", err)
	}
}

// modelLine asks the model for the toast in the server's voice, and falls
// back to plain on any failure: an error, the deadline, the day's budget, or
// an answer that is not one line of text.
func (r *runner) modelLine(ctx context.Context, c candidate, plain string) string {
	qctx, cancel := context.WithTimeout(ctx, toastModelDeadline)
	defer cancel()
	q := agent.Question{
		Force: c.m.force,
		Asker: "the service, for a milestone toast",
		Text:  "Write one short line for the whole server celebrating this, in your voice, as a summary of one line: " + plain,
		Toast: c.m.kind,
	}
	res, err := r.agent.Answer(qctx, q, nil)
	r.stats.RecordAnswer(res.Usage, res.CostUSD)
	if err != nil {
		log.Printf("toast: the model failed, sending the plain line: %v", err)
		return plain
	}
	art := res.Artifact
	if art.Shape == agent.ShapeNotice || art.ToAsker {
		log.Printf("toast: no line from the model (%s), sending the plain line", logSafe(art.Line()))
		return plain
	}
	if line := strings.TrimSpace(art.Line()); line != "" {
		return line
	}
	return plain
}
