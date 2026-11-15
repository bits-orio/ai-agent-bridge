package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

const okReply = `{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

// forcedBody runs one forced step against a stub and returns the request body.
func forcedBody(t *testing.T, thinking string) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okReply)
	}))
	defer srv.Close()
	c := New("k", Options{Model: "claude-x", Endpoint: srv.URL, Thinking: thinking})
	defs := []model.ToolDef{{Name: "submit_answer", Description: "d", Schema: map[string]any{"type": "object"}}}
	msgs := []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "q"}}}}
	if _, err := c.StepForced(context.Background(), "rules", msgs, defs, "submit_answer"); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStepForcedSendsToolChoice(t *testing.T) {
	tc, _ := forcedBody(t, "off")["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "submit_answer" {
		t.Errorf("tool_choice = %v, want type tool name submit_answer", tc)
	}
}

// The API rejects a forced tool choice while thinking is on.
func TestStepForcedWithThinkingStaysAuto(t *testing.T) {
	for _, th := range []string{"adaptive", "", "model"} {
		if tc, ok := forcedBody(t, th)["tool_choice"]; ok {
			t.Errorf("thinking %q: tool_choice = %v, want none", th, tc)
		}
	}
}
