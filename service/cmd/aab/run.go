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
			MaxToolResultBytes:        cfg.Agent.MaxToolResultBytes,
			MemoryTTL:                 cfg.MemoryTTL(),
			QuestionsPerPlayerPerHour: cfg.Agent.QuestionsPerPlayerPerHour,
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

	log.Printf("run: model %s (thinking %s, effort %s, cache %s), %d rounds and %d tokens per question, polling every %s",
		mdl.Name(), cfg.Anthropic.Thinking, orDefault(cfg.Anthropic.Effort, "model default"), cfg.Anthropic.CacheTTL,
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
	return anthropic.New(cfg.Anthropic.APIKey, anthropic.Options{
		Model:     cfg.Anthropic.Model,
		MaxOutput: cfg.Agent.MaxOutputTokens,
		Thinking:  cfg.Anthropic.Thinking,
		Effort:    cfg.Anthropic.Effort,
		CacheTTL:  cfg.Anthropic.CacheTTL,
	}), nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
