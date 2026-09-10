// Package catalog turns the companion's tools reply into tools the agent can
// call. Every provider on the server, the companion included, is discovered
// the same way (CONTEXT.md "Catalog"), so nothing here knows which mod a tool
// came from.
//
// Two things happen on the way through. A tool's wire name becomes
// "<iface>__<fn>" with every character the model API disallows replaced by an
// underscore, and force is injected as a required argument on every tool: any
// player may ask about any force, and a provider never declares force itself
// (CONTEXT.md "Reserved parameter").
package catalog

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"sort"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// maxNameLen is the model API's limit on a tool name.
const maxNameLen = 64

// Caller runs one tool on one provider. *rpc.Client satisfies it; tests and
// the catalog-refresh wrapper in cmd/aab use their own.
type Caller interface {
	CallTool(ctx context.Context, iface, fn string, args any) (json.RawMessage, error)
}

// Target is the provider function one catalog name resolves to.
type Target struct {
	Iface string
	Fn    string
}

// Catalog is one snapshot of what the server exposed when it was built.
// Nothing is cached beyond it: cmd/aab throws the whole catalog away and
// rebuilds it when a call reports an unknown provider, and every ten minutes
// anyway (CONTEXT.md invariant 3).
type Catalog struct {
	tools  []tools.Tool
	byName map[string]Target
}

// Build converts one tools reply into callable tools, in the order the
// companion sorted the providers.
func Build(providers rpc.ToolsReply, caller Caller) *Catalog {
	c := &Catalog{byName: map[string]Target{}}
	for _, p := range providers {
		for _, fn := range sortedKeys(p.Tools) {
			name := ToolName(p.Iface, fn)
			if prev, taken := c.byName[name]; taken {
				log.Printf("catalog: %s.%s and %s.%s both map to tool name %q, keeping the first", prev.Iface, prev.Fn, p.Iface, fn, name)
				continue
			}
			c.byName[name] = Target{Iface: p.Iface, Fn: fn}
			c.tools = append(c.tools, tool(name, p.Iface, fn, p.Tools[fn], caller))
		}
	}
	return c
}

// Tools is every tool in the snapshot.
func (c *Catalog) Tools() []tools.Tool { return c.tools }

// Target resolves a catalog name back to the provider function it came from.
func (c *Catalog) Target(name string) (Target, bool) {
	t, ok := c.byName[name]
	return t, ok
}

func tool(name, iface, fn string, manifest rpc.ToolManifest, caller Caller) tools.Tool {
	return tools.Tool{
		Name:        name,
		Description: describe(iface, fn, manifest),
		Schema:      schema(manifest),
		Call: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return caller.CallTool(ctx, iface, fn, withForce(ctx, args))
		},
	}
}

// describe is what the model reads to choose a tool: the provider's own
// description, then where it came from, so two mods offering similar tools
// stay tellable apart.
func describe(iface, fn string, manifest rpc.ToolManifest) string {
	desc := manifest.Desc
	if desc == "" {
		desc = "No description supplied by the provider."
	}
	return desc + " Read from the game live, through the " + iface + " provider's " + fn + " function."
}

var unsafeNameChar = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ToolName is the name the model sees for one provider function.
func ToolName(iface, fn string) string {
	name := unsafeNameChar.ReplaceAllString(iface+"__"+fn, "_")
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return name
}

func sortedKeys(m map[string]rpc.ToolManifest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
