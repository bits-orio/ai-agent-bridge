// The two catalog ops and the two-step read over them.
package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProvidersParsesTheList(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"iface":"ai-agent-bridge-tools","v":1,"tools":["list_forces","list_players"]},{"iface":"other-mod","v":1,"tools":{}}]}`}
	c := New(fake)

	got, err := c.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Providers() returned %d entry(ies): %+v", len(got), got)
	}
	if got[0].Iface != "ai-agent-bridge-tools" || len(got[0].Tools) != 2 || got[0].Tools[1] != "list_players" {
		t.Errorf("first entry = %+v", got[0])
	}
	// A provider with no usable tools encodes as {} and is still a provider, not
	// a decode failure.
	if got[1].Iface != "other-mod" || len(got[1].Tools) != 0 {
		t.Errorf("second entry = %+v, want other-mod with no tools", got[1])
	}
	if !strings.Contains(fake.last, `"op":"providers"`) {
		t.Errorf("command = %q, want the providers op", fake.last)
	}
}

// One malformed entry costs that provider its place in the list and nobody else
// theirs, the same rule the manifests themselves follow.
func TestProvidersSkipsOnlyTheEntryThatCannotDecode(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"iface":"bad-mod","v":1,"tools":{"hello":{"desc":"x"}}},{"iface":"good-mod","v":1,"tools":["hello"]}]}`}

	got, err := New(fake).Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(got) != 1 || got[0].Iface != "good-mod" {
		t.Fatalf("Providers() = %+v, want only good-mod", got)
	}
}

func TestManifestParsesOneProvider(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{"v":1,"tools":{"entity_count":{"desc":"Count entities.","params":{"name":"string! Prototype name."}}}}}`}
	c := New(fake)

	got, err := c.Manifest(context.Background(), "ai-agent-bridge-tools")
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if got.Iface != "ai-agent-bridge-tools" {
		t.Errorf("Iface = %q, want the interface that was asked for", got.Iface)
	}
	if got.V != 1 || got.Tools["entity_count"].Desc != "Count entities." ||
		got.Tools["entity_count"].Params["name"] != "string! Prototype name." {
		t.Errorf("Manifest() = %+v", got)
	}
	if !strings.Contains(fake.last, `"i":"ai-agent-bridge-tools"`) {
		t.Errorf("command = %q, want the interface in the i field", fake.last)
	}
}

// A manifest too large to send comes back as the protocol's own refusal, which is
// what lets the caller skip that one provider and keep the rest.
func TestManifestTooLargeIsTypedError(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":false,"e":"too_large","m":"result exceeds 8000 bytes"}`}
	_, err := New(fake).Manifest(context.Background(), "verbose-mod")
	if !HasCode(err, CodeTooLarge) {
		t.Errorf("error = %v, want a typed too_large", err)
	}
}

func TestManifestRejectsAReplyThatIsNotAManifest(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":["list_forces"]}`}
	if _, err := New(fake).Manifest(context.Background(), "bad-mod"); err == nil {
		t.Fatal("expected a manifest that is a list to be an error")
	}
}

// opRCON answers each op with its own canned reply, for the reads that take more
// than one round trip. A manifest reply is keyed "manifest:<iface>".
type opRCON struct {
	replies map[string]string
	ops     []string
}

func (o *opRCON) Execute(cmd string) (string, time.Duration, error) {
	resp, err := o.execute(cmd)
	return resp, 0, err
}

func (o *opRCON) execute(cmd string) (string, error) {
	var req struct {
		Op string `json:"op"`
		I  string `json:"i"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(cmd, "/aab-rpc ")), &req); err != nil {
		return "", err
	}
	key := req.Op
	if req.I != "" {
		key += ":" + req.I
	}
	o.ops = append(o.ops, key)
	reply, canned := o.replies[key]
	if !canned {
		return `{"ok":false,"e":"bad_op","m":"no canned reply"}`, nil
	}
	return reply, nil
}

// The point of reading the catalog one provider at a time: the provider whose
// manifest will not fit loses its own tools and nobody else's.
func TestCatalogKeepsTheProvidersWhoseManifestsArrive(t *testing.T) {
	fake := &opRCON{replies: map[string]string{
		"providers":                      `{"ok":true,"r":[{"iface":"ai-agent-bridge-tools","v":1,"tools":["list_forces"]},{"iface":"verbose-mod","v":1,"tools":["standings","history"]}]}`,
		"manifest:ai-agent-bridge-tools": `{"ok":true,"r":{"v":1,"tools":{"list_forces":{"desc":"Every force."}}}}`,
		"manifest:verbose-mod":           `{"ok":false,"e":"too_large","m":"result exceeds 8000 bytes"}`,
	}}

	providers, found, err := New(fake).Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if found != 2 {
		t.Errorf("found = %d, want 2 probes reported", found)
	}
	if len(providers) != 1 || providers[0].Iface != "ai-agent-bridge-tools" ||
		providers[0].Tools["list_forces"].Desc != "Every force." {
		t.Fatalf("Catalog() = %+v, want only the provider that answered", providers)
	}
	want := []string{"providers", "manifest:ai-agent-bridge-tools", "manifest:verbose-mod"}
	if len(fake.ops) != len(want) {
		t.Fatalf("ops = %v, want %v", fake.ops, want)
	}
}

// A companion older than the providers op says bad_op, which is how the caller
// knows to read the whole catalog from the tools op instead.
func TestCatalogReportsAnOldCompanion(t *testing.T) {
	fake := &opRCON{replies: map[string]string{}}
	_, _, err := New(fake).Catalog(context.Background())
	if !HasCode(err, CodeBadOp) {
		t.Errorf("error = %v, want a typed bad_op", err)
	}
}

// No probe on the server at all is an empty catalog, not a failure.
func TestCatalogWithNoProviders(t *testing.T) {
	fake := &opRCON{replies: map[string]string{"providers": `{"ok":true,"r":{}}`}}
	providers, found, err := New(fake).Catalog(context.Background())
	if err != nil || found != 0 || len(providers) != 0 {
		t.Fatalf("Catalog() = %v, %d, %v; want empty, 0, nil", providers, found, err)
	}
}
