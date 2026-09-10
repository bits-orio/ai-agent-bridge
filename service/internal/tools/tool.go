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

// Declares reports whether a tool's schema has a property of this name. The
// agent uses it to find the tools that take the reserved force argument,
// whichever package built them: a game tool out of the catalog and a history
// tool out of SQLite are the same shape here.
func Declares(schema map[string]any, prop string) bool {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, found := props[prop]
	return found
}

// FillString sets one string argument the caller left out and returns the
// arguments as a JSON object, so a tool called with nothing at all still gets
// a table it can index. An argument already carrying a non-empty string wins,
// and arguments that are not a JSON object are passed through untouched for
// the tool itself to refuse.
func FillString(args json.RawMessage, name, value string) json.RawMessage {
	if value == "" {
		return args
	}
	fields := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &fields); err != nil {
			return args
		}
	}
	if isString(fields[name]) {
		return args
	}
	filled, err := json.Marshal(value)
	if err != nil {
		return args
	}
	fields[name] = filled
	out, err := json.Marshal(fields)
	if err != nil {
		return args
	}
	return out
}

// isString reports whether raw is a non-empty JSON string.
func isString(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return s != ""
}
