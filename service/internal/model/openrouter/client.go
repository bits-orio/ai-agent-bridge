// Package openrouter is the model.Model that talks to OpenRouter's chat
// completions API (docs/design/phase3-spec.md, part 3; ADR 0007). One
// request per agent round; the agent owns the conversation and runs the
// tools. No SDK: the wire format is small and the fields that matter
// (cache_control, reasoning, provider, usage.cost) are OpenRouter's own.
//
// No OpenRouter key exists on the development machine. Everything here is
// checked against an httptest server that speaks the shapes in
// OpenRouter's reference; the first live run is the real test.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// Endpoint is the chat completions URL. Tests point a client elsewhere.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// Referer and Title identify the app to OpenRouter, which lists apps by
// them; both are public already.
const (
	Referer = "https://github.com/bits-orio/ai-agent-bridge"
	Title   = "AI Agent Bridge"
)

const (
	DefaultMaxOutputTokens = 4096
	retryAfter             = time.Second
)

// Options is what the operator chose, beyond the key.
type Options struct {
	Model          string   // OpenRouter model id, e.g. deepseek/deepseek-v4-pro-0813
	Fallbacks      []string // tried in order when Model fails, OpenRouter's `models`
	MaxOutput      int      // output tokens per turn; zero or less means DefaultMaxOutputTokens
	Reasoning      string   // "off" sends enabled false, "model" sends nothing, low/medium/high send that effort
	CacheTTL       string   // "1h" or "5m" for the rules-and-tools breakpoint on routes that take one
	DataCollection string   // "deny" or "allow", OpenRouter's provider.data_collection; "" sends nothing
	Endpoint       string   // "" means Endpoint
}

// Client answers one agent round through OpenRouter.
type Client struct {
	http *http.Client
	key  string
	opts Options
}

func New(apiKey string, opts Options) *Client {
	if opts.MaxOutput <= 0 {
		opts.MaxOutput = DefaultMaxOutputTokens
	}
	if opts.Endpoint == "" {
		opts.Endpoint = Endpoint
	}
	return &Client{http: &http.Client{Timeout: 120 * time.Second}, key: apiKey, opts: opts}
}

func (c *Client) Name() string { return c.opts.Model }

// Step sends the whole conversation and returns the next assistant turn.
// A 429 or a 5xx is tried once more after a second; anything else is the
// error, with OpenRouter's own message in it.
func (c *Client) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	body, err := json.Marshal(c.request(system, msgs, defs))
	if err != nil {
		return model.Step{}, fmt.Errorf("openrouter: %s: encode: %w", c.opts.Model, err)
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return model.Step{}, err
	}
	return fromResponse(resp), nil
}

func (c *Client) send(ctx context.Context, body []byte) (*response, error) {
	for attempt := 1; ; attempt++ {
		resp, status, err := c.post(ctx, body)
		if err == nil && status/100 == 2 {
			if resp.Error != nil {
				// A 200 carrying an error object: the route failed after
				// the headers went out. Not retried; it is reported.
				return nil, fmt.Errorf("openrouter: %s: %s", c.opts.Model, resp.Error.Message)
			}
			return resp, nil
		}
		retryable := err == nil && (status == http.StatusTooManyRequests || status/100 == 5)
		if !retryable || attempt >= 2 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("openrouter: %s: HTTP %d: %s", c.opts.Model, status, resp.errorText())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
}

func (c *Client) post(ctx context.Context, body []byte) (*response, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("openrouter: %s: %w", c.opts.Model, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", Referer)
	req.Header.Set("X-OpenRouter-Title", Title)
	req.Header.Set("X-Title", Title)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("openrouter: %s: %w", c.opts.Model, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, res.StatusCode, fmt.Errorf("openrouter: %s: read reply: %w", c.opts.Model, err)
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		if res.StatusCode/100 == 2 {
			return nil, res.StatusCode, fmt.Errorf("openrouter: %s: bad reply: %w", c.opts.Model, err)
		}
		out.rawError = string(raw)
	}
	return &out, res.StatusCode, nil
}
