// The probe's parameter grammar and the reserved force argument.
//
// A manifest entry describes each parameter as "<type>[!] <description>",
// with type one of string, integer, number or boolean and a trailing "!"
// meaning required (PLAN.md, Probe). Anything else is taken as a string whose
// description is the whole line, so a provider with a typo still exposes a
// usable tool.

package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// ForceParam is the reserved argument the service injects on every tool.
const ForceParam = "force"

const forceDesc = "Force name."

var paramTypes = map[string]string{
	"string":  "string",
	"integer": "integer",
	"number":  "number",
	"boolean": "boolean",
}

// schema builds the JSON Schema for one tool's arguments, with force added as
// a required string.
func schema(manifest rpc.ToolManifest) map[string]any {
	props := map[string]any{
		ForceParam: map[string]any{"type": "string", "description": forceDesc},
	}
	required := []string{ForceParam}
	for name, spec := range manifest.Params {
		if name == ForceParam {
			continue // reserved: the provider's own declaration never wins
		}
		prop, req := parseParam(spec)
		props[name] = prop
		if req {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return tools.ObjectSchema(props, required...)
}

// parseParam turns one "<type>[!] <description>" line into a schema property
// and whether the parameter is required.
func parseParam(spec string) (map[string]any, bool) {
	head, rest, _ := strings.Cut(strings.TrimSpace(spec), " ")
	required := strings.HasSuffix(head, "!")
	kind, known := paramTypes[strings.TrimSuffix(head, "!")]
	if !known {
		// Not a type word: the provider wrote a bare description.
		return map[string]any{"type": "string", "description": strings.TrimSpace(spec)}, false
	}
	return map[string]any{"type": kind, "description": strings.TrimSpace(rest)}, required
}

// forceKey carries the asker's force down to the tool call.
type forceKey struct{}

// WithForce marks ctx with the force to read when the model leaves force out.
// The agent sets it once per question; every tool call under that context
// inherits it.
func WithForce(ctx context.Context, force string) context.Context {
	return context.WithValue(ctx, forceKey{}, force)
}

func forceFrom(ctx context.Context) string {
	force, _ := ctx.Value(forceKey{}).(string)
	return force
}

// withForce fills force in when the model omitted it, and guarantees a JSON
// object even for a tool the model called with no arguments at all. The
// companion's tools index their argument table directly, so nil would error
// inside the provider.
func withForce(ctx context.Context, args json.RawMessage) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &fields)
	}
	if !hasString(fields[ForceParam]) {
		if force := forceFrom(ctx); force != "" {
			forceJSON, err := json.Marshal(force)
			if err == nil {
				fields[ForceParam] = forceJSON
			}
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}

// hasString reports whether raw is a non-empty JSON string.
func hasString(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return s != ""
}
