package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// recorder stands in for the rpc client and keeps what it was called with.
type recorder struct {
	iface string
	fn    string
	args  any
	out   string
	err   error
}

func (r *recorder) CallTool(_ context.Context, iface, fn string, args any) (json.RawMessage, error) {
	r.iface, r.fn, r.args = iface, fn, args
	if r.err != nil {
		return nil, r.err
	}
	return json.RawMessage(r.out), nil
}

func (r *recorder) argsMap(t *testing.T) map[string]any {
	t.Helper()
	raw, ok := r.args.(json.RawMessage)
	if !ok {
		t.Fatalf("args are %T, want json.RawMessage", r.args)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("args are not an object: %v", err)
	}
	return out
}

func sampleReply() rpc.ToolsReply {
	return rpc.ToolsReply{{
		Iface: "ai-agent-bridge-tools",
		V:     1,
		Tools: map[string]rpc.ToolManifest{
			"list_forces": {Desc: "Every force on the server."},
			"item_rate": {
				Desc: "Production rate of one item.",
				Params: map[string]string{
					"surface": "string! surface name, e.g. nauvis",
					"item":    "string! item prototype name",
					"n":       "integer how many, optional",
					"loose":   "not a type at all",
				},
			},
		},
	}}
}

// The wire name is "<iface>__<fn>" with everything the model API disallows
// replaced, and it maps back to the provider function it came from.
func TestToolNameMangling(t *testing.T) {
	for _, tc := range []struct{ iface, fn, want string }{
		{"ai-agent-bridge-tools", "list_forces", "ai-agent-bridge-tools__list_forces"},
		{"some mod's iface!", "do.it", "some_mod_s_iface___do_it"},
		{"UPPER-1", "fn2", "UPPER-1__fn2"},
	} {
		if got := ToolName(tc.iface, tc.fn); got != tc.want {
			t.Errorf("ToolName(%q, %q) = %q, want %q", tc.iface, tc.fn, got, tc.want)
		}
	}
}

func TestToolNameIsClippedToTheAPILimit(t *testing.T) {
	long := ""
	for i := 0; i < 20; i++ {
		long += "abcde"
	}
	if got := ToolName(long, "fn"); len(got) != maxNameLen {
		t.Errorf("name is %d characters, want it clipped to %d", len(got), maxNameLen)
	}
}

// The interface prefix gives way, never the function name. Cutting the tail
// instead made two functions on a long interface the same name, and the
// collision check then dropped one of them (review-fix contract 11).
func TestToolNameTruncatesTheInterfaceNotTheFunction(t *testing.T) {
	iface := "some-long-multiplayer-teams-provider-interface-name-v1" // 54 characters
	since := ToolName(iface, "production_since")
	rate := ToolName(iface, "production_rate")

	if since == rate {
		t.Fatalf("both functions map to %q", since)
	}
	for _, tc := range []struct{ name, fn string }{{since, "production_since"}, {rate, "production_rate"}} {
		if len(tc.name) > maxNameLen {
			t.Errorf("%q is %d characters, over the %d limit", tc.name, len(tc.name), maxNameLen)
		}
		if !strings.HasSuffix(tc.name, sep+tc.fn) {
			t.Errorf("%q does not end in the whole function name %q", tc.name, tc.fn)
		}
	}
}

// Both functions survive the build, so neither vanishes from the catalog.
func TestBuildKeepsEveryFunctionOnALongInterface(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "some-long-multiplayer-teams-provider-interface-name-v1",
		Tools: map[string]rpc.ToolManifest{
			"production_since": {Desc: "Since a tick."},
			"production_rate":  {Desc: "Per minute."},
		},
	}}
	c := Build(reply, &recorder{out: "{}"})
	if len(c.Tools()) != 2 {
		t.Fatalf("built %d tool(s), want 2: %+v", len(c.Tools()), c.Tools())
	}
	for _, tool := range c.Tools() {
		target, ok := c.Target(tool.Name)
		if !ok {
			t.Fatalf("%q does not map back to a provider function", tool.Name)
		}
		if !strings.HasSuffix(tool.Name, sep+target.Fn) {
			t.Errorf("%q names a different function than %q", tool.Name, target.Fn)
		}
	}
}

// A function name long enough to fill the budget on its own is the one case
// where it has to give way, and the result is still a legal, unique-per-function
// name.
func TestToolNameWithAnAbsurdlyLongFunctionName(t *testing.T) {
	fn := strings.Repeat("f", 200)
	got := ToolName("iface", fn)
	if len(got) != maxNameLen {
		t.Errorf("name is %d characters, want %d", len(got), maxNameLen)
	}
	if !strings.Contains(got, sep) {
		t.Errorf("%q lost the separator", got)
	}
}

