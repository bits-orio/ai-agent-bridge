package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/arith"
)

// A genuine sweep reply gets ranked, and the leader and margin it gains are
// cross-checked against a direct call to arith's own "rank" tool on the same
// rows: this is the property the contract asks for, "ranked ... through the
// SAME arith.order that the rank tool uses", not merely "some plausible
// leader field appears".
func TestRankSweepMatchesADirectArithRankCall(t *testing.T) {
	sweep := `{"sweep_v":1,"axis":"force","metric":"entities","subject":"solar-panel","unit":"count","cols":["name","value"],"rows":[["team-2",2305],["team-1",41]],"total":14,"shown":8,"skipped":0,"why":null}`

	directArgs := `{"rows":[{"name":"team-2","value":2305},{"name":"team-1","value":41}]}`
	wantVerdict := callArithRank(t, directArgs)

	got := rankSweep(context.Background(), json.RawMessage(sweep))

	var gotFields map[string]json.RawMessage
	if err := json.Unmarshal(got, &gotFields); err != nil {
		t.Fatalf("ranked sweep reply is not valid JSON: %v\n%s", err, got)
	}
	for _, key := range []string{"leader", "margin", "margin_percent"} {
		if string(gotFields[key]) != string(wantVerdict[key]) {
			t.Errorf("%s = %s, want %s (arith's own rank tool on the identical rows)", key, gotFields[key], wantVerdict[key])
		}
	}
	// Every field the reply already carried survives untouched.
	for _, key := range []string{"sweep_v", "axis", "metric", "subject", "unit", "cols", "rows", "total", "shown", "skipped", "why"} {
		if _, ok := gotFields[key]; !ok {
			t.Errorf("ranked reply lost field %q it already carried: %s", key, got)
		}
	}
}

// callArithRank runs arith's own "rank" tool directly, the same way the
// existing rank_by_rate/rank tests in arith_test.go do, and returns its
// decoded top-level fields for comparison.
func callArithRank(t *testing.T, args string) map[string]json.RawMessage {
	t.Helper()
	for _, tool := range arith.Tools() {
		if tool.Name != "rank" {
			continue
		}
		out, err := tool.Call(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("arith rank: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatalf("arith rank reply did not decode: %v", err)
		}
		return fields
	}
	t.Fatal("arith.Tools() has no \"rank\" tool")
	return nil
}

// The hard gate: every one of these is a reply that must come back byte for
// byte identical to what it received, never rewritten, never an error.
// D1 explicitly asks for the identity property to be tested on several
// non-sweep replies, not just the happy path, because this is the first
// place the service rewrites a provider's reply instead of forwarding it
// verbatim.
func TestRankSweepIsTheIdentityOnEverythingThatIsNotAGenuineSweep(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"an ordinary tool reply with no sweep_v at all", `{"forces":["team-1","team-2"]}`},
		{"a briefing-shaped reply", `{"t":184320,"h":51.2,"fs":[{"name":"team-1"}]}`},
		{"an already-ranked arith reply, which itself has no sweep_v", `{"leader":"team-2","margin":"1 over team-1's 0","ranking":[{"rank":1,"name":"team-2","value":1}]}`},
		{"sweep_v of the wrong JSON type", `{"sweep_v":"1","cols":["name","value"],"rows":[["team-2",1]]}`},
		{"sweep_v present but not 1", `{"sweep_v":2,"cols":["name","value"],"rows":[["team-2",1]]}`},
		{"a refusal: sweep_v 1 but found false, no rows", `{"sweep_v":1,"found":false,"reason":"unrecognised metric","cards":[]}`},
		{"cols is not the exact name/value shape", `{"sweep_v":1,"cols":["force","total"],"rows":[["team-2",1]]}`},
		{"cols has an extra column", `{"sweep_v":1,"cols":["name","value","extra"],"rows":[["team-2",1,0]]}`},
		{"rows is empty", `{"sweep_v":1,"cols":["name","value"],"rows":[]}`},
		{"rows is missing entirely", `{"sweep_v":1,"cols":["name","value"]}`},
		{"a row with three cells instead of two", `{"sweep_v":1,"cols":["name","value"],"rows":[["team-2",1,"extra"]]}`},
		{"a row whose name cell is a number, not a string", `{"sweep_v":1,"cols":["name","value"],"rows":[[2,1]]}`},
		{"a row whose value cell is a string, not a number", `{"sweep_v":1,"cols":["name","value"],"rows":[["team-2","a lot"]]}`},
		{"a row whose name cell is an empty string", `{"sweep_v":1,"cols":["name","value"],"rows":[["",1]]}`},
		{"the reply is a JSON array, not an object", `[1,2,3]`},
		{"the reply is a bare JSON string", `"just a string"`},
		{"the reply is JSON null", `null`},
		{"the reply is not valid JSON at all", `not json`},
		{"the reply is empty", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			got := rankSweep(context.Background(), raw)
			if string(got) != string(raw) {
				t.Errorf("rankSweep rewrote a non-sweep reply.\n got:  %s\n want: %s", got, raw)
			}
		})
	}
}

