package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// serve answers /api/chat with reply and hands back the decoded request.
func serve(t *testing.T, status int, reply string) (*Client, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			got["_auth"] = auth
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request is not JSON: %v", err)
		}
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return New(Options{Model: "qwen3:8b", BaseURL: srv.URL + "/", Reasoning: "off"}), &got
}

var defs = []model.ToolDef{{Name: "get_production", Description: "items per minute"}}

// The reply shape is the one Ollama 0.34.0 sent on 2026-11-13.
const toolReply = `{"model":"qwen3:8b","message":{"role":"assistant","content":"","thinking":"plan",
"tool_calls":[{"id":"call_a","function":{"index":0,"name":"get_production","arguments":{"force":"player","item":"iron-plate"}}},
{"function":{"index":1,"name":"get_production","arguments":{"force":"player","item":"copper-plate"}}}]},
"done":true,"done_reason":"stop","prompt_eval_count":158,"prompt_eval_cached_count":100,"eval_count":57}`

func TestStepToolCalls(t *testing.T) {
	c, got := serve(t, 200, toolReply)
	step, err := c.Step(context.Background(), "rules", []model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "how much iron?"}}},
	}, defs)
	if err != nil {
		t.Fatal(err)
	}
	if step.StopReason != model.StopToolUse {
		t.Errorf("stop reason %q", step.StopReason)
	}
	uses := model.ToolUses(step.Blocks)
	if len(uses) != 2 || uses[0].ID != "call_a" || uses[1].ID == "" || uses[1].ID == uses[0].ID {
		t.Fatalf("tool uses %+v", uses)
	}
	if string(uses[1].Input) != `{"force":"player","item":"copper-plate"}` {
		t.Errorf("input %s", uses[1].Input)
	}
	if step.Blocks[0].Type != model.BlockReasoning || step.Blocks[0].Thinking != "plan" {
		t.Errorf("first block %+v", step.Blocks[0])
	}
	want := model.Usage{InputTokens: 58, CacheReadTokens: 100, OutputTokens: 57}
	if step.Usage != want {
		t.Errorf("usage %+v, want %+v", step.Usage, want)
	}

	req := *got
	if req["stream"] != false || req["think"] != false || req["keep_alive"] != DefaultKeepAlive {
		t.Errorf("request switches: %v %v %v", req["stream"], req["think"], req["keep_alive"])
	}
	opts := req["options"].(map[string]any)
	if opts["num_ctx"] != float64(DefaultNumCtx) || opts["num_predict"] != float64(DefaultMaxOutputTokens) {
		t.Errorf("options %v", opts)
	}
	if _, sent := req["_auth"]; sent {
		t.Error("an Authorization header went out with no key set")
	}
	msgs := req["messages"].([]any)
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "rules" {
		t.Errorf("system message %v", first)
	}
}

// A tool result carries only the id of its call; Ollama wants the name.
func TestToolResultsAreNamed(t *testing.T) {
	msgs := toMessages([]model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "q"}}},
		{Role: model.RoleAssistant, Blocks: []model.Block{
			{Type: model.BlockReasoning, Thinking: "plan"},
			{Type: model.BlockToolUse, ID: "a", Name: "aab-tools__list_forces", Input: json.RawMessage(`{}`)},
			{Type: model.BlockToolUse, ID: "b", Name: "get_production", Input: json.RawMessage(`"{\"item\":\"x\"}"`)},
		}},
		{Role: model.RoleUser, Blocks: []model.Block{
			{Type: model.BlockToolResult, ID: "a", Content: "[]"},
			{Type: model.BlockToolResult, ID: "b", Content: "12"},
			{Type: model.BlockText, Text: "one round left"},
		}},
	}, shortNames([]model.ToolDef{{Name: "aab-tools__list_forces"}, {Name: "get_production"}}))
	raw, _ := json.Marshal(msgs)
	want := `[{"role":"user","content":"q"},` +
		`{"role":"assistant","content":"","thinking":"plan","tool_calls":[{"function":{"name":"list_forces","arguments":{}}},{"function":{"name":"get_production","arguments":{"item":"x"}}}]},` +
		`{"role":"tool","content":"[]","tool_name":"list_forces"},` +
		`{"role":"tool","content":"12","tool_name":"get_production"},` +
		`{"role":"user","content":"one round left"}]`
	if string(raw) != want {
		t.Errorf("messages\n got %s\nwant %s", raw, want)
	}
}