func TestBuildReverseMap(t *testing.T) {
	c := Build(sampleReply(), &recorder{out: "{}"})
	if len(c.Tools()) != 2 {
		t.Fatalf("built %d tools, want 2", len(c.Tools()))
	}
	// Sorted by function name inside a provider, so the JSON is stable.
	if c.Tools()[0].Name != "ai-agent-bridge-tools__item_rate" {
		t.Errorf("first tool = %q, want item_rate", c.Tools()[0].Name)
	}
	target, ok := c.Target("ai-agent-bridge-tools__list_forces")
	if !ok || target.Iface != "ai-agent-bridge-tools" || target.Fn != "list_forces" {
		t.Errorf("Target = %+v (found %v), want the list_forces provider function", target, ok)
	}
	if _, ok := c.Target("nope"); ok {
		t.Error("Target found a tool that is not in the catalog")
	}
}

// force is injected on every tool, as a required string, whether or not the
// provider declared any parameters.
func TestSchemaInjectsForce(t *testing.T) {
	c := Build(sampleReply(), &recorder{out: "{}"})
	for _, tool := range c.Tools() {
		props := tool.Schema["properties"].(map[string]any)
		if _, ok := props[ForceParam]; !ok {
			t.Fatalf("%s has no force property: %v", tool.Name, props)
		}
		required, _ := tool.Schema["required"].([]string)
		found := false
		for _, name := range required {
			if name == ForceParam {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not require force: %v", tool.Name, required)
		}
	}
}

func TestParamGrammar(t *testing.T) {
	c := Build(sampleReply(), &recorder{out: "{}"})
	rate := c.Tools()[0]
	props := rate.Schema["properties"].(map[string]any)

	surface := props["surface"].(map[string]any)
	if surface["type"] != "string" || surface["description"] != "surface name, e.g. nauvis" {
		t.Errorf("surface property = %v", surface)
	}
	count := props["n"].(map[string]any)
	if count["type"] != "integer" {
		t.Errorf("n property = %v, want an integer", count)
	}
	// A line that does not start with a type word is taken whole, as a string.
	loose := props["loose"].(map[string]any)
	if loose["type"] != "string" || loose["description"] != "not a type at all" {
		t.Errorf("loose property = %v", loose)
	}

	required, _ := rate.Schema["required"].([]string)
	if len(required) != 3 {
		t.Fatalf("required = %v, want force, item and surface", required)
	}
	for i, want := range []string{"force", "item", "surface"} {
		if required[i] != want {
			t.Errorf("required[%d] = %q, want %q (the list is sorted so the JSON is stable)", i, required[i], want)
		}
	}
}

// The three list<...> words each build the schema phase4-spec.md §5 spells
// out: list<string> and list<number> are a bare scalar array, list<point> is
// an array of {x, y, surface?} objects with only x and y required.
func TestParamGrammarLists(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "some-provider",
		Tools: map[string]rpc.ToolManifest{
			"sweep": {
				Desc: "d",
				Params: map[string]string{
					"items":   "list<string>! item names to report on, e.g. iron-plate",
					"amounts": "list<number> amounts to compare",
					"anchors": "list<point> one or more {x, y, surface?} to search near",
				},
			},
		},
	}}
	c := Build(reply, &recorder{out: "{}"})
	props := c.Tools()[0].Schema["properties"].(map[string]any)

	items := props["items"].(map[string]any)
	if items["type"] != "array" || items["description"] != "item names to report on, e.g. iron-plate" {
		t.Errorf("items property = %v", items)
	}
	if got := items["items"].(map[string]any); got["type"] != "string" {
		t.Errorf("items element schema = %v, want a bare string", got)
	}

	amounts := props["amounts"].(map[string]any)
	if amounts["type"] != "array" {
		t.Errorf("amounts property = %v", amounts)
	}
	if got := amounts["items"].(map[string]any); got["type"] != "number" {
		t.Errorf("amounts element schema = %v, want a bare number", got)
	}

	anchors := props["anchors"].(map[string]any)
	if anchors["type"] != "array" {
		t.Errorf("anchors property = %v", anchors)
	}
	point := anchors["items"].(map[string]any)
	if point["type"] != "object" {
		t.Fatalf("point schema = %v, want an object", point)
	}
	pointProps := point["properties"].(map[string]any)
	if pointProps["x"].(map[string]any)["type"] != "number" || pointProps["y"].(map[string]any)["type"] != "number" {
		t.Errorf("point x/y = %v, want both number", pointProps)
	}
	if pointProps["surface"].(map[string]any)["type"] != "string" {
		t.Errorf("point surface = %v, want string", pointProps["surface"])
	}
	pointRequired, _ := point["required"].([]string)
	if len(pointRequired) != 2 || pointRequired[0] != "x" || pointRequired[1] != "y" {
		t.Errorf("point required = %v, want exactly [x y]", pointRequired)
	}

	// list<string> was declared with "!"; the other two were not.
	required, _ := c.Tools()[0].Schema["required"].([]string)
	found := map[string]bool{}
	for _, name := range required {
		found[name] = true
	}
	if !found["items"] {
		t.Errorf("required = %v, want items required", required)
	}
	if found["amounts"] || found["anchors"] {
		t.Errorf("required = %v, want amounts and anchors optional", required)
	}
}

