// D1: the sweep ranker.
//
// A sweep reply already comes back with its rows sorted by value descending,
// force name as tiebreak (docs/design/phase5-sweep.md, "rows are always
// sorted by value descending ... BEFORE any cut"), so the leader is always
// row zero. What it does not carry is the margin over the runner-up, and
// without this the model would spend a whole extra round handing those same
// rows to the rank tool just to get one. rankSweep runs that round here
// instead, on the way back through the catalog, by calling through to the
// exact same "rank" tool arith.Tools() already exports: one implementation
// of leader-and-margin, not a second one that can drift from the first.
//
// This is the first place the service rewrites a provider's reply instead of
// forwarding it verbatim, which the design records as a real risk (phase5
// contract, Unit D1). The gate guarding it is deliberately narrow and fails
// closed: on anything that is not unmistakably a genuine sweep reply,
// rankSweep returns the original bytes, untouched, not a re-marshaled
// equivalent of them.
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/bits-orio/ai-agent-bridge/service/internal/arith"
)

// sweepCols is the exact two-column shape a genuine sweep envelope carries
// (phase5-sweep.md's envelope example: `"cols": ["name", "value"]`). Every
// metric's axis cell is a (name, value) pair regardless of what the metric
// measures; rankSweep will not rank a reply whose cols say anything else,
// even one that is otherwise shaped like a table.
var sweepCols = []string{"name", "value"}

// rankTool is arith's own "rank" tool, resolved once at package init.
// arith.Tools() builds a fresh slice of closures on every call, but rank
// itself carries no state, so finding it once avoids walking that slice on
// every tool reply for the life of the process.
var rankTool = mustFindRankTool()

func mustFindRankTool() func(context.Context, json.RawMessage) (json.RawMessage, error) {
	for _, t := range arith.Tools() {
		if t.Name == "rank" {
			return t.Call
		}
	}
	// arith.Tools() is a fixed, small, in-repo list; losing "rank" from it is
	// a build-time contract break, not something a live server can hit.
	panic("catalog: arith.Tools() no longer exposes a \"rank\" tool")
}

// rankSweep ranks a sweep reply's rows through arith's rank tool and adds
// the leader and its margin as new top-level fields, so the model reads the
// verdict out of the same round that fetched the rows. Every field the reply
// already carried survives untouched. On anything that does not clear the
// gate below, rankSweep returns raw exactly as it received it.
func rankSweep(ctx context.Context, raw json.RawMessage) json.RawMessage {
	rows, ok := sweepRankableRows(raw)
	if !ok {
		return raw
	}

	args, err := json.Marshal(struct {
		Rows []rankRow `json:"rows"`
	}{rows})
	if err != nil {
		return raw
	}
	verdictRaw, err := rankTool(ctx, args)
	if err != nil {
		return raw
	}
	var verdict struct {
		Leader        string   `json:"leader"`
		Margin        string   `json:"margin"`
		MarginPercent *float64 `json:"margin_percent,omitempty"`
	}
	if err := json.Unmarshal(verdictRaw, &verdict); err != nil {
		return raw
	}

	// Merge onto the reply's own fields rather than building a new object
	// from scratch, so a field a future metric adds rides through unchanged
	// even though this code has never heard of it.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw // unreachable: sweepRankableRows already parsed raw as an object.
	}
	leaderJSON, err := marshalNoEscape(verdict.Leader)
	if err != nil {
		return raw
	}
	marginJSON, err := marshalNoEscape(verdict.Margin)
	if err != nil {
		return raw
	}
	fields["leader"] = leaderJSON
	fields["margin"] = marginJSON
	if verdict.MarginPercent != nil {
		marginPctJSON, err := marshalNoEscape(*verdict.MarginPercent)
		if err != nil {
			return raw
		}
		fields["margin_percent"] = marginPctJSON
	} else {
		// arith leaves the percentage out when second place is zero, since
		// there is no percentage of nothing. A reply that already carried one
		// would otherwise keep it beside a margin that contradicts it.
		delete(fields, "margin_percent")
	}
	out, err := marshalNoEscape(fields)
	if err != nil {
		return raw
	}
	return out
}

// marshalNoEscape is json.Marshal without the HTML escaping json.Marshal
// applies by default. The rows carry names players typed, and a platform can
// be called "A & B <fast>": json.Marshal turns each of those characters into
// six bytes of \u escape, on a reply that agent.go cuts at 4096 bytes, and
// does it to the rows this function promises to forward untouched, because
// a RawMessage is re-encoded on the way through. The companion never escaped
// them, so the ranker must not start.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// rankRow is one row of arith's rank tool's own "rows" argument.
type rankRow struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// sweepRankableRows is the hard gate. It reports the reply's rows as
// {name, value} pairs and true only when every one of these holds:
//
//   - the reply decodes as a JSON object
//   - sweep_v is present and exactly 1 (the positive sweep marker)
//   - found is either absent or not false (a refusal also carries sweep_v:1,
//     per the contract, with no rows: "A refusal is
//     {"sweep_v":1, "found": false, ...} with no rows")
//   - cols is exactly ["name", "value"]
//   - rows is non-empty and every row is a two-element array of one
//     non-empty string followed by one number
//
// Anything else, including a reply that decodes fine but fails just one of
// these, reports false and rankSweep leaves the reply exactly as it arrived.
func sweepRankableRows(raw json.RawMessage) ([]rankRow, bool) {
	var env struct {
		SweepV *int              `json:"sweep_v"`
		Found  *bool             `json:"found"`
		Cols   []string          `json:"cols"`
		Rows   []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false
	}
	if env.SweepV == nil || *env.SweepV != 1 {
		return nil, false
	}
	if env.Found != nil && !*env.Found {
		return nil, false
	}
	if !sameCols(env.Cols, sweepCols) {
		return nil, false
	}
	if len(env.Rows) == 0 {
		return nil, false
	}

	rows := make([]rankRow, 0, len(env.Rows))
	for _, r := range env.Rows {
		var cells []json.RawMessage
		if err := json.Unmarshal(r, &cells); err != nil || len(cells) != 2 {
			return nil, false
		}
		var name string
		if err := json.Unmarshal(cells[0], &name); err != nil || name == "" {
			return nil, false
		}
		value, ok := numberCell(cells[1])
		if !ok {
			return nil, false
		}
		rows = append(rows, rankRow{Name: name, Value: value})
	}
	return rows, true
}

func sameCols(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// numberCell reads a value cell as a number whether the companion sent it as
// one or as a string. It sends every fraction as a short decimal string,
// because the engine's JSON writer prints a rounded 0.79 as fifty digits
// otherwise (bounded.round's own comment), and the briefing decoders learned
// the same lesson the hard way: a strict float64 here rejected every fraction
// and turned the ranker into the identity on any metric with decimals, which
// is why the research metric rounded itself to whole numbers to stay ranked.
func numberCell(raw json.RawMessage) (float64, bool) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
