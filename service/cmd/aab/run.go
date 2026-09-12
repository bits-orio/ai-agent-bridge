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
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
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
	if cfg.Model.Provider != "fake" && cfg.Model.Provider != "anthropic" {
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

	r := &runner{
		cfg:      cfg,
		rpc:      client,
		caller:   &toolCaller{client: client},
		store:    store,
		stats:    controlapi.NewStats(mdl.Name(), time.Now()),
		inFlight: map[int64]*delivery{},
		agent: agent.New(mdl, agent.Caps{
			MaxRounds:            cfg.Agent.MaxRounds,
			MaxTokensPerQuestion: cfg.Agent.MaxTokensPerQuestion,
			MaxToolResultBytes:   cfg.Agent.MaxToolResultBytes,
			Sessions: agent.SessionCaps{
				Idle:         cfg.SessionIdle(),
				NamedIdle:    cfg.NamedSessionIdle(),
				MaxExchanges: cfg.Agent.SessionMaxExchanges,
				MaxBytes:     cfg.Agent.SessionMaxBytes,
			},
			QuestionsPerPlayerPerHour: cfg.Agent.QuestionsPerPlayerPerHour,
			QuestionsPerHour:          cfg.Agent.QuestionsPerHour,
			MaxCostPerDay:             cfg.Agent.MaxCostPerDay,
			MaxToolCalls:              cfg.Agent.MaxToolCalls,
		}),
	}

	r.agent.Trace = log.Printf

	go tailEvents(ctx, cfg, store)
	if cfg.ControlAPI.Addr != "" {
		go func() {
			if err := controlapi.New(cfg.ControlAPI.Addr, cfg.ControlAPI.Token, r.stats).Run(ctx); err != nil {
				log.Printf("control api: %v", err)
			}
		}()
	}

	log.Printf("run: model %s via %s (reasoning %s, cache %s, data collection %s), %d rounds and %d tokens per question, polling every %s",
		mdl.Name(), cfg.Model.Provider, cfg.Model.Reasoning, cfg.Model.CacheTTL, cfg.Model.DataCollection,
		cfg.Agent.MaxRounds, cfg.Agent.MaxTokensPerQuestion, cfg.Interval())
	r.greet(ctx)
	r.loop(ctx)
	log.Print("run: stopped")
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
		return anthropic.New(cfg.Anthropic.APIKey, anthropic.Options{
			Model: m.ID, MaxOutput: cfg.Agent.MaxOutputTokens, Thinking: thinking, Effort: effort, CacheTTL: m.CacheTTL,
		}), nil
	default:
		if cfg.OpenRouter.APIKey == "" {
			return nil, fmt.Errorf("no OpenRouter API key: set env var %q, or set model.provider to fake", cfg.OpenRouter.APIKeyEnv)
		}
		return openrouter.New(cfg.OpenRouter.APIKey, openrouter.Options{
			Model: m.ID, Fallbacks: m.Fallbacks, MaxOutput: cfg.Agent.MaxOutputTokens,
			Reasoning: m.Reasoning, CacheTTL: m.CacheTTL, DataCollection: m.DataCollection,
		}), nil
	}
}
