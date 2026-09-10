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
		cfg:    cfg,
		rpc:    client,
		caller: &toolCaller{client: client},
		store:  store,
		stats:  controlapi.NewStats(mdl.Name(), time.Now()),
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
	cursor  int64
	lastErr string
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
	log.Printf("run: companion %s on protocol %d, %d player(s), %d question(s) pending, ask command %s",
		st.ModVersion, st.ProtocolVersion, st.PlayerCount, st.PendingCount, st.AskCommand)
}

func (r *runner) loop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *runner) tick(ctx context.Context) {
	questions, err := r.rpc.Poll(ctx, r.cursor)
	if err != nil {
		r.stats.SetConnected(false)
		r.logOnce(fmt.Sprintf("run: poll failed: %v", err))
		return
	}
	r.stats.SetConnected(true)
	r.lastErr = ""
	for _, q := range questions {
		r.answer(ctx, q)
		r.cursor = q.ID
	}
}

// answer runs one question and sends the artifact back. Every ending delivers
// something: a model that fails still gets a notice printed in the game
// rather than leaving the asker waiting.
func (r *runner) answer(ctx context.Context, q rpc.Question) {
	question := agent.Question{
		ID:          q.ID,
		Text:        q.Text,
		PlayerIndex: q.PlayerIndex,
		Force:       q.Force,
		Asker:       askerLabel(q),
	}
	log.Printf("question %d from %s (force %s): %s", q.ID, question.Asker, q.Force, q.Text)

	result, err := r.agent.Answer(ctx, question, r.toolsFor(ctx))
	if err != nil {
		log.Printf("question %d: the model failed: %v", q.ID, err)
		result.Artifact = agent.Notice(agent.LevelWarning, modelFailedNotice)
	}
	r.stats.RecordAnswer(result.Usage, result.CostUSD)
	log.Printf("answer %d shape=%s rounds=%d tokens_in=%d tokens_out=%d cost_usd=%.4f",
		q.ID, result.Artifact.Shape, result.Rounds, result.Usage.InputTokens, result.Usage.OutputTokens, result.CostUSD)

	if _, err := r.rpc.Answer(ctx, q.ID, result.Artifact); err != nil {
		log.Printf("answer %d: could not deliver it: %v", q.ID, err)
	}
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

// logOnce keeps a repeating failure, a server that is down for instance, to
// one line instead of one line per poll.
func (r *runner) logOnce(msg string) {
	if msg == r.lastErr {
		return
	}
	r.lastErr = msg
	log.Print(msg)
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
	var protocol *rpc.Error
	if errors.As(err, &protocol) && (protocol.Code == rpc.CodeNoProvider || protocol.Code == rpc.CodeNoTool) {
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

func askerLabel(q rpc.Question) string {
	if q.PlayerIndex != nil {
		return fmt.Sprintf("player %d", *q.PlayerIndex)
	}
	return "another mod"
}
