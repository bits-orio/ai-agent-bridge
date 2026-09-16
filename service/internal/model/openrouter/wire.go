// The request and response shapes, and the conversion each way between
// them and the neutral model blocks.

package openrouter

import (
	"encoding/json"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

type request struct {
	Model             string     `json:"model"`
	Models            []string   `json:"models,omitempty"`
	Messages          []message  `json:"messages"`
	Tools             []toolDef  `json:"tools,omitempty"`
	ToolChoice        string     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool      `json:"parallel_tool_calls,omitempty"`
	MaxTokens         int        `json:"max_tokens"`
	Reasoning         *reasoning `json:"reasoning,omitempty"`
	Provider          *routing   `json:"provider,omitempty"`
}

type message struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"` // a string, or []part when a part carries cache_control
	ToolCalls        []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type part struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDef struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type reasoning struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

type routing struct {
	DataCollection string   `json:"data_collection,omitempty"`
	Order          []string `json:"order,omitempty"`           // upstreams to try, in this order
	AllowFallbacks *bool    `json:"allow_fallbacks,omitempty"` // false pins the request to Order alone
}

type response struct {
	Provider string `json:"provider"` // the upstream host OpenRouter routed to, e.g. "DeepSeek"
	Choices  []struct {
		Message struct {
			Content          *string           `json:"content"`
			ToolCalls        []toolCall        `json:"tool_calls"`
			Reasoning        string            `json:"reasoning"`
			ReasoningDetails []json.RawMessage `json:"reasoning_details"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int     `json:"prompt_tokens"`
		CompletionTokens    int     `json:"completion_tokens"`
		Cost                float64 `json:"cost"`
		PromptTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	rawError string
}

func (r *response) errorText() string {
	if r == nil {
		return ""
	}
	if r.Error != nil && r.Error.Message != "" {
		return r.Error.Message
	}
	return strings.TrimSpace(r.rawError)
}

// request builds one round. Two cache breakpoints, as on the direct client:
// the system prompt with the operator's TTL (tools are cached with it) and
// the last user text part, five minutes. Routes that cache by themselves
// ignore both. Reasoning is off unless asked for: a model that reasons
// about "what forces are there" spends most of the output bill on it.
func (c *Client) request(system string, msgs []model.Message, defs []model.ToolDef) request {
	req := request{
		Model:     c.opts.Model,
		Models:    c.opts.Fallbacks,
		MaxTokens: c.opts.MaxOutput,
	}
	if system != "" {
		req.Messages = append(req.Messages, message{Role: "system", Content: []part{{
			Type: "text", Text: system, CacheControl: c.systemCache(),
		}}})
	}
	req.Messages = append(req.Messages, toMessages(msgs)...)
	if len(defs) > 0 {
		req.Tools = toToolDefs(defs)
		req.ToolChoice = "auto"
		parallel := true
		req.ParallelToolCalls = &parallel
	}
	switch c.opts.Reasoning {
	case "off":
		off := false
		req.Reasoning = &reasoning{Enabled: &off}
	case "low", "medium", "high":
		req.Reasoning = &reasoning{Effort: c.opts.Reasoning}
	}
	// OpenRouter picks the upstream host for a model unless told otherwise,
	// and for the same model the hosts differ threefold: on the live server
	// StreamLake answered a round in 4.2 s at the median and Ionstream in
	// 13.3 s, with a 35 s tail, and the prompt cache is per host, so every
	// switch between them is a cold 10,000-token round. Providers names the
	// hosts to prefer, in order; AllowFallbacks false refuses every other.
	if c.opts.DataCollection != "" || len(c.opts.Providers) > 0 {
		req.Provider = &routing{DataCollection: c.opts.DataCollection, Order: c.opts.Providers}
		if len(c.opts.Providers) > 0 && !c.opts.AllowFallbacks {
			no := false
			req.Provider.AllowFallbacks = &no
		}
	}
	return req
}

func (c *Client) systemCache() *cacheControl {
	cc := &cacheControl{Type: "ephemeral"}
	if c.opts.CacheTTL == "1h" {
		cc.TTL = "1h"
	}
	return cc
}

// toMessages converts the conversation. A user turn's text becomes one
// user message; each tool result becomes its own tool message, in order,
// which is how the chat format wants results delivered. The last user text
// part carries the per-round breakpoint.
func toMessages(msgs []model.Message) []message {
	var out []message
	lastUser := -1
	for i, m := range msgs {
		if m.Role == model.RoleUser {
			lastUser = i
		}
	}
	for i, m := range msgs {
		if m.Role == model.RoleAssistant {
			out = append(out, assistantMessage(m))
			continue
		}
		var text []part
		for _, b := range m.Blocks {
			switch b.Type {
			case model.BlockText:
				if b.Text != "" {
					text = append(text, part{Type: "text", Text: b.Text})
				}
			case model.BlockToolResult:
				out = append(out, message{Role: "tool", ToolCallID: b.ID, Content: b.Content})
			}
		}
		if len(text) > 0 {
			if i == lastUser {
				text[len(text)-1].CacheControl = &cacheControl{Type: "ephemeral"}
			}
			out = append(out, message{Role: "user", Content: text})
		}
	}
	return out
}

func assistantMessage(m model.Message) message {
	out := message{Role: "assistant"}
	var texts []string
	var details []json.RawMessage
	for _, b := range m.Blocks {
		switch b.Type {
		case model.BlockText:
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case model.BlockToolUse:
			out.ToolCalls = append(out.ToolCalls, toolCall{
				ID: b.ID, Type: "function",
				Function: functionCall{Name: b.Name, Arguments: argumentsText(b.Input)},
			})
		case model.BlockReasoning:
			if len(b.Data) > 0 {
				details = append(details, json.RawMessage(b.Data))
			} else if b.Thinking != "" {
				out.Reasoning = b.Thinking
			}
		}
	}
	// The chat format wants a content string, null when there was none.
	if len(texts) > 0 {
		out.Content = strings.Join(texts, "\n")
	}
	if len(details) > 0 {
		raw, _ := json.Marshal(details)
		out.ReasoningDetails = raw
		out.Reasoning = ""
	}
	return out
}

func argumentsText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func toToolDefs(defs []model.ToolDef) []toolDef {
	out := make([]toolDef, 0, len(defs))
	for _, d := range defs {
		params := d.Schema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, toolDef{Type: "function", Function: functionDef{
			Name: d.Name, Description: d.Description, Parameters: params,
		}})
	}
	return out
}

// fromResponse turns the first choice into a Step. Reasoning entries are
// kept raw and in order; the loop hands them back untouched next round.
func fromResponse(r *response) model.Step {
	step := model.Step{Provider: r.Provider}
	if len(r.Choices) == 0 {
		step.StopReason = model.StopEndTurn
		step.Usage = usageOf(r)
		return step
	}
	choice := r.Choices[0]
	for _, d := range choice.Message.ReasoningDetails {
		step.Blocks = append(step.Blocks, model.Block{Type: model.BlockReasoning, Data: string(d)})
	}
	if len(choice.Message.ReasoningDetails) == 0 && choice.Message.Reasoning != "" {
		step.Blocks = append(step.Blocks, model.Block{Type: model.BlockReasoning, Thinking: choice.Message.Reasoning})
	}
	if choice.Message.Content != nil && *choice.Message.Content != "" {
		step.Blocks = append(step.Blocks, model.Block{Type: model.BlockText, Text: *choice.Message.Content})
	}
	for _, call := range choice.Message.ToolCalls {
		step.Blocks = append(step.Blocks, model.Block{
			Type: model.BlockToolUse, ID: call.ID, Name: call.Function.Name,
			Input: inputJSON(call.Function.Arguments),
		})
	}
	switch choice.FinishReason {
	case "tool_calls":
		step.StopReason = model.StopToolUse
	case "stop", "":
		step.StopReason = model.StopEndTurn
	case "length":
		step.StopReason = model.StopMaxTokens
	default:
		step.StopReason = choice.FinishReason
	}
	if step.StopReason == model.StopEndTurn && len(choice.Message.ToolCalls) > 0 {
		// Some routes say "stop" with tool calls attached; the calls decide.
		step.StopReason = model.StopToolUse
	}
	step.Usage = usageOf(r)
	return step
}

// inputJSON makes sure a tool call's arguments are a JSON object: the
// arguments arrive as a string that is usually an object, sometimes empty.
func inputJSON(args string) json.RawMessage {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(trimmed)
}

// usageOf maps OpenRouter's usage onto the neutral counters. prompt_tokens
// is the whole prompt; the cached and cache-write parts are subtracted so
// the four counters stay disjoint the way the direct client reports them.
func usageOf(r *response) model.Usage {
	u := r.Usage
	fresh := u.PromptTokens - u.PromptTokensDetails.CachedTokens - u.PromptTokensDetails.CacheWriteTokens
	if fresh < 0 {
		fresh = 0
	}
	return model.Usage{
		InputTokens:      fresh,
		OutputTokens:     u.CompletionTokens,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: u.PromptTokensDetails.CacheWriteTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
		Cost:             u.Cost,
	}
}
