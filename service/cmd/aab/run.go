// aab run: the service proper. Connect over RCON, build the catalog, tail the
// events file into history, poll for questions, answer each one, and serve the
// control API.
//
// The service is the only party that polls (ADR 0001). Nothing runs inside the
// game until a question arrives, and every read happens on an RCON round trip
// the service itself started.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/anthropic"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
	"github.com/bits-orio/ai-agent-bridge/service/internal/transport"
)

// catalogTTL is how long a catalog snapshot is reused. A provider added or
// removed mid-session is picked up either at the next refresh or the first
// time a call reports an unknown provider, whichever comes first.
const catalogTTL = 10 * time.Minute

// pollLimit is how many questions one poll asks for. The companion serves the
// oldest unanswered ones, so a page at a time is all the service ever needs and
// a backlog can never encode past the companion's reply cap.
const pollLimit = 16

// maxDeliveries is how many times one answer is offered to the companion before
// the service stops trying (review-fix contract 3). The artifact is already paid
// for, so it is worth a retry; it is not worth retrying forever.
const maxDeliveries = 3

// heartbeatEvery is how often a service with nothing to do says so, which is how
// an operator tells "idle" from "wedged" in the log.
const heartbeatEvery = 5 * time.Minute

const modelFailedNotice = "I could not reach the model just now. Try again in a moment."

func runService(cfg *config.Config, client *rpc.Client) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mdl, err := buildModel(cfg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	store, err := history.Open(cfg.History.Path)
	if err != nil {
		log.Fatalf("run: history %s: %v", cfg.History.Path, err)
	}
	defer store.Close()

	r := &runner{
		cfg:      cfg,
		rpc:      client,
		caller:   &toolCaller{client: client},
		store:    store,
		stats:    controlapi.NewStats(mdl.Name(), time.Now()),
		inFlight: map[int64]*delivery{},
		agent: agent.New(mdl, agent.Caps{
			MaxRounds:                 cfg.Agent.MaxRounds,
			MaxTokensPerQuestion:      cfg.Agent.MaxTokensPerQuestion,
			MemoryTTL:                 cfg.MemoryTTL(),
			QuestionsPerPlayerPerHour: cfg.Agent.QuestionsPerPlayerPerHour,
		}),
	}

	go tailEvents(ctx, cfg, store)
	if cfg.ControlAPI.Addr != "" {
		go func() {
			if err := controlapi.New(cfg.ControlAPI.Addr, cfg.ControlAPI.Token, r.stats).Run(ctx); err != nil {
				log.Printf("control api: %v", err)
			}
		}()
	}

	log.Printf("run: model %s, %d rounds and %d tokens per question, polling every %s",
		mdl.Name(), cfg.Agent.MaxRounds, cfg.Agent.MaxTokensPerQuestion, cfg.Interval())
	r.greet(ctx)
	r.loop(ctx)
	log.Print("run: stopped")
}