func TestReasoningSwitch(t *testing.T) {
	for reasoning, want := range map[string]any{"off": false, "model": nil, "low": true, "high": true} {
		req := New(Options{Model: "m", Reasoning: reasoning}).request("", nil, nil)
		raw, _ := json.Marshal(req)
		var got map[string]any
		json.Unmarshal(raw, &got)
		if got["think"] != want {
			t.Errorf("reasoning %s: think %v, want %v", reasoning, got["think"], want)
		}
	}
}

func TestStopReasons(t *testing.T) {
	for reply, want := range map[string]string{
		`{"message":{"content":"hi"},"done_reason":"stop","prompt_eval_count":5}`:      model.StopEndTurn,
		`{"message":{"content":"hi th"},"done_reason":"length","prompt_eval_count":5}`: model.StopMaxTokens,
	} {
		c, _ := serve(t, 200, reply)
		step, err := c.Step(context.Background(), "", nil, nil)
		if err != nil || step.StopReason != want {
			t.Errorf("%s: stop %q err %v, want %q", reply, step.StopReason, err, want)
		}
	}
}

func TestMissingModelIsA404(t *testing.T) {
	c, _ := serve(t, 404, `{"error":"model 'qwen3:8b' not found"}`)
	_, err := c.Step(context.Background(), "", nil, nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 || !strings.Contains(he.Text, "not found") {
		t.Fatalf("err %v", err)
	}
}

// Ollama cuts a prompt that does not fit, from the front, and says nothing.
func TestCutPromptIsAnError(t *testing.T) {
	c, _ := serve(t, 200, `{"message":{"content":"There is no secret word."},"done_reason":"stop","prompt_eval_count":258,"eval_count":9}`)
	_, err := c.Step(context.Background(), strings.Repeat("filler text here. ", 600), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ollama.num_ctx") {
		t.Fatalf("err %v, want one naming ollama.num_ctx", err)
	}
}

func TestKeyGoesOutWhenSet(t *testing.T) {
	c, got := serve(t, 200, `{"message":{"content":"hi"},"prompt_eval_count":5}`)
	c.opts.APIKey = "k"
	if _, err := c.Step(context.Background(), "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if (*got)["_auth"] != "Bearer k" {
		t.Errorf("auth %v", (*got)["_auth"])
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			io.WriteString(w, `{"version":"0.34.0"}`)
		case "/api/show":
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "nope") {
				w.WriteHeader(404)
				io.WriteString(w, `{"error":"model 'nope' not found"}`)
				return
			}
			io.WriteString(w, `{"capabilities":["completion","tools","thinking"]}`)
		}
	}))
	defer srv.Close()
	info, err := New(Options{Model: "qwen3:8b", BaseURL: srv.URL}).Check(context.Background())
	if err != nil || info.Version != "0.34.0" || !info.HasTools() {
		t.Errorf("info %+v err %v", info, err)
	}
	_, err = New(Options{Model: "nope", BaseURL: srv.URL}).Check(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 {
		t.Errorf("err %v, want a 404", err)
	}
}

