// Package anthropic is the model.Model backed by the official Anthropic Go
// SDK, written as a manual loop step: one Messages.New call per agent round,
// with the agent owning the conversation and running the tools.
//
// Every symbol here comes from the SDK's own documented tool-use example
// (Messages.New with Tools, block.AsAny(), variant.JSON.Input.Raw(),
// NewToolResultBlock, NewUserMessage, the StopReason constants) plus the
// prompt-caching fields (CacheControl on a block param,
// NewCacheControlEphemeralParam). The first live run answered four questions
// on 2026-09-10 (TESTING.md section 3).
package anthropic

import (
	"context"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// DefaultMaxOutputTokens caps one assistant turn when the operator sets no
// cap. An artifact is a few hundred tokens; the rest is headroom for a
// model that thinks, which counts against the same number. Output is the
// expensive direction, so the cap is deliberately low and the per-question
// budget in the config is the wider one.
const DefaultMaxOutputTokens = 4096

// Options is what the operator chose for the model, beyond the key.
type Options struct {
	Model     string
	MaxOutput int    // output tokens per turn; zero or less means DefaultMaxOutputTokens
	Thinking  string // "off" sends thinking disabled, "adaptive" sends adaptive, "model" or "" sends nothing
	Effort    string // "" sends nothing; otherwise output_config.effort as given
	CacheTTL  string // "1h" or "5m" for the rules-and-tools cache entry; "" means 5m
}

// Client answers one agent round through the Anthropic Messages API.
type Client struct {
	api  sdk.Client
	opts Options
}

// New builds a client for one model id. The id is a plain string from the
// config (sdk.Model is an alias for string), so a model released after this
// binary was built still works without a code change.
func New(apiKey string, opts Options) *Client {
	if opts.MaxOutput <= 0 {
		opts.MaxOutput = DefaultMaxOutputTokens
	}
	return &Client{api: sdk.NewClient(option.WithAPIKey(apiKey)), opts: opts}
}

func (c *Client) Name() string { return c.opts.Model }

// Step sends the whole conversation and returns the next assistant turn.
func (c *Client) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	resp, err := c.api.Messages.New(ctx, c.params(system, msgs, defs))
	if err != nil {
		return model.Step{}, fmt.Errorf("anthropic: %s: %w", c.opts.Model, err)
	}
	return model.Step{
		Blocks:     fromContentBlocks(resp.Content),
		StopReason: string(resp.StopReason),
		Usage:      fromUsage(resp.Usage),
	}, nil
}

// params is the whole request for one round.
//
// Thinking is off unless the operator turned it on: a Claude 5 model runs
// adaptive thinking when the field is left out, and on a question like "what
// forces are there" that thinking is most of the output bill. Effort rides
// along only when set, since not every model accepts it.
//
// Two cache breakpoints ride on every request. The system prompt carries
// one with the operator's TTL, and the API caches everything before a
// breakpoint, so the tool definitions ahead of it are cached with it: that
// is the fixed few thousand tokens every round used to pay for in full. The
// last user block carries the other, on the five-minute lifetime, so round
// three reads rounds one and two from the cache instead of re-sending them.
// The longer-lived entry has to come first in the prompt, which the order
// system then messages already guarantees.
func (c *Client) params(system string, msgs []model.Message, defs []model.ToolDef) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		Model:     c.opts.Model,
		MaxTokens: int64(c.opts.MaxOutput),
		Messages:  cacheLastUserBlock(toMessageParams(msgs)),
	}
	switch c.opts.Thinking {
	case "off":
		params.Thinking = sdk.ThinkingConfigParamUnion{OfDisabled: &sdk.ThinkingConfigDisabledParam{}}
	case "adaptive":
		params.Thinking = sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{}}
	}
	if c.opts.Effort != "" {
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(c.opts.Effort)}
	}
	if system != "" {
		cache := sdk.NewCacheControlEphemeralParam()
		if c.opts.CacheTTL == "1h" {
			cache.TTL = sdk.CacheControlEphemeralTTLTTL1h
		}
		params.System = []sdk.TextBlockParam{{Text: system, CacheControl: cache}}
	}
	if len(defs) > 0 {
		params.Tools = toToolParams(defs)
	}
	return params
}
