// The request and response shapes of Ollama's /api/chat, and the conversion
// each way between them and the neutral model blocks.

package ollama

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

type request struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	Tools     []toolDef `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
	Think     *bool     `json:"think,omitempty"`
	Options   options   `json:"options"`
	KeepAlive string    `json:"keep_alive,omitempty"`
}

type options struct {
	NumCtx     int `json:"num_ctx"`
	NumPredict int `json:"num_predict"`
}

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// toolCall carries its arguments as a JSON object, not as a string, and has
// no id the docs promise: a result is matched to its call by tool name and
// order.
type toolCall struct {
	ID       string       `json:"id,omitempty"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
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

type response struct {
	Message         message `json:"message"`
	DoneReason      string  `json:"done_reason"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	PromptCached    int     `json:"prompt_eval_cached_count"` // the part of PromptEvalCount read from the server's prompt cache
	EvalCount       int     `json:"eval_count"`
	Error           string  `json:"error"`
}

// request builds one round. Reasoning maps onto think as a switch: Ollama's
// levels are per model and most models take only true or false, so low,
// medium and high all ask for thinking and "model" leaves the model's own
// default alone.
func (c *Client) request(system string, msgs []model.Message, defs []model.ToolDef) request {
	req := request{
		Model:     c.opts.Model,
		Options:   options{NumCtx: c.opts.NumCtx, NumPredict: c.opts.MaxOutput},
		KeepAlive: c.opts.KeepAlive,
	}
	if system != "" {
		req.Messages = append(req.Messages, message{Role: "system", Content: system})
	}
	short := shortNames(defs)
	req.Messages = append(req.Messages, toMessages(msgs, short)...)
	if len(defs) > 0 {
		req.Tools = toToolDefs(defs, short)
	}
	switch c.opts.Reasoning {
	case "off":
		off := false
		req.Think = &off
	case "low", "medium", "high":
		on := true
		req.Think = &on
	}
	return req
}

// shortNames maps each tool's name to the name Ollama is shown. A game tool
// is named <interface>__<function>, and the rules speak of it by the
// function alone. A small local model calls it the way the rules do, and
// Ollama drops a call to a name it was not given without a word: measured
// on qwen3:8b, ai-agent-bridge-tools__list_forces was never called and
// list_forces always was. So the prefix comes off wherever the function
// name alone still names one tool.
func shortNames(defs []model.ToolDef) map[string]string {
	count := map[string]int{}
	for _, d := range defs {
		count[d.Name]++
		if s := afterPrefix(d.Name); s != d.Name {
			count[s]++
		}
	}
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		out[d.Name] = d.Name
		if s := afterPrefix(d.Name); count[s] == 1 {
			out[d.Name] = s
		}
	}
	return out
}

func afterPrefix(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 && i+2 < len(name) {
		return name[i+2:]
	}
	return name
}

// shown is the name Ollama sees for a tool; a tool no longer in the catalog
// keeps its own.
func shown(short map[string]string, name string) string {
	if s, ok := short[name]; ok {
		return s
	}
	return name
}

// toMessages converts the conversation. Each tool result becomes its own
// tool message, in order, named after the call it answers: the neutral
// block knows the call's id, so the name is looked up from the assistant
// turns before it.
func toMessages(msgs []model.Message, short map[string]string) []message {
	var out []message
	names := map[string]string{}
	for _, m := range msgs {
		if m.Role == model.RoleAssistant {
			out = append(out, assistantMessage(m, names, short))
			continue
		}
		var text []string
		for _, b := range m.Blocks {
			switch b.Type {
			case model.BlockText:
				if b.Text != "" {
					text = append(text, b.Text)
				}
			case model.BlockToolResult:
				out = append(out, message{Role: "tool", ToolName: names[b.ID], Content: b.Content})
			}
		}
		if len(text) > 0 {
			out = append(out, message{Role: "user", Content: strings.Join(text, "\n")})
		}
	}
	return out
}

func assistantMessage(m model.Message, names, short map[string]string) message {
	out := message{Role: "assistant"}
	var texts []string
	for _, b := range m.Blocks {
		switch b.Type {
		case model.BlockText:
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case model.BlockToolUse:
			names[b.ID] = shown(short, b.Name)
			out.ToolCalls = append(out.ToolCalls, toolCall{
				Function: functionCall{Name: names[b.ID], Arguments: argumentsObject(b.Input)},
			})
		case model.BlockReasoning:
			if b.Thinking != "" {
				out.Thinking = b.Thinking
			}
		}
	}
	out.Content = strings.Join(texts, "\n")
	return out
}

// argumentsObject makes sure a tool call's arguments are a JSON object.
// Ollama sends an object; a string holding one is unwrapped, and anything
// else becomes the empty object.
func argumentsObject(raw json.RawMessage) json.RawMessage {
	var probe map[string]any
	if json.Unmarshal(raw, &probe) == nil && probe != nil {
		return raw
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && json.Unmarshal([]byte(text), &probe) == nil && probe != nil {
		return json.RawMessage(text)
	}
	return json.RawMessage(`{}`)
}

func toToolDefs(defs []model.ToolDef, short map[string]string) []toolDef {
	out := make([]toolDef, 0, len(defs))
	for _, d := range defs {
		params := d.Schema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, toolDef{Type: "function", Function: functionDef{
			Name: short[d.Name], Description: d.Description, Parameters: params,
		}})
	}
	return out
}

// fromResponse turns the reply into a Step. Older servers send no call ids,
// so a call without one gets one here, unique within the question because
// the prompt token count grows every round. A call comes back under the
// name Ollama was shown; full gives it its own name again.
func fromResponse(r *response, full map[string]string) model.Step {
	var step model.Step
	if len(r.Message.ToolCalls) == 0 {
		if call, ok := leakedCall(r.Message.Content, full); ok {
			r.Message.ToolCalls, r.Message.Content = []toolCall{call}, ""
		}
	}
	if r.Message.Thinking != "" {
		step.Blocks = append(step.Blocks, model.Block{Type: model.BlockReasoning, Thinking: r.Message.Thinking})
	}
	if r.Message.Content != "" {
		step.Blocks = append(step.Blocks, model.Block{Type: model.BlockText, Text: r.Message.Content})
	}
	for i, call := range r.Message.ToolCalls {
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", r.PromptEvalCount, i)
		}
		name := call.Function.Name
		if f, ok := full[name]; ok {
			name = f
		}
		step.Blocks = append(step.Blocks, model.Block{
			Type: model.BlockToolUse, ID: id, Name: name,
			Input: argumentsObject(call.Function.Arguments),
		})
	}
	switch {
	case len(r.Message.ToolCalls) > 0:
		step.StopReason = model.StopToolUse
	case r.DoneReason == "length":
		step.StopReason = model.StopMaxTokens
	default:
		step.StopReason = model.StopEndTurn
	}
	cached := r.PromptCached
	if cached > r.PromptEvalCount {
		cached = r.PromptEvalCount
	}
	step.Usage = model.Usage{
		InputTokens:     r.PromptEvalCount - cached,
		CacheReadTokens: cached,
		OutputTokens:    r.EvalCount,
	}
	return step
}

// cutBytesPerToken is the ratio above which a prompt was certainly cut. A
// request runs four to five bytes a token, JSON and all. Measured on 0.34.0:
// a 10,000-byte prompt sent with num_ctx 512 came back as 258 prompt tokens,
// forty bytes each, with HTTP 200 and the system prompt gone.
const cutBytesPerToken = 8

// wasCut says whether Ollama dropped part of the prompt to fit the context.
// It does that from the front, rules first, and reports nothing, so the
// only sign is a prompt token count far too small for the bytes sent.
func wasCut(requestBytes int, r *response) bool {
	return r.PromptEvalCount > 0 && requestBytes > 2000 && requestBytes/r.PromptEvalCount > cutBytesPerToken
}

// leakedCall finds a tool call that came back as text. A small model now
// and then writes a call Ollama's parser does not take, and the whole of it
// arrives as content. Seen on qwen3:8b, 2026-11-13, in four of ten
// questions, with and without the leading dot:
//
//	.submit_answer
//	{"shape":"summary","lines":["..."]}
//	</tool_call>
//
// The other form is the model's own, {"name":...,"arguments":{...}} between
// tool_call tags. Only a known tool followed by a brace counts; anything
// else stays the text it was. Arguments that are not valid JSON go on as
// the empty object, so the tool refuses the call and the model tries again,
// which beats printing the wreck to chat as the answer.
func leakedCall(content string, known map[string]string) (toolCall, bool) {
	body := strings.TrimSpace(content)
	body = strings.TrimSpace(strings.TrimPrefix(body, "<tool_call>"))
	body = strings.TrimSpace(strings.TrimSuffix(body, "</tool_call>"))
	var wrapped struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	name, args := "", ""
	if json.Unmarshal([]byte(body), &wrapped) == nil && wrapped.Name != "" {
		name, args = wrapped.Name, string(wrapped.Arguments)
	} else if first, rest, found := strings.Cut(body, "\n"); found {
		name, args = strings.Trim(first, " .:`\t\r"), strings.TrimSpace(rest)
	}
	if _, ok := known[name]; !ok || !strings.HasPrefix(args, "{") {
		return toolCall{}, false
	}
	return toolCall{Function: functionCall{Name: name, Arguments: json.RawMessage(args)}}, true
}
