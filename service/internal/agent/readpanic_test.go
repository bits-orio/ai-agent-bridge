package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// A tool that panics must not take the process down: every read of a round
// runs on its own goroutine, where nothing above it can recover.
func TestAPanickingToolFailsItsOwnReadOnly(t *testing.T) {
	byName := map[string]tools.Tool{
		"boom": {Name: "boom", Call: func(context.Context, json.RawMessage) (json.RawMessage, error) { panic("kaboom") }},
		"fine": {Name: "fine", Call: func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`"ok"`), nil }},
	}
	calls := []model.Block{
		{Type: model.BlockToolUse, ID: "a", Name: "boom", Input: json.RawMessage(`{}`)},
		{Type: model.BlockToolUse, ID: "b", Name: "fine", Input: json.RawMessage(`{}`)},
	}
	results := make([]model.Block, len(calls))
	runReads(context.Background(), calls, []int{0, 1}, results, byName, "player", 4096, nil, false)

	if !results[0].IsError || !strings.Contains(results[0].Content, "kaboom") {
		t.Errorf("the panicking read = %+v, want a failed result naming the panic", results[0])
	}
	if results[1].IsError || !strings.Contains(results[1].Content, "ok") {
		t.Errorf("the other read = %+v, want its own result untouched", results[1])
	}
}
