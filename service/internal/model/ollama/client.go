// Package ollama is the model.Model that talks to an Ollama server's native
// chat API, POST /api/chat, one request per agent round. The agent owns the
// conversation and runs the tools.
//
// The native API and not the OpenAI-compatible one, for one reason: only the
// native API takes options.num_ctx. Ollama gives a model a 4,096-token
// context on a card under 24 GiB, the rules and tool definitions alone are
// about 10,000 tokens, and a prompt over the context is cut without an
// error, so the service has to ask for the context it needs on every request.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

const (
	// DefaultBaseURL is where a local Ollama listens.
	DefaultBaseURL = "http://localhost:11434"
	// DefaultNumCtx holds the rules, the tool definitions, the briefing and
	// a few rounds of tool results. An 8B model at this context fits a
	// 10 GiB card.
	DefaultNumCtx          = 16384
	DefaultMaxOutputTokens = 4096
	// DefaultTimeout bounds one request. A local model that has to be loaded
	// first and then read 10,000 tokens takes far longer than a hosted one.
	DefaultTimeout = 5 * time.Minute
	// DefaultKeepAlive keeps the model in memory between questions; Ollama's
	// own five minutes means a cold load for most of them.
	DefaultKeepAlive = "30m"
	retryAfter       = time.Second
)

// HTTPError is a non-2xx answer from Ollama, carrying its own message. 404
// is a model that was never pulled.
type HTTPError struct {
	Status int
	Model  string
	Text   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("ollama: %s: HTTP %d: %s", e.Model, e.Status, e.Text)
}

// Options is what the operator chose.
type Options struct {
	Model     string        // the model as Ollama names it, e.g. qwen3:8b
	BaseURL   string        // "" means DefaultBaseURL
	APIKey    string        // only for a server that wants one (https://ollama.com); "" sends no header
	NumCtx    int           // context window in tokens; zero or less means DefaultNumCtx
	MaxOutput int           // output tokens per turn; zero or less means DefaultMaxOutputTokens
	Reasoning string        // "off" sends think false, "model" sends nothing, low/medium/high send think true
	KeepAlive string        // how long the model stays loaded; "" means DefaultKeepAlive
	Timeout   time.Duration // one request, connect to last byte; zero means DefaultTimeout
}

// Client answers one agent round through Ollama.
type Client struct {
	http *http.Client
	opts Options
}

func New(opts Options) *Client {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	if opts.NumCtx <= 0 {
		opts.NumCtx = DefaultNumCtx
	}
	if opts.MaxOutput <= 0 {
		opts.MaxOutput = DefaultMaxOutputTokens
	}
	if opts.KeepAlive == "" {
		opts.KeepAlive = DefaultKeepAlive
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	return &Client{http: &http.Client{Timeout: opts.Timeout}, opts: opts}
}

func (c *Client) Name() string { return c.opts.Model }

// Step sends the whole conversation and returns the next assistant turn. A
// 5xx or a request that ran into the client's own timeout is tried once
// more after a second; anything else is the error, with Ollama's own
// message in it.
func (c *Client) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	body, err := json.Marshal(c.request(system, msgs, defs))
	if err != nil {
		return model.Step{}, fmt.Errorf("ollama: %s: encode: %w", c.opts.Model, err)
	}
	for attempt := 1; ; attempt++ {
		var resp response
		status, err := c.do(ctx, http.MethodPost, "/api/chat", body, &resp)
		if err == nil && status/100 == 2 && resp.Error == "" {
			if wasCut(len(body), &resp) {
				return model.Step{}, fmt.Errorf("ollama: %s: the prompt (%d bytes) did not fit the %d-token context and Ollama cut it; raise ollama.num_ctx",
					c.opts.Model, len(body), c.opts.NumCtx)
			}
			full := map[string]string{}
			for name, short := range shortNames(defs) {
				full[short] = name
			}
			return fromResponse(&resp, full), nil
		}
		timedOut := err != nil && ctx.Err() == nil && isTimeout(err)
		retryable := timedOut || (err == nil && status/100 == 5)
		if !retryable || attempt >= 2 {
			switch {
			case timedOut:
				return model.Step{}, fmt.Errorf("ollama: %s: no reply within %s, twice: %w", c.opts.Model, c.opts.Timeout, err)
			case err != nil:
				return model.Step{}, err
			}
			return model.Step{}, &HTTPError{Status: status, Model: c.opts.Model, Text: resp.Error}
		}
		select {
		case <-ctx.Done():
			return model.Step{}, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// do sends one request and decodes the JSON reply into out. A non-2xx reply
// that is not JSON leaves its text in out's Error field when out is a
// *response; the status is returned either way.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.opts.BaseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("ollama: %s: %w", c.opts.Model, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ollama: %s: %w", c.opts.Model, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return res.StatusCode, fmt.Errorf("ollama: %s: read reply: %w", c.opts.Model, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if res.StatusCode/100 == 2 {
			return res.StatusCode, fmt.Errorf("ollama: %s: bad reply: %w", c.opts.Model, err)
		}
		if r, ok := out.(*response); ok {
			r.Error = strings.TrimSpace(string(raw))
		}
	}
	return res.StatusCode, nil
}

// Info is what a preflight wants to know about the server and the model.
type Info struct {
	Version      string   // the Ollama server's version
	Capabilities []string // what the model can do, e.g. completion, tools, thinking
}

// HasTools says whether the model takes tool definitions. Every question
// needs them.
func (i Info) HasTools() bool {
	for _, c := range i.Capabilities {
		if c == "tools" {
			return true
		}
	}
	return false
}

// Check asks the server for its version and for the model's capabilities.
// An *HTTPError with status 404 means the model has not been pulled.
func (c *Client) Check(ctx context.Context) (Info, error) {
	var info Info
	var version struct {
		Version string `json:"version"`
	}
	status, err := c.do(ctx, http.MethodGet, "/api/version", nil, &version)
	if err != nil {
		return info, err
	}
	if status/100 != 2 {
		return info, &HTTPError{Status: status, Model: "version", Text: "no version from " + c.opts.BaseURL}
	}
	info.Version = version.Version

	body, _ := json.Marshal(map[string]string{"model": c.opts.Model})
	var show struct {
		Capabilities []string `json:"capabilities"`
		Error        string   `json:"error"`
	}
	status, err = c.do(ctx, http.MethodPost, "/api/show", body, &show)
	if err != nil {
		return info, err
	}
	if status/100 != 2 {
		return info, &HTTPError{Status: status, Model: c.opts.Model, Text: show.Error}
	}
	info.Capabilities = show.Capabilities
	return info, nil
}
