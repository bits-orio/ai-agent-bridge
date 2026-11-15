package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// cmdTool answers each call from replies in turn (the last repeats) and
// keeps the arguments it was called with.
type cmdTool struct {
	replies []string
	err     error
	args    []map[string]any
}

func (r *cmdTool) tool(name string) tools.Tool {
	return tools.Tool{
		Name:   name,
		Schema: tools.ObjectSchema(map[string]any{"force": map[string]any{"type": "string"}, "metric": map[string]any{"type": "string"}}, "force"),
		Call: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var a map[string]any
			_ = json.Unmarshal(raw, &a)
			r.args = append(r.args, a)
			if r.err != nil {
				return nil, r.err
			}
			i := min(len(r.args), len(r.replies)) - 1
			return json.RawMessage(r.replies[i]), nil
		},
	}
}

// silentModel fails the test the moment a free command reaches the model.
func silentModel() *scriptedModel {
	return &scriptedModel{steps: []model.Step{textStep("the model must not be asked")}}
}

func rocketsReply(total int) string {
	rows := []string{}
	for i := 0; i < 10 && i < total; i++ {
		rows = append(rows, `["team-`+string(rune('a'+i))+`",`+string(rune('9'-i))+`]`)
	}
	return `{"sweep_v":1,"axis":"force","metric":"rockets","unit":"count","cols":["name","value"],"rows":[` +
		strings.Join(rows, ",") + `],"total":` + itoa(total) + `,"shown":` + itoa(min(total, 10)) + `,"skipped":0}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

const cards = `[{"metric":"built","axes":["force"]},{"metric":"logistic_robots","axes":["force","surface"]},{"metric":"rockets","axes":["force"]},{"metric":"item_made","axes":["force"],"subject":"the item"}]`

func TestTopAnswersWithATableAndNoModelRound(t *testing.T) {
	sweep := &cmdTool{replies: []string{rocketsReply(12)}}
	m := silentModel()
	a := New(m, caps())
	dir := t.TempDir()
	a.Ledger = ledger.Open(dir, true)

	res, err := a.Answer(context.Background(), Question{ID: 7, Text: "top rockets", PlayerIndex: player(1), Force: "team-c"}, []tools.Tool{sweep.tool(SweepTool)})
	if err != nil {
		t.Fatal(err)
	}
	art := res.Artifact
	if art.Shape != ShapeTable || art.ToAsker {
		t.Fatalf("top rockets = %+v, want a public table", art)
	}
	if strings.Join(art.Columns, "|") != "#|force|value" || len(art.Rows) != MaxTableRows {
		t.Errorf("columns %v rows %d, want #|force|value and %d rows", art.Columns, len(art.Rows), MaxTableRows)
	}
	if strings.Join(art.Rows[0], "|") != "1|team-a|9" {
		t.Errorf("first row %v, want the companion's first row as it sorted it", art.Rows[0])
	}
	if !strings.Contains(art.Title, "rockets") || !strings.Contains(art.Title, "8 of 12") {
		t.Errorf("title %q should name the metric and say 8 of 12 are shown", art.Title)
	}
	if got := sweep.args[0]; got["metric"] != "rockets" || got["limit"] != float64(10) || got["force"] != "team-c" || got["subject"] != nil {
		t.Errorf("sweep called with %v", got)
	}
	if m.calls != 0 || res.Rounds != 0 || res.CostUSD != 0 {
		t.Errorf("model calls %d rounds %d cost %v, want none", m.calls, res.Rounds, res.CostUSD)
	}
	a.Ledger.Close()
	rec := findQuestion(t, dir, 7)
	if rec.Command != CommandTop || rec.Lookups != 1 || rec.Rounds != 0 || rec.Refused || rec.Shape != ShapeTable {
		t.Errorf("ledger record %+v, want command top with one lookup and no round", rec)
	}
}

func TestTopPassesASubject(t *testing.T) {
	sweep := &cmdTool{replies: []string{`{"sweep_v":1,"axis":"force","metric":"item_made","subject":"iron-plate","unit":"items","cols":["name","value"],"rows":[["player","93.5"]],"total":1,"shown":1}`}}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "top item_made iron-plate", PlayerIndex: player(1)}, []tools.Tool{sweep.tool(SweepTool)})
	if sweep.args[0]["subject"] != "iron-plate" || res.Artifact.Shape != ShapeTable || res.Artifact.Rows[0][2] != "93.5" {
		t.Errorf("args %v, answer %+v", sweep.args[0], res.Artifact)
	}
	if !strings.Contains(res.Artifact.Title, "iron-plate") || strings.Contains(res.Artifact.Title, " of ") {
		t.Errorf("title %q should carry the subject and claim no cut", res.Artifact.Title)
	}
}

// An unknown name is the companion's to judge: its reply lists the metrics,
// and the notice lists them back, never a list of this service's own.
func TestTopUnknownMetricListsTheCompanionsMetrics(t *testing.T) {
	sweep := &cmdTool{replies: []string{`{"sweep_v":1,"found":false,"metric":"science","reason":"unknown sweep metric: science","metrics":` + cards + `}`}}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "top science", PlayerIndex: player(1)}, []tools.Tool{sweep.tool(SweepTool)})
	art := res.Artifact
	if art.Shape != ShapeNotice || !art.ToAsker {
		t.Fatalf("top science = %+v, want a notice to the asker", art)
	}
	for _, want := range []string{"nknown sweep metric: science", "built", "logistic_robots", "item_made"} {
		if !strings.Contains(art.Text, want) {
			t.Errorf("notice %q is missing %q", art.Text, want)
		}
	}
	if len(sweep.args) != 1 {
		t.Errorf("%d sweep calls, want 1: no spelling of science is a metric", len(sweep.args))
	}
}

// A friendly spelling the companion did not know is read against the
// metrics its reply listed and tried once more: two words for one metric,
// a singular for a plural.
func TestTopFriendlySpellings(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"top logistic robots", "logistic_robots"},
		{"top rocket", "rockets"},
		{"top logistic-robots", "logistic_robots"},
	} {
		unknown := `{"sweep_v":1,"found":false,"reason":"unknown sweep metric","metrics":` + cards + `}`
		sweep := &cmdTool{replies: []string{unknown, rocketsReply(2)}}
		a := New(silentModel(), caps())
		dir := t.TempDir()
		a.Ledger = ledger.Open(dir, true)
		res, _ := a.Answer(context.Background(), Question{ID: 3, Text: tc.text, PlayerIndex: player(1)}, []tools.Tool{sweep.tool(SweepTool)})
		a.Ledger.Close()
		if len(sweep.args) != 2 || sweep.args[1]["metric"] != tc.want || sweep.args[1]["subject"] != nil {
			t.Errorf("%s: calls %v, want a second call for %s with no subject", tc.text, sweep.args, tc.want)
			continue
		}
		if res.Artifact.Shape != ShapeTable {
			t.Errorf("%s: answer %+v", tc.text, res.Artifact)
		}
		if rec := findQuestion(t, dir, 3); rec.Lookups != 2 {
			t.Errorf("%s: lookups %d, want 2, both calls counted", tc.text, rec.Lookups)
		}
	}
}

// A refusal that carries no metric list (a metric that needs a subject)
// says the companion's own reason.
func TestTopRefusalSaysTheCompanionsReason(t *testing.T) {
	sweep := &cmdTool{replies: []string{`{"sweep_v":1,"found":false,"metric":"entities","axis":"force","reason":"entities needs a subject: the entity to count"}`}}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "top entities", PlayerIndex: player(1)}, []tools.Tool{sweep.tool(SweepTool)})
	if res.Artifact.Shape != ShapeNotice || !res.Artifact.ToAsker || !strings.Contains(res.Artifact.Text, "needs a subject: the entity to count") {
		t.Errorf("answer %+v", res.Artifact)
	}
}

// An older companion has no sweep or no force_card: the asker is told the
// server's mod is too old, and nothing is spent.
func TestFreeCommandsOnACompanionWithoutTheTool(t *testing.T) {
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	a := New(silentModel(), c)
	dir := t.TempDir()
	a.Ledger = ledger.Open(dir, true)
	for i, text := range []string{"top rockets", "card"} {
		res, _ := a.Answer(context.Background(), Question{ID: int64(i + 1), Text: text, PlayerIndex: player(1)}, nil)
		if res.Artifact.Shape != ShapeNotice || !res.Artifact.ToAsker || !strings.Contains(res.Artifact.Text, "too old") {
			t.Errorf("%s with no tool = %+v", text, res.Artifact)
		}
	}
	a.Ledger.Close()
	if rec := findQuestion(t, dir, 2); rec.Lookups != 0 || rec.Command != CommandCard {
		t.Errorf("record %+v, want command card with no lookup", rec)
	}
	if !a.quota.take("player:1", a.now()) {
		t.Error("a command the companion could not run spent the asker's quota")
	}
}

const cardReply = `{"construction_robots":40,"current_research":"logistics","enemies_killed":55,"force":"player","found":true,"logistic_robots":125,"partial_window":true,"players_online":1,"players_total":2,"queue":2,"research_progress":"0.25","researched":1,"robots_total":165,"rockets":7,"surfaces":2,"top_items":[{"item":"copper-plate","per_min":180},{"item":"iron-plate","per_min":"93.5"}],"window":"one_hour"}`

func cardValue(t *testing.T, art Artifact, what string) string {
	t.Helper()
	for _, row := range art.Rows {
		if row[0] == what {
			return row[1]
		}
	}
	t.Errorf("card %+v has no %q row", art, what)
	return ""
}

func TestCardOfTheAskersForce(t *testing.T) {
	card := &cmdTool{replies: []string{cardReply}}
	m := silentModel()
	a := New(m, caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "card", PlayerIndex: player(1), Force: "player"}, []tools.Tool{card.tool(ForceCardTool)})
	art := res.Artifact
	if art.Shape != ShapeTable || art.ToAsker || len(art.Columns) != 2 || len(art.Rows) > MaxTableRows {
		t.Fatalf("card = %+v, want a public two-column table", art)
	}
	if card.args[0]["force"] != "player" {
		t.Errorf("force_card called with %v, want the asker's force", card.args[0])
	}
	wantOrder := []string{"players", "research", "technologies researched", "rockets", "robots", "enemies killed", "top items"}
	for i, want := range wantOrder {
		if i >= len(art.Rows) || art.Rows[i][0] != want {
			t.Fatalf("rows %v, want in order %v", art.Rows, wantOrder)
		}
	}
	if got := cardValue(t, art, "players"); got != "1 online of 2" {
		t.Errorf("players %q", got)
	}
	// research_progress arrives as a decimal string, the companion's rule
	// for a fraction.
	if got := cardValue(t, art, "research"); !strings.Contains(got, "[img=technology.logistics]") || !strings.Contains(got, "25%") {
		t.Errorf("research %q", got)
	}
	if got := cardValue(t, art, "technologies researched"); got != "1" {
		t.Errorf("technologies researched %q", got)
	}
	// per_min may be a whole number or a decimal string.
	if got := cardValue(t, art, "top items"); got != "[img=item.copper-plate] 180/min, [img=item.iron-plate] 93.5/min" {
		t.Errorf("top items %q", got)
	}
	if m.calls != 0 {
		t.Errorf("the model was called %d times", m.calls)
	}
}

func TestCardSkipsAbsentKeysAndSaysIdle(t *testing.T) {
	card := &cmdTool{replies: []string{`{"found":true,"force":"team-2","players_total":0,"players_online":0,"researched":3,"research_progress":0}`}}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "card team-2", PlayerIndex: player(1)}, []tools.Tool{card.tool(ForceCardTool)})
	if card.args[0]["force"] != "team-2" {
		t.Errorf("force_card called with %v", card.args[0])
	}
	if got := cardValue(t, res.Artifact, "research"); got != "idle" {
		t.Errorf("research %q, want idle", got)
	}
	if len(res.Artifact.Rows) != 3 {
		t.Errorf("rows %v, want players, research and technologies only", res.Artifact.Rows)
	}
}

// A label is swapped for its force name the way a question's text is.
func TestCardByLabel(t *testing.T) {
	card := &cmdTool{replies: []string{cardReply}}
	a := New(silentModel(), caps())
	q := Question{ID: 1, Text: "card Team Ace", PlayerIndex: player(1), Labels: []ForceLabel{{Name: "team-2", Label: "Team Ace"}}}
	a.Answer(context.Background(), q, []tools.Tool{card.tool(ForceCardTool)})
	if card.args[0]["force"] != "team-2" {
		t.Errorf("force_card called with %v, want team-2", card.args[0])
	}
}

func TestCardUnknownForceSaysTheCompanionsMessage(t *testing.T) {
	card := &cmdTool{err: errors.New("aab-rpc: provider_error: unknown force: nobody (use the force name, not a display name)")}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "card nobody", PlayerIndex: player(1)}, []tools.Tool{card.tool(ForceCardTool)})
	if res.Artifact.Shape != ShapeNotice || !res.Artifact.ToAsker || !strings.Contains(res.Artifact.Text, "unknown force: nobody") || strings.Contains(res.Artifact.Text, "aab-rpc") {
		t.Errorf("answer %+v", res.Artifact)
	}
}

func TestHelpListsTheCommandsToTheAskerForFree(t *testing.T) {
	c := caps()
	c.QuestionsPerPlayerPerHour = 1
	m := silentModel()
	a := New(m, c)
	for i := 0; i < 3; i++ {
		res, _ := a.Answer(context.Background(), Question{ID: int64(i + 1), Text: "help", PlayerIndex: player(1)}, nil)
		art := res.Artifact
		if art.Shape != ShapeList || !art.ToAsker {
			t.Fatalf("help = %+v, want a list to the asker", art)
		}
		joined := strings.Join(art.Items, "\n")
		for _, want := range []string{"top <metric>", "card [force]", "new", "#name", "sessions"} {
			if !strings.Contains(joined, want) {
				t.Errorf("help is missing %q:\n%s", want, joined)
			}
		}
		for _, item := range art.Items {
			if len([]rune(item)) > 100 {
				t.Errorf("help line too long: %q", item)
			}
		}
	}
	if m.calls != 0 || !a.quota.take("player:1", a.now()) {
		t.Errorf("help reached the model (%d) or spent quota", m.calls)
	}
}

// top and card cost RCON, so they count against the asker's and the
// server's hourly quotas like a question does, but never the day's budget.
func TestLookupCommandsSpendTheQuotas(t *testing.T) {
	c := caps()
	c.QuestionsPerPlayerPerHour = 2
	c.QuestionsPerHour = 3
	sweep := &cmdTool{replies: []string{rocketsReply(2)}}
	a := New(silentModel(), c)
	ts := []tools.Tool{sweep.tool(SweepTool)}
	ask := func(id int64, who int) Artifact {
		res, _ := a.Answer(context.Background(), Question{ID: id, Text: "top rockets", PlayerIndex: player(who)}, ts)
		return res.Artifact
	}
	ask(1, 1)
	ask(2, 1)
	if got := ask(3, 1); got.Text != quotaNotice {
		t.Errorf("third top from one player = %+v, want the quota notice", got)
	}
	ask(4, 2)
	if got := ask(5, 3); got.Text != serverQuotaNotice {
		t.Errorf("fourth top on the server = %+v, want the server quota notice", got)
	}
	if len(sweep.args) != 3 {
		t.Errorf("%d sweep calls, want 3: a refused command runs nothing", len(sweep.args))
	}
}

// The companion encodes an empty Lua array as {}: a fresh map's card has no
// top items, and a sweep with nothing to rank has no rows.
func TestEmptyListsArriveAsObjects(t *testing.T) {
	card := &cmdTool{replies: []string{`{"found":true,"force":"player","players_total":0,"players_online":0,"researched":0,"top_items":{},"window":"one_hour"}`}}
	a := New(silentModel(), caps())
	res, _ := a.Answer(context.Background(), Question{ID: 1, Text: "card", PlayerIndex: player(1)}, []tools.Tool{card.tool(ForceCardTool)})
	if res.Artifact.Shape != ShapeTable || cardValue(t, res.Artifact, "technologies researched") != "0" {
		t.Errorf("card with top_items {} = %+v", res.Artifact)
	}
	sweep := &cmdTool{replies: []string{`{"sweep_v":1,"axis":"force","metric":"rockets","cols":["name","value"],"rows":{},"total":0,"shown":0}`}}
	res, _ = a.Answer(context.Background(), Question{ID: 2, Text: "top rockets", PlayerIndex: player(1)}, []tools.Tool{sweep.tool(SweepTool)})
	if res.Artifact.Shape != ShapeSummary || !strings.Contains(res.Artifact.Line(), "Nothing to rank") {
		t.Errorf("top with rows {} = %+v", res.Artifact)
	}
}
