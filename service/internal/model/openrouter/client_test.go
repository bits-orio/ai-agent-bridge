package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// serve answers with the given bodies in order, recording every request.
type fakeRouter struct {
	srv      *httptest.Server
	requests []map[string]any
	headers  []http.Header
	replies  []reply
	calls    int32
}

type reply struct {
	status int
	body   string
}

func newRouter(t *testing.T, replies ...reply) *fakeRouter {
	t.Helper()
	f := &fakeRouter{replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request is not JSON: %v\n%s", err, raw)
		}
		f.requests = append(f.requests, req)
		f.headers = append(f.headers, r.Header.Clone())
		n := int(atomic.AddInt32(&f.calls, 1)) - 1
		if n >= len(f.replies) {
			n = len(f.replies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.replies[n].status)
		_, _ = io.WriteString(w, f.replies[n].body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func client(f *fakeRouter, opts Options) *Client {
	opts.Endpoint = f.srv.URL
	if opts.Model == "" {
		opts.Model = "deepseek/deepseek-v4-pro-0813"
	}
	return New("test-key", opts)
}

const toolCallReply = `{
  "id": "gen-1", "model": "deepseek/deepseek-v4-pro-0813",
  "choices": [{"finish_reason": "tool_calls", "message": {
    "role": "assistant", "content": null,
    "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "list_forces", "arguments": "{\"force\": \"player\"}"}}]
  }}],
  "usage": {"prompt_tokens": 5300, "completion_tokens": 40, "total_tokens": 5340, "cost": 0.0031,
    "prompt_tokens_details": {"cached_tokens": 5000, "cache_write_tokens": 100},
    "completion_tokens_details": {"reasoning_tokens": 0}}
}`

var question = []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: what forces?"}}}}

var oneTool = []model.ToolDef{{Name: "list_forces", Description: "forces", Schema: map[string]any{
	"type": "object", "properties": map[string]any{"force": map[string]any{"type": "string"}}, "required": []string{"force"},
}}}

// A tool-call round: the request carries the shapes OpenRouter documents
// and the reply becomes a tool_use block with the reported usage and cost.
func TestToolCallRound(t *testing.T) {
	f := newRouter(t, reply{200, toolCallReply})
	c := client(f, Options{Reasoning: "off", CacheTTL: "1h", DataCollection: "deny", Fallbacks: []string{"deepseek/deepseek-v4.1-flash"}})

	step, err := c.Step(context.Background(), "rules", question, oneTool)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.Blocks) != 1 || step.Blocks[0].Type != model.BlockToolUse || step.Blocks[0].Name != "list_forces" || step.Blocks[0].ID != "call_1" {
		t.Errorf("blocks = %+v", step.Blocks)
	}
	if string(step.Blocks[0].Input) != `{"force": "player"}` {
		t.Errorf("input = %s", step.Blocks[0].Input)
	}
	if step.StopReason != model.StopToolUse {
		t.Errorf("stop = %q", step.StopReason)
	}
	want := model.Usage{InputTokens: 200, OutputTokens: 40, CacheReadTokens: 5000, CacheWriteTokens: 100, Cost: 0.0031}
	if step.Usage != want {
		t.Errorf("usage = %+v, want %+v", step.Usage, want)
	}

	req := f.requests[0]
	raw, _ := json.Marshal(req)
	for _, want := range []string{
		`"model":"deepseek/deepseek-v4-pro-0813"`,
		`"models":["deepseek/deepseek-v4.1-flash"]`,
		`"max_tokens":4096`,
		`"reasoning":{"enabled":false}`,
		`"provider":{"data_collection":"deny"}`,
		`"tool_choice":"auto"`,
		`"parallel_tool_calls":true`,
		`"tools":[{"function":{"description":"forces","name":"list_forces","parameters":{"properties":{"force":{"type":"string"}},"required":["force"],"type":"object"}},"type":"function"}]`,
		`{"content":[{"cache_control":{"ttl":"1h","type":"ephemeral"},"text":"rules","type":"text"}],"role":"system"}`,
		`{"content":[{"cache_control":{"type":"ephemeral"},"text":"Question: what forces?","type":"text"}],"role":"user"}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("request lacks %s:\n%s", want, raw)
		}
	}
	h := f.headers[0]
	if h.Get("Authorization") != "Bearer test-key" || h.Get("HTTP-Referer") != Referer || h.Get("X-OpenRouter-Title") != Title {
		t.Errorf("headers = %v", h)
	}
}

// The second round replays the tool call and its result the way the chat
// format wants them, and reasoning details go back verbatim.
func TestSecondRoundReplaysCallsResultsAndReasoning(t *testing.T) {
	f := newRouter(t, reply{200, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`})
	c := client(f, Options{Reasoning: "model"})
	msgs := append(question,
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{
			{Type: model.BlockReasoning, Data: `{"type":"reasoning.text","text":"count them","id":"r1","format":"anthropic-claude-v1"}`},
			{Type: model.BlockToolUse, ID: "call_1", Name: "list_forces", Input: json.RawMessage(`{"force":"player"}`)},
		}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{
			{Type: model.BlockToolResult, ID: "call_1", Content: `{"forces":[]}`},
		}},
	)
	step, err := c.Step(context.Background(), "rules", msgs, oneTool)
	if err != nil {
		t.Fatal(err)
	}
	if step.StopReason != model.StopEndTurn || model.TextOf(step.Blocks) != "done" {
		t.Errorf("step = %+v", step)
	}
	raw, _ := json.Marshal(f.requests[0]["messages"])
	for _, want := range []string{
		`{"content":null,"reasoning_details":[{"format":"anthropic-claude-v1","id":"r1","text":"count them","type":"reasoning.text"}],"role":"assistant","tool_calls":[{"function":{"arguments":"{\"force\":\"player\"}","name":"list_forces"},"id":"call_1","type":"function"}]}`,
		`{"content":"{\"forces\":[]}","role":"tool","tool_call_id":"call_1"}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("messages lack %s:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"reasoning"`) && !strings.Contains(string(raw), `"reasoning_details"`) {
		t.Errorf("a plain reasoning string was sent beside details: %s", raw)
	}
	if _, has := f.requests[0]["reasoning"]; has {
		t.Errorf("reasoning: model must send no reasoning field: %v", f.requests[0]["reasoning"])
	}
}

