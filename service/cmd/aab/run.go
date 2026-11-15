// aab run: the service proper. Connect over RCON, build the catalog, tail the
// events file into history, poll for questions, answer each one, and serve the
// control API.
//
// The service is the only party that polls (ADR 0001). Nothing runs inside the
// game until a question arrives, and every read happens on an RCON round trip
// the service itself started. The loop lives in runner.go, the tick in poll.go,
// the answering in answer.go and the catalog in catalog.go.

package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/anthropic"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/openrouter"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/transport"
)

func runService(cfg *config.Config, client *rpc.Client) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mdl, err := buildModel(cfg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	if cfg.Model.Provider == "ollama" {
		// Never fatal, like the balance below: a model pulled or a server
		// started while the service runs works from the next question.
		if v := checkOllama(ctx, cfg); v.level == "ok" {
			log.Printf("run: %s", v.text)
		} else {
			log.Printf("run: WARNING %s", v.text)
		}
	} else if cfg.Model.Provider != "fake" && cfg.Model.Provider != "anthropic" {
		// Said once here and never fatal: credits added while the service
		// runs take effect on the next question without a restart.
		if v := checkBalance(ctx, cfg); v.level == "ok" {
			log.Printf("run: %s", v.text)
		} else {
			log.Printf("run: WARNING %s", v.text)
		}
	}
	store, err := history.Open(cfg.History.Path)
	if err != nil {
		log.Fatalf("run: history %s: %v", cfg.History.Path, err)
	}
	defer store.Close()

	ledgerWriter := ledger.Open(cfg.Ledger.Dir, cfg.LedgerEnabled())
	defer ledgerWriter.Close()
	if cfg.LedgerEnabled() {
		log.Printf("ledger: writing question and round records to %s", cfg.Ledger.Dir)
	} else {
		log.Print("ledger: disabled (ledger.enabled: false)")
	}

	r := &runner{
		cfg:      cfg,
		rpc:      client,
		caller:   &toolCaller{client: client},
		store:    store,
		stats:    controlapi.NewStats(mdl.Name(), time.Now()),
		inFlight: map[int64]*delivery{},
		toasts:   newToaster(cfg.Toasts),
		agent: agent.New(mdl, agent.Caps{
			MaxRounds:               cfg.Agent.MaxRounds,
			MaxTokensPerQuestion:    cfg.Agent.MaxTokensPerQuestion,
			MaxToolResultBytes:      cfg.Agent.MaxToolResultBytes,
			MaxRoundToolResultBytes: cfg.Agent.MaxRoundToolResultBytes,
			Sessions: agent.SessionCaps{
				Idle:         cfg.SessionIdle(),
				NamedIdle:    cfg.NamedSessionIdle(),
				ClarifyIdle:  cfg.ClarifyIdle(),
				MaxExchanges: cfg.Agent.SessionMaxExchanges,
				MaxBytes:     cfg.Agent.SessionMaxBytes,
			},
			QuestionsPerPlayerPerHour: cfg.Agent.QuestionsPerPlayerPerHour,
			QuestionsPerHour:          cfg.Agent.QuestionsPerHour,
			MaxCostPerDay:             cfg.Agent.MaxCostPerDay,
			MaxToolCalls:              cfg.Agent.MaxToolCalls,
		}),
	}

	if cfg.Agent.MaxCostPerDay > 0 {
		keepSpend(r.agent, cfg.Agent.DailySpendPath)
	}
	r.agent.Trace = log.Printf
	r.agent.Ledger = ledgerWriter
	r.agent.Floor = client.Floor
	r.agent.BriefingEnabled = cfg.BriefingEnabled()
	r.agent.Chat = newHistoryChat(store)
	r.agent.Personality = cfg.Agent.Personality
	logVoice(cfg.Agent.Personality)
	if cfg.BriefingEnabled() {
		log.Print("briefing: on, riding with every question (briefing.enabled: true)")
	} else {
		log.Print("briefing: off (briefing.enabled: false)")
	}

	var feed *toastFeed
	if r.toasts != nil {
		feed = r.toasts.feed
		log.Printf("toasts: %s, at most %d an hour, technologies %s", cfg.Toasts.Mode, cfg.Toasts.MaxPerHour, strings.Join(cfg.Toasts.Technologies, ", "))
	} else {
		log.Print("toasts: off (toasts.mode: off)")
	}
	go tailEvents(ctx, cfg, store, feed)
	if cfg.ControlAPI.Addr != "" {
		go func() {
			if err := controlapi.New(cfg.ControlAPI.Addr, cfg.ControlAPI.Token, r.stats).Run(ctx); err != nil {
				// The commonest cause is a second copy of this service on the
				// same machine, and a second copy against the same server
				// answers every question twice: the companion keeps the first
				// answer and says yes to the second, so the race is invisible
				// from either log. Say so here, where it is first visible.
				log.Printf("control api: %v (is another aab already running against this server?)", err)
			}
		}()
	}

	route := fmt.Sprintf("reasoning %s, cache %s, data collection %s", cfg.Model.Reasoning, cfg.Model.CacheTTL, cfg.Model.DataCollection)
	if cfg.Model.Provider == "ollama" {
		// The cache lifetime and data collection are a hosted route's; a
		// local model has neither.
		route = fmt.Sprintf("reasoning %s, context %d tokens", cfg.Model.Reasoning, cfg.Ollama.NumCtx)
	}
	log.Printf("run: model %s via %s (%s), %d rounds and %d tokens per question, polling every %s",
		mdl.Name(), cfg.Model.Provider, route,
		cfg.Agent.MaxRounds, cfg.Agent.MaxTokensPerQuestion, cfg.Interval())
	r.greet(ctx)
	r.loop(ctx)
	log.Print("run: stopped")
}

