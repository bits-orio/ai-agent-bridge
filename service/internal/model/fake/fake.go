// Package fake is a scripted model. It calls one tool, then submits an
// artifact carrying what that tool returned, picking both from keywords in
// the question.
//
// It exists so the end-to-end harness can drive the whole loop against a real
// Factorio server with no API key and no network, and get the same answer
// every time. Select it by setting the model to "fake" in the config.
package fake

import (
	"context"
	"fmt"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// ModelID is the model name that selects this model in the config.
const ModelID = "fake"

// Tokens reported per step, so a test can assert on usage and cost arithmetic
// without a live API.
const (
	inputTokensPerStep  = 100
	outputTokensPerStep = 20
)

// Model is the scripted model. It holds no state: every decision is read back
// out of the conversation it is handed.
type Model struct{}

func New() *Model { return &Model{} }

func (m *Model) Name() string { return ModelID }

// Step plays one of two scripted turns. The first calls the tool the question
// asks for; the second submits an artifact whose first line or row is the
// tool result, so a test can assert on real game data.
func (m *Model) Step(_ context.Context, _ string, msgs []model.Message, defs []model.ToolDef) (model.Step, error) {
	turn := assistantTurns(msgs) + 1
	text := questionOf(msgs)
	id := fmt.Sprintf("fake-%d", turn)

	if turn == 1 {
		if has(text, "recall") {
			return step(submitBlock(id, recall(msgs))), nil
		}
		if c := firstCall(text, defs); c != nil {
			return step(model.Block{
				Type: model.BlockToolUse, ID: id, Name: c.tool, Input: mustJSON(c.args),
			}), nil
		}
		return step(submitBlock(id, echo(text))), nil
	}
	return step(submitBlock(id, artifactOf(text, lastToolResult(msgs)))), nil
}

func step(block model.Block) model.Step {
	return model.Step{
		Blocks:     []model.Block{block},
		StopReason: model.StopToolUse,
		Usage:      model.Usage{InputTokens: inputTokensPerStep, OutputTokens: outputTokensPerStep},
	}
}

func submitBlock(id string, artifact map[string]any) model.Block {
	return model.Block{Type: model.BlockToolUse, ID: id, Name: "submit_answer", Input: mustJSON(artifact)}
}

// recall answers "recall" with what the session carried into the prompt:
// how many earlier exchanges, and the first of them. The harness uses it to
// see a session from outside.
func recall(msgs []model.Message) map[string]any {
	context := contextOf(msgs)
	n := strings.Count(context, " asked: ")
	lines := []string{fmt.Sprintf("earlier=%d", n)}
	if i := strings.Index(context, " asked: "); i >= 0 {
		first := context[i+len(" asked: "):]
		if j := strings.Index(first, "\n"); j >= 0 {
			first = first[:j]
		}
		lines = append(lines, "first="+clip(first))
	}
	return map[string]any{"shape": "summary", "lines": lines}
}

// contextOf is the user text before the question itself: the session
// transcript when there is one.
func contextOf(msgs []model.Message) string {
	for _, m := range msgs {
		if m.Role != model.RoleUser {
			continue
		}
		for _, b := range m.Blocks {
			if b.Type != model.BlockText {
				continue
			}
			if i := strings.LastIndex(b.Text, "Question: "); i >= 0 {
				return b.Text[:i]
			}
			return ""
		}
	}
	return ""
}

// echo is the answer to a question that matched no keyword at all.
func echo(text string) map[string]any {
	return map[string]any{"shape": "summary", "lines": []string{"You asked: " + clip(text)}}
}

// artifactOf builds the second turn's artifact: the shape the question asked
// for, carrying the compacted tool result.
func artifactOf(text, result string) map[string]any {
	body := clip(compact(result))
	if body == "" {
		body = "no tool result"
	}
	switch shapeOf(text) {
	case "table":
		return map[string]any{"shape": "table", "columns": []string{"result"}, "rows": [][]string{{body}}}
	case "list":
		return map[string]any{"shape": "list", "items": []string{body}}
	case "comparison":
		return map[string]any{
			"shape":   "comparison",
			"columns": []string{"a", "b"},
			"rows":    []map[string]string{{"label": "result", "a": body, "b": ""}},
		}
	case "notice":
		return map[string]any{"shape": "notice", "text": body}
	default:
		return map[string]any{"shape": "summary", "lines": []string{body}}
	}
}

func shapeOf(text string) string {
	switch {
	case has(text, "table"):
		return "table"
	case has(text, "list"):
		return "list"
	case has(text, "compare"):
		return "comparison"
	case has(text, "notice"):
		return "notice"
	default:
		return "summary"
	}
}

func assistantTurns(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == model.RoleAssistant {
			n++
		}
	}
	return n
}

// questionOf pulls the question out of the first user turn. The agent may
// have prefixed earlier exchanges, so anything before the last "Question: "
// marker is ignored.
func questionOf(msgs []model.Message) string {
	for _, m := range msgs {
		if m.Role != model.RoleUser {
			continue
		}
		for _, b := range m.Blocks {
			if b.Type != model.BlockText {
				continue
			}
			if i := strings.LastIndex(b.Text, "Question: "); i >= 0 {
				return b.Text[i+len("Question: "):]
			}
			return b.Text
		}
	}
	return ""
}

func lastToolResult(msgs []model.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		for j := len(msgs[i].Blocks) - 1; j >= 0; j-- {
			if b := msgs[i].Blocks[j]; b.Type == model.BlockToolResult {
				return b.Content
			}
		}
	}
	return ""
}