// TestLive runs two rounds against a real server: AAB_OLLAMA_LIVE names the
// model, AAB_OLLAMA_URL the server when it is not the local one.
func TestLive(t *testing.T) {
	name := os.Getenv("AAB_OLLAMA_LIVE")
	if name == "" {
		t.Skip("set AAB_OLLAMA_LIVE to a pulled model to run against a real Ollama")
	}
	c := New(Options{Model: name, BaseURL: os.Getenv("AAB_OLLAMA_URL"), Reasoning: "off", NumCtx: 8192})
	ctx := context.Background()
	system := "You answer questions about a factory. Use the tools; never guess a number. " + strings.Repeat("Rule: be brief. ", 300)
	tools := []model.ToolDef{{Name: "get_production", Description: "items per minute a force makes of one item",
		Schema: map[string]any{"type": "object", "required": []string{"item"},
			"properties": map[string]any{"item": map[string]any{"type": "string"}}}}}
	msgs := []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "How many iron-plate per minute?"}}}}

	first, err := c.Step(ctx, system, msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	uses := model.ToolUses(first.Blocks)
	if first.StopReason != model.StopToolUse || len(uses) == 0 {
		t.Fatalf("round 1 called no tool: %+v", first)
	}
	t.Logf("round 1: %s %s, usage %+v", uses[0].Name, uses[0].Input, first.Usage)
	results := model.Message{Role: model.RoleUser}
	for _, u := range uses {
		results.Blocks = append(results.Blocks, model.Block{Type: model.BlockToolResult, ID: u.ID, Content: `{"per_minute":1234}`})
	}
	msgs = append(msgs, model.Message{Role: model.RoleAssistant, Blocks: first.Blocks}, results)
	second, err := c.Step(ctx, system, msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("round 2: %q, usage %+v", model.TextOf(second.Blocks), second.Usage)
	if !strings.Contains(strings.ReplaceAll(model.TextOf(second.Blocks), ",", ""), "1234") {
		t.Errorf("round 2 did not use the tool result")
	}
	if second.Usage.CacheReadTokens == 0 {
		t.Errorf("round 2 read nothing from the prompt cache: %+v", second.Usage)
	}
}

// A small model calls a game tool by its function name, the way the rules
// write it, and Ollama drops a call to a name it was not shown.
func TestGameToolsGoOutUnderTheirFunctionName(t *testing.T) {
	c, got := serve(t, 200, `{"message":{"content":"","tool_calls":[{"function":{"name":"list_forces","arguments":{}}},{"function":{"name":"mod-a__rank","arguments":{}}}]},"prompt_eval_count":9}`)
	step, err := c.Step(context.Background(), "", nil, []model.ToolDef{
		{Name: "aab-tools__list_forces"}, {Name: "mod-a__rank"}, {Name: "mod-b__rank"}, {Name: "rank"}, {Name: "submit_answer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	for _, tool := range (*got)["tools"].([]any) {
		sent = append(sent, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	if want := "list_forces mod-a__rank mod-b__rank rank submit_answer"; strings.Join(sent, " ") != want {
		t.Errorf("names sent %v, want %s", sent, want)
	}
	uses := model.ToolUses(step.Blocks)
	if len(uses) != 2 || uses[0].Name != "aab-tools__list_forces" || uses[1].Name != "mod-a__rank" {
		t.Errorf("calls came back as %+v", uses)
	}
}

// A call Ollama's parser did not take arrives as text; it is still a call.
func TestLeakedCallIsRecovered(t *testing.T) {
	known := map[string]string{"submit_answer": "submit_answer", "list_forces": "aab-tools__list_forces"}
	for content, want := range map[string]string{
		"submit_answer\n{\"shape\":\"summary\",\"lines\":[\"a\"]}\n</tool_call>":               "submit_answer",
		"<tool_call>\n{\"name\":\"list_forces\",\"arguments\":{\"names\":true}}\n</tool_call>": "aab-tools__list_forces",
		"The force makes 12 iron plates a minute.":                                             "",
		".submit_answer\n{\"shape\":\"list\",\"items\":[\"a\"/min\"]}\n</tool_call>":           "submit_answer",
		"submit_answer\nis the tool I would call":                                              "",
		"unknown_tool\n{\"a\":1}\n</tool_call>":                                                "",
	} {
		step := fromResponse(&response{Message: message{Content: content}, PromptEvalCount: 7}, known)
		uses := model.ToolUses(step.Blocks)
		switch {
		case want == "" && (len(uses) != 0 || model.TextOf(step.Blocks) != content || step.StopReason != model.StopEndTurn):
			t.Errorf("%q: should have stayed text, got %+v", content, step)
		case want != "" && (len(uses) != 1 || uses[0].Name != want || step.StopReason != model.StopToolUse || model.TextOf(step.Blocks) != ""):
			t.Errorf("%q: want a call to %s, got %+v", content, want, step)
		}
	}
}
