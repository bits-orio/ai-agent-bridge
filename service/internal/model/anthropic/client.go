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
// cap. An artifact is a few hundred tokens; the rest is headroom for adaptive
// thinking, which counts against the same number. Output is the expensive
// direction, so the cap is deliberately low and the per-question budget in
// the config is the wider one.
const DefaultMaxOutputTokens = 4096

// Client answers one agent round through the Anthropic Messages API.
type Client struct {
	api       sdk.Client
	model     string
	maxOutput int64
}

// New builds a client for one model id. The id is a plain string from the
// config (sdk.Model is an alias for string), so a model released after this
// binary was built still works without a code change. maxOutput caps one
// turn's output tokens; zero or less means DefaultMaxOutputTokens.
func New(apiKey, modelID string, maxOutput int) *Client {
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputTokens
	}
	return &Client{
		api:       sdk.NewClient(option.WithAPIKey(apiKey)),
		model:     modelID,
		maxOutput: int64(maxOutput),
	}
}

func (c *Client) Name() string { return c.model }

// Step sends the whole conversation and returns the next assistant turn.
// Thinking is left unset: on Claude Opus 5 that runs adaptive thinking, which
// is the recommended mode, and on older models it means no thinking.
//
// Two cache breakpoints ride on every request. The system prompt carries
// one, and the API caches everything before a breakpoint, so the tool
// definitions in front of it are cached with it: that is the fixed six
// thousand tokens every round used to pay for in full. The last user block
// carries the other, so round three reads rounds one and two from the cache
// instead of re-sending them. A cached read costs a tenth of a fresh token.
func (c *Client) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	params := sdk.MessageNewParams{
		Model:     c.model,
		MaxTokens: c.maxOutput,
		Messages:  cacheLastUserBlock(toMessageParams(msgs)),
	}
	if system != "" {
		params.System = []sdk.TextBlockParam{{Text: system, CacheControl: sdk.NewCacheControlEphemeralParam()}}
	}
	if len(defs) > 0 {
		params.Tools = toToolParams(defs)
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return model.Step{}, fmt.Errorf("anthropic: %s: %w", c.model, err)
	}
	return model.Step{
		Blocks:     fromContentBlocks(resp.Content),
		StopReason: string(resp.StopReason),
		Usage:      fromUsage(resp.Usage),
	}, nil
}