// A JSON-valid but semantically empty rows array, and a reply that decodes
// but carries no cols at all, both fail the gate the same way: absence, not
// a decode error.
func TestRankSweepGateFailsClosedOnAbsentFields(t *testing.T) {
	raw := json.RawMessage(`{"sweep_v":1,"rows":[["team-2",1]]}`) // no cols key
	got := rankSweep(context.Background(), raw)
	if string(got) != string(raw) {
		t.Errorf("a sweep reply with no cols at all was rewritten: %s", got)
	}
}

// found:true (rather than the more common absence of the key) still ranks:
// only found:false is a refusal.
func TestRankSweepRanksWhenFoundIsExplicitlyTrue(t *testing.T) {
	raw := json.RawMessage(`{"sweep_v":1,"found":true,"cols":["name","value"],"rows":[["team-2",5],["team-1",1]]}`)
	got := rankSweep(context.Background(), raw)
	if !strings.Contains(string(got), `"leader":"team-2"`) {
		t.Errorf("found:true should still be ranked: %s", got)
	}
}

// The rows are names players typed, and a platform may be called "A & B
// <fast>". json.Marshal escapes each of those characters to six bytes of \u
// sequence, including inside the rows this function promises to forward
// untouched, because a RawMessage is re-encoded on the way through. Against
// agent.go's 4096-byte cut on a tool result that is a real cost, and the
// companion never escaped them in the first place.
func TestRankSweepForwardsPlayerTypedNamesByteForByte(t *testing.T) {
	sweep := `{"sweep_v":1,"axis":"platform","metric":"entities","subject":"thruster","unit":"count","cols":["name","value"],"rows":[["A & B <fast>",50],["plain",1]],"total":2,"shown":2,"skipped":0,"why":null}`

	got := string(rankSweep(context.Background(), json.RawMessage(sweep)))

	if !strings.Contains(got, `"A & B <fast>"`) {
		t.Errorf("the platform name did not survive byte for byte:\n%s", got)
	}
	for _, escaped := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(got, escaped) {
			t.Errorf("ranked reply carries the HTML escape %s:\n%s", escaped, got)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("ranked reply is not valid JSON: %v\n%s", err, got)
	}
	if string(fields["leader"]) != `"A & B <fast>"` {
		t.Errorf("leader = %s, want the name unescaped", fields["leader"])
	}
}

// arith leaves margin_percent out when second place is zero: there is no
// percentage of nothing. A reply that already carried one used to keep it,
// beside a fresh margin it contradicted.
func TestRankSweepDropsAMarginPercentArithRefusedToCompute(t *testing.T) {
	sweep := `{"sweep_v":1,"axis":"force","metric":"rockets","unit":"count","cols":["name","value"],"rows":[["a",5],["b",0]],"total":2,"shown":2,"skipped":0,"why":null,"margin_percent":99}`

	got := rankSweep(context.Background(), json.RawMessage(sweep))

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("ranked reply is not valid JSON: %v\n%s", err, got)
	}
	if raw, present := fields["margin_percent"]; present {
		t.Errorf("stale margin_percent survived the merge: %s, beside margin %s", raw, fields["margin"])
	}
	if !strings.Contains(string(fields["margin"]), "over b") {
		t.Errorf("margin = %s, want a fresh verdict over b", fields["margin"])
	}
}

// A fraction reaches the wire as a short decimal string, the companion's own
// convention for every non-integer, so a metric with decimals must still be
// ranked, and ranked by its real value. A strict number decode made the
// ranker the identity on exactly those metrics.
func TestRankSweepRanksAStringEncodedFraction(t *testing.T) {
	sweep := `{"sweep_v":1,"axis":"force","metric":"standings","unit":"points","cols":["name","value"],"rows":[["team-b","12.5"],["team-a",5]],"total":2,"shown":2,"skipped":0,"why":null}`

	got := rankSweep(context.Background(), json.RawMessage(sweep))

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("ranked reply is not valid JSON: %v\n%s", err, got)
	}
	if string(fields["leader"]) != `"team-b"` {
		t.Errorf("leader = %s, want team-b: the string-encoded 12.5 was not ranked", fields["leader"])
	}
	if !strings.Contains(string(fields["margin"]), "7.5") {
		t.Errorf("margin = %s, want 7.5 over team-a, so the fraction was read as its real value", fields["margin"])
	}
}
