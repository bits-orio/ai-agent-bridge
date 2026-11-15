package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// captureLog collects what the catalog logs while f runs.
func captureLog(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prev := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer log.SetOutput(prevOut)
	log.SetFlags(0)
	defer log.SetFlags(prev)
	f()
	return buf.String()
}

func buildOne(fn string, m rpc.ToolManifest) *Catalog {
	return Build(rpc.ToolsReply{{Iface: "prov", Tools: map[string]rpc.ToolManifest{fn: m}}}, &recorder{out: "{}"})
}

// A parameter key the model API would reject is dropped with a log line;
// the other parameters and the tool survive.
func TestBadParamKeysAreDropped(t *testing.T) {
	for _, key := range []string{"", "has space", "semi;colon", strings.Repeat("k", 65), "naïve", "a/b", "line\nbreak"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			var c *Catalog
			logged := captureLog(t, func() {
				c = buildOne("fn", rpc.ToolManifest{Desc: "d", Params: map[string]string{key: "string a value", "good.key-1_x": "integer a count"}})
			})
			if len(c.Tools()) != 1 {
				t.Fatalf("tools = %d, want the tool kept", len(c.Tools()))
			}
			props := c.Tools()[0].Schema["properties"].(map[string]any)
			if _, bad := props[key]; bad {
				t.Errorf("bad key %q reached the schema", key)
			}
			if _, ok := props["good.key-1_x"]; !ok {
				t.Errorf("a good key was lost: %v", props)
			}
			if !strings.Contains(logged, "prov.fn") || !strings.Contains(logged, fmt.Sprintf("%q", key)) {
				t.Errorf("log %q should name provider, tool and key", logged)
			}
		})
	}
}

// A required parameter that cannot be named drops the whole tool.
func TestRequiredBadParamKeyDropsTheTool(t *testing.T) {
	reply := rpc.ToolsReply{{Iface: "prov", Tools: map[string]rpc.ToolManifest{
		"broken": {Desc: "d", Params: map[string]string{"bad key": "string! needed"}},
		"fine":   {Desc: "d"},
	}}}
	var c *Catalog
	logged := captureLog(t, func() { c = Build(reply, &recorder{out: "{}"}) })
	if len(c.Tools()) != 1 || c.Tools()[0].Name != "prov__fine" {
		t.Fatalf("tools = %v, want only prov__fine", c.Tools())
	}
	if _, ok := c.Target("prov__broken"); ok {
		t.Error("the dropped tool still resolves")
	}
	if !strings.Contains(logged, "prov.broken") || !strings.Contains(logged, `"bad key"`) {
		t.Errorf("log %q should name provider, tool and key", logged)
	}
}

func TestEnumValuesAreDedupedKeepingTheFirst(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want []string
	}{
		{"enum<a,b,a,c,b>! pick", []string{"a", "b", "c"}},
		{"enum<a, ,b,,a> pick", []string{"a", "b"}},
		{"enum<x,x,x> pick", []string{"x"}},
	} {
		prop, _ := parseParam(tc.spec)
		if got := prop["enum"]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q enum = %v, want %v", tc.spec, got, tc.want)
		}
	}
	// An enum left with nothing degrades to a plain string, no enum key.
	if prop, _ := parseParam("enum<,, > pick"); prop["type"] != "string" || prop["enum"] != nil {
		t.Errorf("empty enum = %v, want a plain string", prop)
	}
}

func TestDescriptionsAreCappedOnAUTF8Boundary(t *testing.T) {
	long := strings.Repeat("é", 400) // two bytes each, 800 bytes
	for _, tc := range []struct {
		name  string
		got   string
		limit int
	}{
		{"tool", describe(rpc.ToolManifest{Desc: long, Tier: "1"}), maxToolDescBytes + len("[tier 1, swept] ")},
		{"param", func() string { p, _ := parseParam("string " + long); return p["description"].(string) }(), maxParamDescBytes},
	} {
		if len(tc.got) > tc.limit || !utf8.ValidString(tc.got) || !strings.HasSuffix(tc.got, "...") {
			t.Errorf("%s description: %d bytes (limit %d), valid %v, tail %q", tc.name, len(tc.got), tc.limit, utf8.ValidString(tc.got), tc.got[len(tc.got)-6:])
		}
	}
	short := "a short one"
	if p, _ := parseParam("string " + short); p["description"] != short {
		t.Errorf("a short description changed: %v", p["description"])
	}
}

// One provider's tools together stay under the cap; the ones that do not fit
// are dropped in sorted order, so the same input gives the same catalog.
func TestProviderSchemaBytesAreCapped(t *testing.T) {
	tools := map[string]rpc.ToolManifest{}
	for i := 0; i < 80; i++ {
		params := map[string]string{}
		for j := 0; j < 8; j++ {
			params[fmt.Sprintf("param_%d", j)] = "string " + strings.Repeat("d", 290)
		}
		tools[fmt.Sprintf("fn_%03d", i)] = rpc.ToolManifest{Desc: strings.Repeat("x", 600), Params: params}
	}
	reply := rpc.ToolsReply{{Iface: "big", Tools: tools}, {Iface: "small", Tools: map[string]rpc.ToolManifest{"one": {Desc: "d"}}}}
	var first, second *Catalog
	logged := captureLog(t, func() {
		first = Build(reply, &recorder{out: "{}"})
		second = Build(reply, &recorder{out: "{}"})
	})
	size := map[string]int{}
	var names []string
	for _, tl := range first.Tools() {
		size[strings.SplitN(tl.Name, "__", 2)[0]] += toolBytes(tl)
		names = append(names, tl.Name)
	}
	if size["big"] > maxProviderSchemaBytes || size["big"] == 0 {
		t.Errorf("big provider kept %d bytes, want 1..%d", size["big"], maxProviderSchemaBytes)
	}
	if size["small"] == 0 {
		t.Error("another provider's budget must be its own")
	}
	if len(names) >= 81 || !strings.Contains(logged, "big.fn_079") {
		t.Errorf("kept %d tools; log should name a dropped tool: %.200s", len(names), logged)
	}
	a, _ := json.Marshal(first.Tools()[0].Schema)
	b, _ := json.Marshal(second.Tools()[0].Schema)
	if len(first.Tools()) != len(second.Tools()) || !bytes.Equal(a, b) {
		t.Error("two builds of one reply differ")
	}
}
