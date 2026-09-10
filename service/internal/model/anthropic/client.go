// Package anthropic is the model.Model backed by the official Anthropic Go
// SDK, written as a manual loop step: one Messages.New call per agent round,
// with the agent owning the conversation and running the tools.
//
// The machine this was written on has no API key, so nothing here has ever
// run against the live API. It is compile-checked against
// github.com/anthropics/anthropic-sdk-go and every symbol comes from the
// SDK's own documented tool-use example (Messages.New with Tools,
// block.AsAny(), variant.JSON.Input.Raw(), NewToolResultBlock,
// NewUserMessage, the StopReason constants). Treat the first live run as the
// real test.
package anthropic

import (
	"context"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// maxOutputTokens caps one assistant turn. An artifact is a few hundred
// tokens at most; the rest is headroom for adaptive thinking, which counts
// against the same budget. The per-question budget in the config is the cap
// that actually matters, and the agent loop enforces it across rounds.
const maxOutputTokens = 8192

// Client answers one agent round through the Anthropic Messages API.
type Client struct {
	api   sdk.Client
	model string
}

// New builds a client for one model id. The id is a plain string from the
// config (sdk.Model is an alias for string), so a model released after this
// binary was built still works without a code change.
func New(apiKey, modelID string) *Client {
	return &Client{
		api:   sdk.NewClient(option.WithAPIKey(apiKey)),
		model: modelID,
	}
}

func (c *Client) Name() string { return c.model }

// Step sends the whole conversation and returns the next assistant turn.
// Thinking is left unset: on Claude Opus 5 that runs adaptive thinking, which
// is the recommended mode, and on older models it means no thinking.
func (c *Client) Step(ctx context.Context, system string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	params := sdk.MessageNewParams{
		Model:     c.model,
		MaxTokens: maxOutputTokens,
		Messages:  toMessageParams(msgs),
	}
	if system != "" {
		params.System = []sdk.TextBlockParam{{Text: system}}
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