// runner holds everything one polling service needs. Only the poll loop
// touches it, so nothing here needs a lock of its own.
type runner struct {
	cfg    *config.Config
	rpc    *rpc.Client
	caller *toolCaller
	store  *history.Store
	stats  *controlapi.Stats
	agent  *agent.Agent

	tools   []tools.Tool
	builtAt time.Time

	// inFlight is one entry per question the service has picked up, kept until
	// the companion stops offering it. No cursor is kept anywhere, in memory or
	// on disk (review-fix contract 3): the companion decides what is still
	// unanswered, so a restart resumes instead of re-answering everything its
	// ring still holds.
	inFlight map[int64]*delivery

	answered     int
	lastActivity time.Time
	pollFailing  bool

	// page is the poll page size in use. It starts at pollLimit, halves each
	// time the companion refuses a poll reply as too_large (sixteen long
	// questions can outgrow the reply cap) and goes back to pollLimit once a
	// page comes back short, which means the backlog has drained.
	page int
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
// whether RCON and the mod are both there.
func (r *runner) greet(ctx context.Context) {
	st, err := r.rpc.Status(ctx)
	if err != nil {
		r.stats.SetConnected(false)
		log.Printf("run: cannot reach the companion yet: %v", err)
		return
	}
	r.stats.SetConnected(true)
	r.stats.SetModVersion(st.ModVersion)
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

func (r *runner) tick(ctx context.Context) {
	limit := r.pageSize()
	questions, err := r.rpc.Poll(ctx, 0, limit)
	if err != nil {
		if rpc.HasCode(err, rpc.CodeTooLarge) && limit > 1 {
			r.page = limit / 2
			log.Printf("poll reply too large at %d questions, trying %d", limit, r.page)
			return
		}
		r.stats.SetConnected(false)
		r.pollFailed(err)
		return
	}
	if len(questions) < limit {
		r.page = pollLimit
	}
	r.stats.SetConnected(true)
	r.pollRecovered()
	r.forgetGone(questions, limit)
	for _, q := range questions {
		r.handle(ctx, q)
	}
}

// handle takes one polled question as far as it can go this tick: run it if it
// is new, deliver it, and leave it alone once it is finished with.
func (r *runner) handle(ctx context.Context, q rpc.Question) {
	state := r.inFlight[q.ID]
	if state == nil {
		state = &delivery{result: r.run(ctx, q)}
		r.inFlight[q.ID] = state
	}
	if state.done {
		return
	}
	r.deliver(ctx, q, state)
}

// run is the part the operator pays for: the agent loop. Every ending produces
// an artifact, so a model that fails still has a notice to deliver rather than
// leaving the asker waiting.
func (r *runner) run(ctx context.Context, q rpc.Question) agent.Result {
	question := agentQuestion(q)
	log.Printf("question %d from %s: %s", q.ID, question.AskerLabel(), q.Text)

	result, err := r.agent.Answer(ctx, question, r.toolsFor(ctx))
	if err != nil {
		log.Printf("question %d: the model failed: %v", q.ID, err)
		result.Artifact = agent.Notice(agent.LevelWarning, modelFailedNotice)
	}
	r.stats.RecordAnswer(result.Usage, result.CostUSD)
	r.answered++
	r.lastActivity = time.Now()
	return result
}

// deliver offers one artifact to the companion. A delivery that failed on the
// wire is tried again on the next tick with the artifact already in hand: the
// model is never asked the same question twice. Three failures and the service
// stops trying, with a line saying so.
func (r *runner) deliver(ctx context.Context, q rpc.Question, state *delivery) {
	state.attempts++
	delivered, err := r.rpc.Answer(ctx, q.ID, state.result.Artifact)
	if err == nil && !delivered {
		// The companion replied without confirming. Treated as a failed
		// delivery, so the three-attempt budget applies instead of the answer
		// being dropped on the floor.
		err = errors.New("the companion did not confirm it")
	}
	switch {
	case err == nil:
		state.done = true
		log.Printf("answer %d shape=%s rounds=%d tokens=%d/%d cost=$%.4f",
			q.ID, state.result.Artifact.Shape, state.result.Rounds,
			state.result.Usage.InputTokens, state.result.Usage.OutputTokens, state.result.CostUSD)
	case rpc.HasCode(err, rpc.CodeBadArtifact):
		// The companion cannot render this artifact, so sending it again would
		// fail the same way (review-fix contract 5).
		state.done = true
		log.Printf("answer %d: the companion refused the artifact, not retrying: %v", q.ID, err)
	case state.attempts >= maxDeliveries:
		state.done = true
		log.Printf("answer %d: giving up after %d failed deliveries: %v", q.ID, state.attempts, err)
	default:
		log.Printf("answer %d: could not deliver it, trying again next tick: %v", q.ID, err)
	}
}

// forgetGone drops what the service remembers about questions the companion no
// longer offers: answered and rendered, or aged out of its ring. A full page may
// be hiding newer questions, so an id above it is kept until the service has
// been shown everything pending.
// pageSize is the poll page in use, pollLimit until a reply proves too large.
func (r *runner) pageSize() int {
	if r.page <= 0 {
		return pollLimit
	}
	return r.page
}

func (r *runner) forgetGone(offered rpc.PollReply, limit int) {
	if len(r.inFlight) == 0 {
		return
	}
	live := make(map[int64]bool, len(offered))
	var highest int64
	for _, q := range offered {
		live[q.ID] = true
		if q.ID > highest {
			highest = q.ID
		}
	}
	full := len(offered) >= limit
	for id := range r.inFlight {
		if live[id] || (full && id > highest) {
			continue
		}
		delete(r.inFlight, id)
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
// not one line per poll.
func (r *runner) pollFailed(err error) {
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

// toolsFor returns the catalog to run a question against, rebuilding it when
// a provider went missing or the snapshot is old (CONTEXT.md invariant 3).
func (r *runner) toolsFor(ctx context.Context) []tools.Tool {
	if r.tools == nil || r.caller.stale.Load() || time.Since(r.builtAt) > catalogTTL {
		r.rebuild(ctx)
	}
	return r.tools
}

func (r *runner) rebuild(ctx context.Context) {
	providers, err := r.rpc.Tools(ctx)
	if err != nil {
		log.Printf("catalog: cannot read the tools list, keeping the last one: %v", err)
		return
	}
	game := catalog.Build(providers, r.caller).Tools()
	stored := r.store.Tools()

	fresh := make([]tools.Tool, 0, len(game)+len(stored))
	fresh = append(fresh, game...)
	fresh = append(fresh, stored...)

	r.tools = fresh
	r.builtAt = time.Now()
	r.caller.stale.Store(false)
	log.Printf("catalog: %d tool(s) from %d provider(s), %d history tool(s)", len(game), len(providers), len(stored))
}

// toolCaller is the rpc client with a flag on it: a call that reports an
// unknown provider or an unknown tool means the catalog no longer matches the
// server, so the next question rebuilds it.
type toolCaller struct {
	client *rpc.Client
	stale  atomic.Bool
}

func (t *toolCaller) CallTool(ctx context.Context, iface, fn string, args any) (json.RawMessage, error) {
	out, err := t.client.CallTool(ctx, iface, fn, args)
	if rpc.HasCode(err, rpc.CodeNoProvider) || rpc.HasCode(err, rpc.CodeNoTool) {
		t.stale.Store(true)
	}
	return out, err
}

// tailEvents feeds the companion's events.jsonl into history. A read failure
// is logged by the tailer and retried; it never stops the service answering
// live questions.
func tailEvents(ctx context.Context, cfg *config.Config, store *history.Store) {
	tailer := transport.NewLocal(cfg.Factorio.EventsFile, cfg.Interval())
	if cfg.Transport == "sftp" {
		tailer = transport.NewSFTP(transport.SFTPConfig{
			Host:           cfg.Factorio.SFTP.Host,
			User:           cfg.Factorio.SFTP.User,
			KeyPath:        cfg.Factorio.SFTP.KeyPath,
			Password:       cfg.Factorio.SFTP.Password,
			KnownHostsPath: cfg.Factorio.SFTP.KnownHostsPath,
		}, cfg.Factorio.EventsFile, cfg.Interval())
	}
	log.Printf("history: %s, tailing %s over %s", cfg.History.Path, cfg.Factorio.EventsFile, cfg.Transport)
	tailer.Run(ctx, func(line []byte) {
		if err := store.Ingest(line); err != nil {
			log.Printf("history: could not store an event: %v", err)
		}
	})
}

// buildModel picks the model the operator configured. "fake" selects the
// scripted model the end-to-end harness runs against, which needs no key.
func buildModel(cfg *config.Config) (model.Model, error) {
	if cfg.Anthropic.Model == fake.ModelID {
		log.Print("run: using the scripted fake model, no API key needed")
		return fake.New(), nil
	}
	if cfg.Anthropic.APIKey == "" {
		return nil, fmt.Errorf("no API key: set env var %q, or set the model to %q to run the scripted model", cfg.Anthropic.APIKeyEnv, fake.ModelID)
	}
	return anthropic.New(cfg.Anthropic.APIKey, cfg.Anthropic.Model), nil
}

// agentQuestion maps one polled question onto the agent's question type. The
// asker's name travels with it, which is what lets the label name a player:
// history rows are keyed by player name, so "when did I last die" can only be
// scoped to the person who asked when the prompt carries their name (review-fix
// contract 9). The label itself is built in one place, agent.Question.AskerLabel,
// so the log line and the system prompt always name the asker the same way.
func agentQuestion(q rpc.Question) agent.Question {
	return agent.Question{
		ID:          q.ID,
		Text:        q.Text,
		PlayerIndex: q.PlayerIndex,
		PlayerName:  q.PlayerName,
		Force:       q.Force,
	}
}
