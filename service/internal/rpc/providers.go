// The catalog in two steps: which providers are there, then one manifest each.
//
// One tools reply puts every provider on the server under one byte cap, so the
// most verbose mod on the server decided whether anybody's tools arrived at all.
// providers lists the probes and the tool names under each; manifest fetches one
// provider's entry on its own, so a manifest too large to send costs that
// provider its tools and nobody else theirs (second review-fix contract 1).
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// ProviderSummary is one entry in a providers reply: which interface carries a
// probe, which probe version it answers with, and the names of the tools it
// offers. Names only, so the reply stays small however many providers a server
// runs; the descriptions arrive one manifest at a time.
type ProviderSummary struct {
	Iface string   `json:"iface"`
	V     int      `json:"v"`
	Tools nameList `json:"tools"`
}

// Providers calls the providers op ({}) and returns every probe the companion
// found, sorted by interface name. An entry that does not decode is skipped with
// a line naming it, the same rule the manifests themselves follow.
func (c *Client) Providers(ctx context.Context) ([]ProviderSummary, error) {
	raw, err := c.Call(ctx, "providers", nil)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if err := unmarshalList(raw, &entries); err != nil {
		return nil, fmt.Errorf("aab-rpc: providers: bad reply: %w", err)
	}

	out := make([]ProviderSummary, 0, len(entries))
	for _, entry := range entries {
		var summary ProviderSummary
		if err := json.Unmarshal(entry, &summary); err != nil {
			log.Printf("providers: skipping provider %s, its entry did not decode: %v", ifaceName(entry), err)
			continue
		}
		out = append(out, summary)
	}
	return out, nil
}

// manifestRequest is the payload of a manifest op: {i}, the provider's interface
// name.
type manifestRequest struct {
	I string `json:"i"`
}

// Manifest fetches one provider's manifest. The reply is the manifest verbatim,
// {v, tools}, so the interface name comes back from the argument rather than the
// wire: nothing else on the server can claim it.
func (c *Client) Manifest(ctx context.Context, iface string) (Provider, error) {
	raw, err := c.Call(ctx, "manifest", manifestRequest{I: iface})
	if err != nil {
		return Provider{}, err
	}
	var manifest struct {
		V     int                     `json:"v"`
		Tools map[string]ToolManifest `json:"tools"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Provider{}, fmt.Errorf("aab-rpc: manifest %s: bad reply: %w", iface, err)
	}
	return Provider{Iface: iface, V: manifest.V, Tools: manifest.Tools}, nil
}

// Catalog reads the whole catalog the way the service does: the providers list,
// then one manifest per provider, keeping every provider that answers. found is
// how many probes the companion reported, which is what tells "no mod exposes
// tools" apart from "every manifest failed to arrive". An error means the
// providers list itself never came; bad_op means this companion is older than
// these two ops and Tools is the way in.
func (c *Client) Catalog(ctx context.Context) (providers ToolsReply, found int, err error) {
	summaries, err := c.Providers(ctx)
	if err != nil {
		return nil, 0, err
	}
	providers = make(ToolsReply, 0, len(summaries))
	for _, summary := range summaries {
		manifest, err := c.Manifest(ctx, summary.Iface)
		if err != nil {
			log.Printf("manifest: skipping provider %s and its %d tool(s), it did not arrive: %v",
				summary.Iface, len(summary.Tools), err)
			continue
		}
		providers = append(providers, manifest)
	}
	return providers, len(summaries), nil
}

// nameList is a list of names that tolerates the companion's empty table. Its
// JSON encoder cannot tell an empty Lua array from an empty object and emits {}
// for both, so a provider with no usable tools would otherwise fail to decode
// and be reported as broken.
type nameList []string

func (n *nameList) UnmarshalJSON(b []byte) error {
	if trimmed := bytes.TrimSpace(b); len(trimmed) == 2 && trimmed[0] == '{' && trimmed[1] == '}' {
		*n = nil
		return nil
	}
	var names []string
	if err := json.Unmarshal(b, &names); err != nil {
		return err
	}
	*n = names
	return nil
}