// A type word this build does not know degrades to a plain string carrying
// the whole spec line, exactly like a provider's typo does today. This is the
// property that lets the grammar ship ahead of the tools that use a future
// word: an older service reading a newer manifest loses the type but keeps
// the tool and the parameter, rather than dropping either (phase4-spec.md
// §5).
func TestUnknownParamTypeDegradesToAStringButKeepsTheTool(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "future-provider",
		Tools: map[string]rpc.ToolManifest{
			"peek": {
				Desc: "d",
				Params: map[string]string{
					"target": "list<entity>! a type word from a grammar this build has not learned yet",
				},
			},
		},
	}}
	c := Build(reply, &recorder{out: "{}"})
	if len(c.Tools()) != 1 {
		t.Fatalf("built %d tool(s), want the tool kept despite the unknown type", len(c.Tools()))
	}
	props := c.Tools()[0].Schema["properties"].(map[string]any)
	target := props["target"].(map[string]any)
	if target["type"] != "string" {
		t.Errorf("target property = %v, want it to degrade to a string", target)
	}
	if target["description"] != "list<entity>! a type word from a grammar this build has not learned yet" {
		t.Errorf("description = %v, want the whole original line", target["description"])
	}
	// The unknown word swallowed the "!"; an unrecognised type is read whole,
	// so it is never required either.
	required, _ := c.Tools()[0].Schema["required"].([]string)
	for _, name := range required {
		if name == "target" {
			t.Errorf("required = %v, want target left optional", required)
		}
	}
}

// A provider declaring force itself never overrides the reserved one.
func TestProviderCannotDeclareForce(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "other-mod",
		Tools: map[string]rpc.ToolManifest{"peek": {Desc: "x", Params: map[string]string{"force": "integer! nonsense"}}},
	}}
	c := Build(reply, &recorder{out: "{}"})
	props := c.Tools()[0].Schema["properties"].(map[string]any)
	if props[ForceParam].(map[string]any)["type"] != "string" {
		t.Errorf("the provider's force declaration won: %v", props[ForceParam])
	}
}

