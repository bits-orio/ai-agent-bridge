package arith

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func call(t *testing.T, name, args string) string {
	t.Helper()
	for _, tool := range Tools() {
		if tool.Name == name {
			out, err := tool.Call(context.Background(), json.RawMessage(args))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return string(out)
		}
	}
	t.Fatalf("no tool %s", name)
	return ""
}

// The live miss: 100 plates each, 0.53 and 0.49 online hours; the leader is
// team-2 at 204.1 per hour, whatever a model without reasoning thinks.
func TestRankByRateNamesTheRealLeader(t *testing.T) {
	out := call(t, "rank_by_rate", `{"rows":[{"name":"team-1","total":100,"hours":0.53},{"name":"team-2","total":100,"hours":0.49}]}`)
	for _, want := range []string{`"leader":"team-2"`, `"per_hour":204.1`, `"per_hour":188.7`, `"rank":1,"name":"team-2"`, `"margin":"15.4 over team-1's 188.7"`, `"margin_percent":8.2`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

// No online hours means no rate, said rather than divided by zero; a tie is
// a tie; lowest-first works.
func TestRankEdges(t *testing.T) {
	out := call(t, "rank_by_rate", `{"rows":[{"name":"team-1","total":50,"hours":0},{"name":"team-2","total":10,"hours":2}]}`)
	if !strings.Contains(out, `"leader":"team-2"`) || !strings.Contains(out, `team-1 (no online hours, so no rate)`) {
		t.Errorf("zero hours not handled: %s", out)
	}
	tie := call(t, "rank", `{"rows":[{"name":"a","value":3},{"name":"b","value":3}]}`)
	if !strings.Contains(tie, `"leader":"a and b tied"`) {
		t.Errorf("tie not named: %s", tie)
	}
	low := call(t, "rank", `{"rows":[{"name":"slow","value":9},{"name":"fast","value":4}],"higher_is_better":false}`)
	if !strings.Contains(low, `"leader":"fast"`) {
		t.Errorf("lowest-first ranking wrong: %s", low)
	}
	for _, tool := range Tools() {
		if _, err := tool.Call(context.Background(), json.RawMessage(`{"rows":[]}`)); err == nil {
			t.Errorf("%s accepted empty rows", tool.Name)
		}
	}
}
