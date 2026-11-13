// The Ollama provider: building the client, and the preflight that says
// before the first question whether the server is up, the model is pulled
// and the model takes tools. Each of the three otherwise shows up in game
// as "I could not reach the model", which names none of them.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/ollama"
)

func newOllama(cfg *config.Config) (*ollama.Client, error) {
	timeout, err := cfg.Model.RequestTimeout()
	if err != nil {
		return nil, err
	}
	return ollama.New(ollama.Options{
		Model: cfg.Model.ID, BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
		NumCtx: cfg.Ollama.NumCtx, MaxOutput: cfg.Agent.MaxOutputTokens,
		Reasoning: cfg.Model.Reasoning, KeepAlive: cfg.Ollama.KeepAlive, Timeout: timeout,
	}), nil
}

// checkOllama is one line about the Ollama server and the model: ok, or
// fail with what to do about it.
func checkOllama(ctx context.Context, cfg *config.Config) balanceVerdict {
	c, err := newOllama(cfg)
	if err != nil {
		return balanceVerdict{"fail", err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := c.Check(ctx)
	var he *ollama.HTTPError
	switch {
	case errors.As(err, &he) && he.Status == http.StatusNotFound:
		return balanceVerdict{"fail", fmt.Sprintf("Ollama at %s has no model %s: run `ollama pull %s` on that machine", cfg.Ollama.BaseURL, cfg.Model.ID, cfg.Model.ID)}
	case err != nil:
		return balanceVerdict{"fail", fmt.Sprintf("Ollama at %s did not answer (%v): start it with `ollama serve`, or set ollama.base_url", cfg.Ollama.BaseURL, err)}
	case !info.HasTools():
		return balanceVerdict{"fail", fmt.Sprintf("model %s cannot call tools (it can: %s), and every question needs them: pick a model tagged \"tools\" at https://ollama.com/search?c=tools",
			cfg.Model.ID, strings.Join(info.Capabilities, ", "))}
	}
	return balanceVerdict{"ok", fmt.Sprintf("Ollama %s at %s, model %s, context %d tokens, kept loaded %s",
		info.Version, cfg.Ollama.BaseURL, cfg.Model.ID, cfg.Ollama.NumCtx, cfg.Ollama.KeepAlive)}
}
