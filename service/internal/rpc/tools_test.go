package rpc

import (
	"encoding/json"
	"testing"
)

// A manifest decodes as one object, so one entry's badly typed tier would
// fail the whole Unmarshal and cost the provider every tool it owns. Lua has
// one number type, so `tier = 1` reaches the wire as the number 1 while a
// provider author reading the spec might equally write "1". Both must decode,
// and anything else must decode as empty rather than as an error.
func TestTierDecodesFromStringNumberOrNonsense(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want Tier
	}{
		{"string", `{"desc":"d","tier":"2"}`, "2"},
		{"number", `{"desc":"d","tier":1}`, "1"},
		{"float", `{"desc":"d","tier":2.0}`, "2.0"},
		{"absent", `{"desc":"d"}`, ""},
		{"null", `{"desc":"d","tier":null}`, ""},
		{"object", `{"desc":"d","tier":{"a":1}}`, ""},
		{"bool", `{"desc":"d","tier":true}`, ""},
	} {
		var m ToolManifest
		if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
			t.Fatalf("%s: %v, want no error: a bad tier must never cost a tool", tc.name, err)
		}
		if m.Tier != tc.want {
			t.Errorf("%s: tier = %q, want %q", tc.name, m.Tier, tc.want)
		}
		if m.Desc != "d" {
			t.Errorf("%s: desc = %q, the rest of the entry must survive", tc.name, m.Desc)
		}
	}
}

// The whole point: one entry written the Lua way must not take its neighbours
// with it. This is the shape a provider ships the first time it adopts tier.
func TestANumericTierDoesNotCostTheProviderItsOtherTools(t *testing.T) {
	var p Provider
	raw := `{"iface":"mts-v1","v":1,"tools":{
		"team_clock":  {"desc":"one team's clock","tier":1},
		"team_clocks": {"desc":"every team's clock"}
	}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Tools) != 2 {
		t.Fatalf("%d tool(s) survived, want 2: a numeric tier on one entry must not drop the provider", len(p.Tools))
	}
	if p.Tools["team_clock"].Tier != "1" {
		t.Errorf("team_clock tier = %q, want %q", p.Tools["team_clock"].Tier, "1")
	}
	if p.Tools["team_clocks"].Tier != "" {
		t.Errorf("team_clocks tier = %q, want empty: an entry without one reads as tier 1", p.Tools["team_clocks"].Tier)
	}
}
