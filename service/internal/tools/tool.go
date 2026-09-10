// Package tools defines the one shape every tool the agent can call takes,
// whether it reads the game through the companion, reads history from
// SQLite, or ends the loop by submitting an answer. The agent never knows
// which kind it is holding.
package tools

import (
	"context"
	"encoding/json"
)

// Tool is one bounded operation the model may call.
type Tool struct {
	// Name is the exact name the model sees. Letters, digits, underscores and
	// hyphens only, at most 64 characters.
	Name string
	// Description is what the model reads to decide whether to call it. Write
	// two or three full sentences: what it returns, what the arguments mean,
	// when to prefer another tool.
	Description string
	// Schema is a JSON Schema object for the arguments:
	// {"type":"object","properties":{...},"required":[...],"additionalProperties":false}.
	Schema map[string]any
	// Call runs the tool. A returned error is reported to the model as an
	// error tool result and never aborts the question.
	Call func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// ObjectSchema builds a Schema from property definitions and the list of
// required property names.
func ObjectSchema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}
