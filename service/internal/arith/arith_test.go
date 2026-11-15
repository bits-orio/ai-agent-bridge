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

// Ranking and ties go by the raw figures; rounding is for display. The old
// code rounded first, so 3 rockets in 100 h against 1 was "tied at 0".
func TestRawValuesDecideTiesAndShowEnoughPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args string
		want, notWant    []string
	}{
		{"3 vs 1 rocket in 100 h", "rank_by_rate",
			`{"rows":[{"name":"a","total":3,"hours":100},{"name":"b","total":1,"hours":100}]}`,
			[]string{`"leader":"a"`, `"per_hour":0.03`, `"per_hour":0.01`, `"margin":"0.02 over b's 0.01"`}, []string{"tied"}},
		{"12.34 vs 12.31", "rank",
			`{"rows":[{"name":"a","value":12.34},{"name":"b","value":12.31}]}`,
			[]string{`"leader":"a"`, `"value":12.34`, `"value":12.31`, `"margin":"0.03 over b's 12.31"`}, []string{"tied"}},
		{"equal raw is a tie", "rank",
			`{"rows":[{"name":"a","value":12.34},{"name":"b","value":12.34}]}`,
			[]string{`"leader":"a and b tied"`, `tied at 12.3`}, nil},
		{"one decimal is enough when figures differ at it", "rank",
			`{"rows":[{"name":"a","value":12.4},{"name":"b","value":12.1}]}`,
			[]string{`"margin":"0.3 over b's 12.1"`}, nil},
		{"lower is better ranks on raw too", "rank",
			`{"rows":[{"name":"a","value":12.34},{"name":"b","value":12.31}],"higher_is_better":false}`,
			[]string{`"leader":"b"`}, []string{"tied"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := call(t, tc.tool, tc.args)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %s in %s", w, out)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %s in %s", w, out)
				}
			}
		})
	}
}

// Hours so small that the rate overflows are skipped with a reason, and the
// reply stays valid JSON.
func TestNonFiniteRatesAreSkipped(t *testing.T) {
	out := call(t, "rank_by_rate", `{"rows":[{"name":"a","total":1e300,"hours":1e-300},{"name":"b","total":10,"hours":2}]}`)
	if !json.Valid([]byte(out)) {
		t.Fatalf("not JSON: %s", out)
	}
	if !strings.Contains(out, `"leader":"b"`) || !strings.Contains(out, `a (the hours are too small`) {
		t.Errorf("overflowing rate not skipped: %s", out)
	}
	only := call(t, "rank_by_rate", `{"rows":[{"name":"a","total":1e300,"hours":1e-300}]}`)
	if !json.Valid([]byte(only)) || !strings.Contains(only, `"leader":"nobody"`) {
		t.Errorf("all rows skipped: %s", only)
	}
	huge := call(t, "rank", `{"rows":[{"name":"a","value":1.7e308},{"name":"b","value":-1.7e308}]}`)
	if !json.Valid([]byte(huge)) {
		t.Errorf("not JSON: %s", huge)
	}
}
