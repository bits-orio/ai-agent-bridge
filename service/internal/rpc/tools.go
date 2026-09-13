// The one-reply catalog op. It stays for small servers and for the harness; a
// server with several providers reads the catalog through providers and manifest
// instead (see providers.go).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// ToolManifest is one function entry in a provider's agent_tools_v1 manifest: a
// description and, for tools that take arguments, a params grammar
// ("<type>[!] <description>" per param, PLAN.md § Probe).
//
// Tier is the entry's own optional cost label, "1" or "2" (phase4-spec.md
// §5), sitting beside desc and params rather than inside the params grammar.
// It stays optional forever: a provider that has not shipped it yet, mts-v1
// included, decodes with an empty Tier rather than losing the entry, and
// service/internal/catalog reads that the same as an explicit "1".
type ToolManifest struct {
	Desc   string            `json:"desc"`
	Params map[string]string `json:"params,omitempty"`
	Tier   Tier              `json:"tier,omitempty"`
}

// Tier is a manifest entry's cost label on the wire. It decodes from a JSON
// string or a JSON number, and from anything else as empty, because the
// alternative is losing a provider whole.
//
// Lua has one number type and helpers.table_to_json writes `tier = 1` as the
// number 1, while a provider author reading the spec might just as reasonably
// write "1". A plain string field rejects the number, and a manifest decodes
// as one object: one entry's wrong type fails the whole Unmarshal, the
// provider is skipped, and every tool it owns disappears from the catalog.
// That is the exact deletion the optional-tier rule exists to prevent,
// arriving through the field's type instead of its absence.
type Tier string

func (t *Tier) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = Tier(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*t = Tier(n.String())
		return nil
	}
	// Anything else is a label nobody can read, which the catalog already
	// treats as tier 1. Never an error: a bad label must not cost a tool.
	*t = ""
	return nil
}

// Provider is one provider's full entry in the catalog: interface name, probe
// version, and manifest, verbatim (PLAN.md says "manifests verbatim").
type Provider struct {
	Iface string                  `json:"iface"`
	V     int                     `json:"v"`
	Tools map[string]ToolManifest `json:"tools"`
}

// ToolsReply is the "r" of a tools call: the sorted list of providers (PLAN.md).
type ToolsReply []Provider

// Tools calls the tools op ({}) and returns the whole catalog from one reply, one
// provider at a time.
//
// Each entry is decoded on its own so a third mod that writes a plausible but
// wrong manifest costs itself its tools and nobody else's (review-fix contract
// 6). Decoding the whole array in one pass meant a list of tool names where a
// map belonged, a nested params table, or a localised desc took every tool on
// the server down with it, the companion's own included, and the agent then
// answered every question from the prompt alone.
func (c *Client) Tools(ctx context.Context) (ToolsReply, error) {
	raw, err := c.Call(ctx, "tools", nil)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if err := unmarshalList(raw, &entries); err != nil {
		return nil, fmt.Errorf("aab-rpc: tools: bad reply: %w", err)
	}

	out := make(ToolsReply, 0, len(entries))
	for _, entry := range entries {
		var provider Provider
		if err := json.Unmarshal(entry, &provider); err != nil {
			log.Printf("tools: skipping provider %s, its manifest did not decode: %v", ifaceName(entry), err)
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}

// ifaceName digs the iface field out of a provider entry that failed to decode,
// so the log line names the mod whose manifest needs fixing.
func ifaceName(entry json.RawMessage) string {
	var named struct {
		Iface string `json:"iface"`
	}
	if err := json.Unmarshal(entry, &named); err == nil && named.Iface != "" {
		return named.Iface
	}
	return "(unnamed)"
}
