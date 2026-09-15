// The probe's parameter grammar and the reserved force argument.
//
// A manifest entry describes each parameter as "<type>[!] <description>",
// with type one of string, integer, number, boolean, list<string>,
// list<number>, list<point> (a point is {x, y, surface?}) or
// enum<v1,v2,...> (D2: a closed vocabulary, e.g. "enum<force,surface,
// platform,player>"), and a trailing "!" meaning required (PLAN.md, Probe;
// phase4-spec.md §5). Anything else is taken as a string whose description
// is the whole line, so a provider with a typo, or a type word from a
// grammar this build predates, still exposes a usable tool: the parameter
// reaches the model, just typed as a string instead of whatever it was
// meant to be.
//
// enum<...> reuses the bracketed shape list<...> already established rather
// than inventing a second syntax family, so one look at parseParam finds
// both. It reaches the model as a real JSON Schema "enum", enforced by the
// tool-calling layer, not as prose the model can misread the way a metric
// name typed into a bare string param could be (docs/design/
// phase5-sweep.md, "a hallucinated metric name is not a schema violation").
// It is backward-safe in both directions. An older service reading
// "enum<...>" does not know the word: paramTypes has no entry for it, so it
// falls straight into the existing unknown-type branch below and degrades to
// a plain string carrying the whole spec line, exactly the promise this
// grammar already made for a word from a newer build (phase4-spec.md §5). A
// newer service reading a param with no enum<...> at all takes every
// existing path exactly as before; enumValues only ever fires on that one
// prefix.

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
	word := strings.TrimSuffix(head, "!")

	if items, ok := listItemSchema(word); ok {
		return map[string]any{"type": "array", "items": items, "description": strings.TrimSpace(rest)}, required
	}
	if values, ok := enumValues(word); ok {
		return map[string]any{"type": "string", "enum": values, "description": strings.TrimSpace(rest)}, required
	}
	kind, known := paramTypes[word]
	if !known {
		// Not a type word: the provider wrote a bare description. This is also
		// the fallback a type word from a NEWER grammar than this build knows
		// hits: it degrades to a plain string instead of failing the whole
		// entry, which is what lets the grammar ship ahead of the tools that
		// use it (phase4-spec.md §5).
		return map[string]any{"type": "string", "description": strings.TrimSpace(spec)}, false
	}
	return map[string]any{"type": kind, "description": strings.TrimSpace(rest)}, required
}

// listItemSchema reports the JSON Schema for one element of a list<...>
// parameter, and whether word names one at all. list<string> and
// list<number> emit a bare scalar array; list<point> emits an array of
// objects instead, since a point is {x, y, surface?}: x and y required on
// every point, surface optional, matching the "?" on only one of the three
// in that shape (phase4-spec.md §5).
func listItemSchema(word string) (map[string]any, bool) {
	switch word {
	case "list<string>":
		return map[string]any{"type": "string"}, true
	case "list<number>":
		return map[string]any{"type": "number"}, true
	case "list<point>":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x":       map[string]any{"type": "number"},
				"y":       map[string]any{"type": "number"},
				"surface": map[string]any{"type": "string"},
			},
			"required": []string{"x", "y"},
		}, true
	default:
		return nil, false
	}
}

// enumValues reports the closed vocabulary word "enum<v1,v2,...>" declares,
// and whether word is one at all. Values are split on commas and trimmed; an
// empty value, from a stray "enum<>" or a trailing comma, is dropped rather
// than reaching the model as a blank choice. A word with no values left
// after that is not treated as an enum at all: parseParam's fallback then
// keeps the whole original spec line as a string, the same way it would for
// "enum<>" today, so a malformed declaration loses only its type, never the
// parameter.
func enumValues(word string) ([]string, bool) {
	const prefix, suffix = "enum<", ">"
	if !strings.HasPrefix(word, prefix) || !strings.HasSuffix(word, suffix) {
		return nil, false
	}
	inner := word[len(prefix) : len(word)-len(suffix)]
	var values []string
	for _, v := range strings.Split(inner, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

// describe is what the model reads to choose a tool: a short cost prefix
// ahead of the provider's own description. The provider and function already
// sit in the tool's name, and every word here is sent on every round of every
// question, so nothing else is appended.
func describe(manifest rpc.ToolManifest) string {
	desc := manifest.Desc
	if desc == "" {
		desc = "No description supplied by the provider."
	}
	return tierPrefix(string(manifest.Tier)) + desc
}

// tierPrefix is the cost label the model reads before a tool's own
// description, "[tier 1, swept] " or "[tier 2, bounded] ". tier has no schema
// slot of its own: it never reaches the tool's arguments, only this prefix
// (phase4-spec.md §5).
//
// tier is optional on the wire and stays that way. An entry that has not
// shipped it, and one carrying a value that is neither "1" nor "2", both read
// as tier 1, the cheaper and more common case: never a dropped tool, never a
// field the catalog step requires. mts-v1 and every other provider that has
// not shipped tier yet keeps defaulting to it forever; there is no migration
// to plan for.
//
// The one-sentence explanation of what the prefix means is not repeated here
// on every tool; it belongs once, in the system prompt, verbatim:
//
//	Each tool says whether it is cheap or costly. A cheap tool reads counters
//	the game already keeps and covers every force in one call. A costly tool
//	walks the map, so it covers one force at a time and needs a place to
//	start.
func tierPrefix(tier string) string {
	if tier == "2" {
		return "[tier 2, bounded] "
	}
	return "[tier 1, swept] "
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