// The asker's force is filled in when the model leaves it out.
func TestCallFillsTheAskersForce(t *testing.T) {
	rec := &recorder{out: `{"forces":[]}`}
	c := Build(sampleReply(), rec)
	ctx := WithForce(context.Background(), "enemy")

	if _, err := c.Tools()[1].Call(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if rec.iface != "ai-agent-bridge-tools" || rec.fn != "list_forces" {
		t.Errorf("called %s.%s", rec.iface, rec.fn)
	}
	if got := rec.argsMap(t)["force"]; got != "enemy" {
		t.Errorf("force = %v, want the asker's force", got)
	}
}

// A force the model named itself is left alone.
func TestCallKeepsTheModelsForce(t *testing.T) {
	rec := &recorder{out: "{}"}
	c := Build(sampleReply(), rec)
	ctx := WithForce(context.Background(), "player")

	if _, err := c.Tools()[1].Call(ctx, json.RawMessage(`{"force":"enemy"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := rec.argsMap(t)["force"]; got != "enemy" {
		t.Errorf("force = %v, want the force the model asked for", got)
	}
}

// A tool called with no arguments at all still reaches the provider as an
// object: the companion's tools index their argument table directly.
func TestCallAlwaysSendsAnObject(t *testing.T) {
	rec := &recorder{out: "{}"}
	c := Build(sampleReply(), rec)

	if _, err := c.Tools()[1].Call(context.Background(), nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := rec.argsMap(t); len(got) != 0 {
		t.Errorf("args = %v, want an empty object", got)
	}
}

func TestCallPassesTheProviderError(t *testing.T) {
	rec := &recorder{err: errors.New("aab-rpc: provider_error: boom")}
	c := Build(sampleReply(), rec)

	if _, err := c.Tools()[1].Call(context.Background(), nil); err == nil {
		t.Fatal("expected the provider error to come back")
	}
}

// A manifest entry with no tier at all reads as tier 1: missing tier must
// never drop the entry, and the cheaper tier is the default (phase4-spec.md
// §5).
func TestDescribeWithNoTierDefaultsToTierOne(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "p",
		Tools: map[string]rpc.ToolManifest{"fn": {Desc: "does a thing"}},
	}}
	c := Build(reply, &recorder{out: "{}"})
	if got := c.Tools()[0].Description; got != "[tier 1, swept] does a thing" {
		t.Errorf("description = %q, want the tier 1 prefix", got)
	}
}

// Each declared tier gets its own prefix and word.
func TestDescribeCarriesTheDeclaredTier(t *testing.T) {
	for _, tc := range []struct {
		tier string
		want string
	}{
		{"1", "[tier 1, swept] does a thing"},
		{"2", "[tier 2, bounded] does a thing"},
	} {
		reply := rpc.ToolsReply{{
			Iface: "p",
			Tools: map[string]rpc.ToolManifest{"fn": {Desc: "does a thing", Tier: rpc.Tier(tc.tier)}},
		}}
		c := Build(reply, &recorder{out: "{}"})
		if got := c.Tools()[0].Description; got != tc.want {
			t.Errorf("tier %q: description = %q, want %q", tc.tier, got, tc.want)
		}
	}
}

// A tier word nobody recognises reads the same as no tier at all: tier 1,
// and the tool is kept, not dropped. Only "1" and "2" are defined
// (phase4-spec.md §5: "Two tiers, and every tool sits in exactly one"), so an
// unrecognised value degrades the same way an unrecognised param type word
// does, rather than propagating a cost label nobody declared.
func TestDescribeWithAnUnrecognisedTierFallsBackToTierOne(t *testing.T) {
	for _, tier := range []string{"3", "gold", "0", "-1", "tier 2"} {
		reply := rpc.ToolsReply{{
			Iface: "p",
			Tools: map[string]rpc.ToolManifest{"fn": {Desc: "does a thing", Tier: rpc.Tier(tier)}},
		}}
		c := Build(reply, &recorder{out: "{}"})
		if len(c.Tools()) != 1 {
			t.Fatalf("tier %q: built %d tool(s), want the tool kept", tier, len(c.Tools()))
		}
		if got := c.Tools()[0].Description; got != "[tier 1, swept] does a thing" {
			t.Errorf("tier %q: description = %q, want the tier 1 default", tier, got)
		}
	}
}

// The tier prefix and the list<point> grammar addition are independent: a
// tool can declare a tier 2 cost and take a list<point> parameter, and
// neither one disturbs the other's part of the tool.
func TestTierAndListGrammarComposeOnOneTool(t *testing.T) {
	reply := rpc.ToolsReply{{
		Iface: "p",
		Tools: map[string]rpc.ToolManifest{
			"find_item": {
				Desc: "walks the map for an item",
				Tier: "2",
				Params: map[string]string{
					"anchors": "list<point> one or more {x, y, surface?} to search near",
				},
			},
		},
	}}
	c := Build(reply, &recorder{out: "{}"})
	tool := c.Tools()[0]

	if want := "[tier 2, bounded] walks the map for an item"; tool.Description != want {
		t.Errorf("description = %q, want %q", tool.Description, want)
	}
	props := tool.Schema["properties"].(map[string]any)
	anchors := props["anchors"].(map[string]any)
	if anchors["type"] != "array" || anchors["items"].(map[string]any)["type"] != "object" {
		t.Errorf("anchors property = %v, want the list<point> object schema untouched by tier", anchors)
	}
}

// Two providers whose names collide after mangling keep the first, so the
// catalog never offers the model two tools with one name.
func TestBuildKeepsTheFirstOfAColliding(t *testing.T) {
	reply := rpc.ToolsReply{
		{Iface: "a.b", Tools: map[string]rpc.ToolManifest{"fn": {Desc: "first"}}},
		{Iface: "a_b", Tools: map[string]rpc.ToolManifest{"fn": {Desc: "second"}}},
	}
	c := Build(reply, &recorder{out: "{}"})
	if len(c.Tools()) != 1 {
		t.Fatalf("built %d tools, want 1", len(c.Tools()))
	}
	target, _ := c.Target("a_b__fn")
	if target.Iface != "a.b" {
		t.Errorf("kept %q, want the first provider", target.Iface)
	}
}
