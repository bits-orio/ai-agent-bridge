// Blocks both ways: model.Message to SDK request params, and SDK response
// content back to model.Block. The agent loop owns the history, so
// resp.ToParam() is deliberately not used; every assistant turn is rebuilt
// from the neutral blocks the loop kept.

package anthropic

import (
	"encoding/json"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// toMessageParams converts the conversation into SDK message params.
func toMessageParams(msgs []model.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		blocks := toContentBlocks(m.Blocks)
		if len(blocks) == 0 {
			continue
		}
		if m.Role == model.RoleAssistant {
			out = append(out, sdk.NewAssistantMessage(blocks...))
			continue
		}
		out = append(out, sdk.NewUserMessage(blocks...))
	}
	return out
}

func toContentBlocks(blocks []model.Block) []sdk.ContentBlockParamUnion {
	out := make([]sdk.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case model.BlockText:
			if b.Text == "" {
				continue
			}
			out = append(out, sdk.NewTextBlock(b.Text))
		case model.BlockToolUse:
			out = append(out, sdk.NewToolUseBlock(b.ID, decodeInput(b.Input), b.Name))
		case model.BlockToolResult:
			out = append(out, sdk.NewToolResultBlock(b.ID, b.Content, b.IsError))
		case model.BlockThinking:
			// Signature first, then the text: that is the SDK's argument
			// order (NewThinkingBlock(signature, thinking)).
			out = append(out, sdk.NewThinkingBlock(b.Signature, b.Thinking))
		case model.BlockRedactedThinking:
			out = append(out, sdk.NewRedactedThinkingBlock(b.Data))
		}
	}
	return out
}

// decodeInput turns raw tool arguments into a plain map. ToolUseBlockParam
// takes the input as any and the SDK encodes it itself, so hand it a value it
// certainly understands rather than trusting raw JSON to survive.
func decodeInput(raw json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	return args
}

// toToolParams converts tool definitions into the Tools slice. The schema is
// split into what ToolInputSchemaParam names directly (properties, required)
// and everything else, which rides along as extra fields so a constraint like
// additionalProperties reaches the API intact.
func toToolParams(defs []model.ToolDef) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		schema := sdk.ToolInputSchemaParam{
			Properties: d.Schema["properties"],
			Required:   requiredNames(d.Schema["required"]),
		}
		if extra := extraSchemaFields(d.Schema); len(extra) > 0 {
			schema.ExtraFields = extra
		}
		tool := sdk.ToolParam{
			Name:        d.Name,
			Description: sdk.String(d.Description),
			InputSchema: schema,
		}
		out = append(out, sdk.ToolUnionParam{OfTool: &tool})
	}
	return out
}

// requiredNames accepts the required list however the schema built it: a
// []string from tools.ObjectSchema, or a []any when it came back through JSON.
func requiredNames(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func extraSchemaFields(schema map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		switch k {
		case "type", "properties", "required":
			continue
		}
		out[k] = v
	}
	return out
}

// fromContentBlocks converts a response's content into neutral blocks.
// Thinking blocks are kept in the position they arrived in and handed back
// unchanged on the next round: extended thinking signs each block, and a
// signed block dropped from the history invalidates the turn it belonged to.
// A variant this loop has no use for at all, a server tool result for
// instance, is dropped.
func fromContentBlocks(content []sdk.ContentBlockUnion) []model.Block {
	var out []model.Block
	for _, block := range content {
		switch variant := block.AsAny().(type) {
		case sdk.TextBlock:
			out = append(out, model.Block{Type: model.BlockText, Text: variant.Text})
		case sdk.ThinkingBlock:
			out = append(out, model.Block{
				Type:      model.BlockThinking,
				Thinking:  variant.Thinking,
				Signature: variant.Signature,
			})
		case sdk.RedactedThinkingBlock:
			out = append(out, model.Block{Type: model.BlockRedactedThinking, Data: variant.Data})
		case sdk.ToolUseBlock:
			out = append(out, model.Block{
				Type:  model.BlockToolUse,
				ID:    variant.ID,
				Name:  variant.Name,
				Input: json.RawMessage(variant.JSON.Input.Raw()),
			})
		}
	}
	return out
}

func fromUsage(u sdk.Usage) model.Usage {
	return model.Usage{
		InputTokens:      int(u.InputTokens),
		OutputTokens:     int(u.OutputTokens),
		CacheReadTokens:  int(u.CacheReadInputTokens),
		CacheWriteTokens: int(u.CacheCreationInputTokens),
	}
}
