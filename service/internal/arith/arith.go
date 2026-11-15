// Package arith holds the two tools the service runs itself with no game
// call: ranking figures the model already has. A model with reasoning off
// gets the arithmetic right and the verdict wrong, "Team 01 ahead" over
// figures that said the opposite, so the verdict is not left to it. It
// hands the figures over and takes the leader the tool names.
package arith

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// Tools returns rank_by_rate and rank.
func Tools() []tools.Tool {
	return []tools.Tool{
		{
			Name: "rank_by_rate",
			Description: "Who is ahead per hour: give each force's total and its own online hours, get every force's per-hour figure, " +
				"the ranking from highest down, the leader and its margin over second place. The verdict for 'which team is ahead' comes from here, never from your own comparison.",
			Schema: tools.ObjectSchema(map[string]any{
				"rows": map[string]any{
					"type":        "array",
					"description": "One entry per force: name (the force name), total (what it made or did), hours (its own online hours from the clock tool).",
					"items": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{"name": map[string]any{"type": "string"}, "total": map[string]any{"type": "number"}, "hours": map[string]any{"type": "number"}},
						"required":             []string{"name", "total", "hours"},
						"additionalProperties": false,
					},
				},
			}, "rows"),
			Call: rankByRate,
		},
		{
			Name: "rank",
			Description: "Who is ahead on any figure: give each force's name and value, get the ranking from highest down (or lowest, with higher_is_better false), " +
				"the leader and its margin over second place. Use it for every 'which is more, faster, first' verdict rather than judging the numbers yourself.",
			Schema: tools.ObjectSchema(map[string]any{
				"rows": map[string]any{
					"type":        "array",
					"description": "One entry per force or thing: name and value.",
					"items": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{"name": map[string]any{"type": "string"}, "value": map[string]any{"type": "number"}},
						"required":             []string{"name", "value"},
						"additionalProperties": false,
					},
				},
				"higher_is_better": map[string]any{"type": "boolean", "description": "true (the default) ranks the largest value first; false the smallest."},
			}, "rows"),
			Call: rank,
		},
	}
}

type rateRow struct {
	Name  string  `json:"name"`
	Total float64 `json:"total"`
	Hours float64 `json:"hours"`
}

type ranked struct {
	Rank    int      `json:"rank"`
	Name    string   `json:"name"`
	Value   float64  `json:"value"`
	Total   *float64 `json:"total,omitempty"`
	Hours   *float64 `json:"hours,omitempty"`
	PerHour *float64 `json:"per_hour,omitempty"`

	raw float64 // the unrounded figure; ranking and ties go by this, never by Value
}

type verdict struct {
	Leader        string   `json:"leader"`
	Margin        string   `json:"margin"`
	MarginPercent *float64 `json:"margin_percent,omitempty"`
	Ranking       []ranked `json:"ranking"`
	Skipped       []string `json:"skipped,omitempty"`
	Note          string   `json:"note,omitempty"`
}

// maxDecimals is the most precision a figure is shown with to tell two
// different values apart.
const maxDecimals = 4

func roundTo(v float64, decimals int) float64 {
	// A figure this large has no fraction left to round, and scaling it
	// would overflow.
	if math.Abs(v) >= 1e15 {
		return v
	}
	scale := math.Pow(10, float64(decimals))
	return math.Round(v*scale) / scale
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// showNum prints v at the given precision without trailing zeros. A figure
// that rounds to zero but is not zero keeps three significant digits, so a
// real difference never reads as "0".
func showNum(v float64, decimals int) string {
	if r := roundTo(v, decimals); r != 0 || v == 0 {
		return strconv.FormatFloat(r, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

func rankByRate(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Rows []rateRow `json:"rows"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("rows must be an array of {name, total, hours}: %v", err)
	}
	if len(in.Rows) == 0 {
		return nil, fmt.Errorf("rows is empty: give one entry per force with name, total and hours")
	}
	var rows []ranked
	var skipped []string
	for _, r := range in.Rows {
		if r.Name == "" {
			return nil, fmt.Errorf("every row needs a name")
		}
		if r.Hours <= 0 {
			skipped = append(skipped, r.Name+" (no online hours, so no rate)")
			continue
		}
		per := r.Total / r.Hours
		if !finite(per) {
			skipped = append(skipped, r.Name+" (the hours are too small for a usable rate)")
			continue
		}
		total, hours := r.Total, r.Hours
		rows = append(rows, ranked{Name: r.Name, raw: per, Total: &total, Hours: &hours, PerHour: new(float64)})
	}
	out := order(rows, true)
	out.Skipped = skipped
	out.Note = "per_hour is total divided by that force's own online hours"
	return json.Marshal(out)
}

func rank(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Rows []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
		} `json:"rows"`
		HigherIsBetter *bool `json:"higher_is_better"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("rows must be an array of {name, value}: %v", err)
	}
	if len(in.Rows) == 0 {
		return nil, fmt.Errorf("rows is empty: give one entry per force with name and value")
	}
	var rows []ranked
	for _, r := range in.Rows {
		if r.Name == "" {
			return nil, fmt.Errorf("every row needs a name")
		}
		rows = append(rows, ranked{Name: r.Name, raw: r.Value})
	}
	higher := in.HigherIsBetter == nil || *in.HigherIsBetter
	return json.Marshal(order(rows, higher))
}

// order sorts on the raw figures, numbers the ranks and names the leader
// with its margin over the runner-up. Only exactly equal figures are tied.
// Figures are shown to one decimal, or to as many as it takes (up to four)
// for neighbours that differ to still read differently.
func order(rows []ranked, higher bool) verdict {
	sort.SliceStable(rows, func(i, j int) bool {
		if higher {
			return rows[i].raw > rows[j].raw
		}
		return rows[i].raw < rows[j].raw
	})
	d := decimalsFor(rows)
	for i := range rows {
		rows[i].Rank = i + 1
		rows[i].Value = roundTo(rows[i].raw, d)
		if rows[i].PerHour != nil {
			*rows[i].PerHour = rows[i].Value
		}
	}
	v := verdict{Ranking: rows}
	if len(rows) == 0 {
		v.Leader = "nobody"
		v.Margin = "no row had a usable figure"
		return v
	}
	v.Leader = rows[0].Name
	if len(rows) == 1 {
		v.Margin = "the only entry"
		return v
	}
	first, second := rows[0].raw, rows[1].raw
	if first == second {
		v.Leader = rows[0].Name + " and " + rows[1].Name + " tied"
		v.Margin = "tied at " + showNum(first, d)
		return v
	}
	diff := math.Abs(first - second)
	if !finite(diff) {
		v.Margin = "too large to state"
		return v
	}
	v.Margin = fmt.Sprintf("%s over %s's %s", showNum(diff, d), rows[1].Name, showNum(second, d))
	if second != 0 {
		if p := diff / math.Abs(second) * 100; finite(p) {
			p = roundTo(p, d)
			v.MarginPercent = &p
		}
	}
	return v
}

// decimalsFor picks the fewest decimals, from one up to maxDecimals, at
// which every neighbouring pair of different figures shows as different.
func decimalsFor(rows []ranked) int {
	for d := 1; d < maxDecimals; d++ {
		ok := true
		for i := 1; i < len(rows); i++ {
			a, b := rows[i-1].raw, rows[i].raw
			if a != b && roundTo(a, d) == roundTo(b, d) {
				ok = false
				break
			}
		}
		if ok {
			return d
		}
	}
	return maxDecimals
}