// Reasoning that arrives as details or as a string is kept as blocks; a
// length finish maps onto max tokens; effort levels reach the request.
func TestReasoningAndFinishReasons(t *testing.T) {
	f := newRouter(t, reply{200, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"half","reasoning":"thinking...","reasoning_details":[{"type":"reasoning.text","text":"thinking..."}]}}],"usage":{"prompt_tokens":10,"completion_tokens":900,"completion_tokens_details":{"reasoning_tokens":880}}}`})
	c := client(f, Options{Reasoning: "low"})
	step, err := c.Step(context.Background(), "rules", question, nil)
	if err != nil {
		t.Fatal(err)
	}
	if step.StopReason != model.StopMaxTokens || step.Usage.ReasoningTokens != 880 {
		t.Errorf("step = %+v", step)
	}
	if len(step.Blocks) != 2 || step.Blocks[0].Type != model.BlockReasoning || step.Blocks[0].Data == "" || step.Blocks[1].Text != "half" {
		t.Errorf("blocks = %+v", step.Blocks)
	}
	if got, _ := json.Marshal(f.requests[0]["reasoning"]); string(got) != `{"effort":"low"}` {
		t.Errorf("reasoning = %s", got)
	}
	if _, has := f.requests[0]["tools"]; has {
		t.Error("no tools were given, none must be sent")
	}
}

// A 429 is retried once; a 400 is reported with OpenRouter's message; an
// error object inside a 200 is reported too.
func TestRetryAndErrors(t *testing.T) {
	f := newRouter(t, reply{429, `{"error":{"message":"slow down","code":429}}`}, reply{200, toolCallReply})
	c := client(f, Options{})
	if _, err := c.Step(context.Background(), "rules", question, oneTool); err != nil {
		t.Fatalf("one 429 then success must succeed: %v", err)
	}
	if f.calls != 2 {
		t.Errorf("calls = %d, want 2", f.calls)
	}

	f2 := newRouter(t, reply{400, `{"error":{"message":"No endpoints found matching your data policy","code":404}}`})
	_, err := c2(f2).Step(context.Background(), "rules", question, oneTool)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "data policy") {
		t.Errorf("400 error = %v", err)
	}
	if f2.calls != 1 {
		t.Errorf("a 400 must not be retried, calls = %d", f2.calls)
	}

	f3 := newRouter(t, reply{200, `{"error":{"message":"provider fell over"}}`})
	if _, err := c2(f3).Step(context.Background(), "rules", question, oneTool); err == nil || !strings.Contains(err.Error(), "provider fell over") {
		t.Errorf("error-in-200 = %v", err)
	}

	f4 := newRouter(t, reply{503, `upstream unavailable`}, reply{503, `upstream unavailable`})
	if _, err := c2(f4).Step(context.Background(), "rules", question, oneTool); err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Errorf("503 twice = %v", err)
	}
}

func c2(f *fakeRouter) *Client { return client(f, Options{}) }

// Malformed tool arguments become an empty object rather than a crash, and
// a reply with no choices is an empty end of turn.
func TestOddReplies(t *testing.T) {
	if got := string(inputJSON(`not json`)); got != `{}` {
		t.Errorf("inputJSON(not json) = %s", got)
	}
	if got := string(inputJSON(`[1,2]`)); got != `{}` {
		t.Errorf("inputJSON(array) = %s", got)
	}
	f := newRouter(t, reply{200, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0}}`})
	step, err := client(f, Options{}).Step(context.Background(), "rules", question, nil)
	if err != nil || step.StopReason != model.StopEndTurn || len(step.Blocks) != 0 {
		t.Errorf("no-choice reply: step=%+v err=%v", step, err)
	}
}