// logVoice says at startup which voice the config picks, by the label the
// agent logs again whenever the map's own setting takes over or lets go
// (internal/agent, voice.go).
func logVoice(configured string) {
	log.Printf("voice: %s, from agent.personality; a personality set in game overrides it", agent.VoiceLabel(configured))
}

// tailEvents feeds the companion's events.jsonl into history. A read failure
// is logged by the tailer and retried; it never stops the service answering
// live questions.
//
// feed, when toasts are on, gets a candidate for every milestone among the
// lines that arrive after the tailer first caught up (toasts.go).
func tailEvents(ctx context.Context, cfg *config.Config, store *history.Store, feed *toastFeed) {
	tailer := transport.NewLocal(cfg.Factorio.EventsFile, cfg.Interval())
	if cfg.Transport == "sftp" {
		tailer = transport.NewSFTP(transport.SFTPConfig{
			Host:           cfg.Factorio.SFTP.Host,
			User:           cfg.Factorio.SFTP.User,
			KeyPath:        cfg.Factorio.SFTP.KeyPath,
			Password:       cfg.Factorio.SFTP.Password,
			KnownHostsPath: cfg.Factorio.SFTP.KnownHostsPath,
			RequireHostKey: !cfg.Factorio.SFTP.IgnoreHostKey(),
		}, cfg.Factorio.EventsFile, cfg.Interval())
	}
	log.Printf("history: %s, tailing %s over %s", cfg.History.Path, cfg.Factorio.EventsFile, cfg.Transport)
	// The lines a status call got ahead of stay unreadable until the tail has
	// delivered everything written before it (history, guard.go).
	tailer.OnCaughtUp(func(at time.Time) {
		store.CaughtUp(at)
		if feed != nil {
			feed.caughtUp()
		}
	})
	tailer.Run(ctx, func(line []byte) {
		if err := ingestLine(store, feed, cfg.Toasts.Technologies, line); err != nil {
			log.Printf("history: could not store an event: %v", err)
		}
		// The companion only drops a question nobody answered when its ring
		// is full of them, which means this service fell far behind.
		if bytes.Contains(line, []byte(`"question_dropped"`)) {
			log.Printf("WARNING: the companion dropped an unanswered question, its ring was full: %s", bytes.TrimSpace(line))
		}
	})
}

// buildModel picks the provider and model the operator configured. "fake"
// selects the scripted model the end-to-end harness runs against, which
// needs no key.
func buildModel(cfg *config.Config) (model.Model, error) {
	m := cfg.Model
	switch m.Provider {
	case "fake":
		log.Print("run: using the scripted fake model, no API key needed")
		return fake.New(), nil
	case "anthropic":
		if cfg.Anthropic.APIKey == "" {
			return nil, fmt.Errorf("no Anthropic API key: set env var %q, or set model.provider to fake", cfg.Anthropic.APIKeyEnv)
		}
		thinking, effort := "off", ""
		switch m.Reasoning {
		case "model":
			thinking = "model"
		case "low", "medium", "high":
			thinking, effort = "adaptive", m.Reasoning
		}
		// The price table only knows a few ids. An id it does not know costs
		// nothing as far as the daily budget can tell, so the cap never
		// trips. Said loudly at every start instead of refusing to run: an
		// upgrade must not stop a server that was answering yesterday.
		if cfg.Agent.MaxCostPerDay > 0 && !agent.PriceKnown("anthropic", m.ID) {
			log.Printf("WARNING: model.id %q has no price in this build, so agent.max_cost_per_day ($%.2f) will NOT stop spending: every question is counted as $0. Route the model through openrouter, which reports its own cost, or use an id from the price table",
				m.ID, cfg.Agent.MaxCostPerDay)
		}
		timeout, err := m.RequestTimeout()
		if err != nil {
			return nil, err
		}
		return anthropic.New(cfg.Anthropic.APIKey, anthropic.Options{
			Model: m.ID, MaxOutput: cfg.Agent.MaxOutputTokens, Thinking: thinking, Effort: effort, CacheTTL: m.CacheTTL,
			Timeout: timeout,
		}), nil
	case "ollama":
		return newOllama(cfg)
	default:
		if cfg.OpenRouter.APIKey == "" {
			return nil, fmt.Errorf("no OpenRouter API key: set env var %q, or set model.provider to fake", cfg.OpenRouter.APIKeyEnv)
		}
		timeout, err := m.RequestTimeout()
		if err != nil {
			return nil, err
		}
		return openrouter.New(cfg.OpenRouter.APIKey, openrouter.Options{
			Model: m.ID, Fallbacks: m.Fallbacks, MaxOutput: cfg.Agent.MaxOutputTokens,
			Reasoning: m.Reasoning, CacheTTL: m.CacheTTL, DataCollection: m.DataCollection,
			Providers: m.Providers, AllowFallbacks: m.AllowFallbacks, Timeout: timeout,
		}), nil
	}
}