// The upstream host is the operator's to choose: the request carries the
// preferred hosts in order and, unless fallbacks are allowed, pins to them.
// Measured on the live server, the same model answered a round in 4.2 s at
// the median on one host and 13.3 s on another, and the prompt cache is per
// host, so a switch is a cold ten-thousand-token round.
func TestProvidersPinTheUpstreamHost(t *testing.T) {
	body := func(f *fakeRouter) string {
		raw, _ := json.Marshal(f.requests[0])
		return string(raw)
	}

	f := newRouter(t, reply{200, toolCallReply})
	c := client(f, Options{DataCollection: "deny", Providers: []string{"StreamLake", "DeepSeek"}})
	if _, err := c.Step(context.Background(), "rules", question, oneTool); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"order":["StreamLake","DeepSeek"]`, `"allow_fallbacks":false`, `"data_collection":"deny"`} {
		if !strings.Contains(body(f), want) {
			t.Errorf("request lacks %s:\n%s", want, body(f))
		}
	}

	f = newRouter(t, reply{200, toolCallReply})
	c = client(f, Options{Providers: []string{"StreamLake"}, AllowFallbacks: true})
	if _, err := c.Step(context.Background(), "rules", question, oneTool); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(f), `"allow_fallbacks"`) {
		t.Errorf("allow_fallbacks true should send nothing, OpenRouter's own default:\n%s", body(f))
	}
	if !strings.Contains(body(f), `"order":["StreamLake"]`) {
		t.Errorf("request lacks the host order:\n%s", body(f))
	}

	f = newRouter(t, reply{200, toolCallReply})
	c = client(f, Options{})
	if _, err := c.Step(context.Background(), "rules", question, oneTool); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(f), `"provider"`) {
		t.Errorf("no preference set, yet a provider object was sent:\n%s", body(f))
	}
}
