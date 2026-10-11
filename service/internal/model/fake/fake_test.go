package fake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

func defs(names ...string) []model.ToolDef {
	out := make([]model.ToolDef, 0, len(names)+1)
	for _, n := range names {
		out = append(out, model.ToolDef{Name: n})
	}
	return append(out, model.ToolDef{Name: "submit_answer"})
}

func ask(text string) []model.Message {
	return []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockText, Text: "Question: " + text}}}}
}

func firstBlock(t *testing.T, step model.Step) model.Block {
	t.Helper()
	if len(step.Blocks) != 1 {
		t.Fatalf("step produced %d blocks, want 1", len(step.Blocks))
	}
	return step.Blocks[0]
}

func input(t *testing.T, block model.Block) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(block.Input, &out); err != nil {
		t.Fatalf("input is not an object: %v", err)
	}
	return out
}

var everyTool = defs(
	"ai-agent-bridge-tools__current_research",
	"ai-agent-bridge-tools__item_rate",
	"ai-agent-bridge-tools__list_forces",
	"ai-agent-bridge-tools__list_players",
	"ai-agent-bridge-tools__list_surfaces",
	"ai-agent-bridge-tools__research_queue",
	"ai-agent-bridge-tools__tech_status",
	"ai-agent-bridge-tools__logistics_summary",
	"ai-agent-bridge-tools__entity_count",
	"ai-agent-bridge-tools__evolution",
	"ai-agent-bridge-tools__rockets",
	"ai-agent-bridge-tools__game_time",
	"ai-agent-bridge-tools__pollution",
	"ai-agent-bridge-tools__production_since",
	"history__last_event",
	"aab-test-provider__hello",
	"ai-agent-bridge-tools__sweep",
	"ai-agent-bridge-tools__top_items",
	"ai-agent-bridge-tools__recipe_needs",
	"recent_chat",
)

func TestFirstStepPicksAToolByKeyword(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"what is being researched", "ai-agent-bridge-tools__current_research"},
		{"iron plate rate please", "ai-agent-bridge-tools__item_rate"},
		{"what forces are there", "ai-agent-bridge-tools__list_forces"},
		{"table of players", "ai-agent-bridge-tools__list_players"},
		{"which teams are playing", "ai-agent-bridge-tools__list_forces"},
		{"when did I last die", "history__last_event"},
		{"what was my last death", "history__last_event"},
		{"hello", "aab-test-provider__hello"},
		{"what surfaces exist", "ai-agent-bridge-tools__list_surfaces"},
		{"what's in the queue", "ai-agent-bridge-tools__research_queue"},
		{"what is in the research queue", "ai-agent-bridge-tools__research_queue"},
		{"technology automation-2", "ai-agent-bridge-tools__tech_status"},
		{"tech status of automation-2", "ai-agent-bridge-tools__tech_status"},
		{"logistic network status", "ai-agent-bridge-tools__logistics_summary"},
		{"how many turrets are there", "ai-agent-bridge-tools__entity_count"},
		{"what is the evolution factor", "ai-agent-bridge-tools__evolution"},
		{"rockets launched so far", "ai-agent-bridge-tools__rockets"},
		{"what is the game time", "ai-agent-bridge-tools__game_time"},
		{"how long have we played", "ai-agent-bridge-tools__game_time"},
		{"how much pollution is there", "ai-agent-bridge-tools__pollution"},
		{"iron plate production since the start", "ai-agent-bridge-tools__production_since"},
		{"recipe needs: 60 red science a minute", "ai-agent-bridge-tools__recipe_needs"},
		{"science belts: 48 blue, 24 red, 24 green", "ai-agent-bridge-tools__recipe_needs"},
		{"what was said in chat", "recent_chat"},
	} {
		step, err := New().Step(context.Background(), "", ask(tc.question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", tc.question, err)
		}
		if got := firstBlock(t, step).Name; got != tc.want {
			t.Errorf("%q called %q, want %q", tc.question, got, tc.want)
		}
	}
}

// recipe_needs takes its targets as parallel lists, and the fake always asks
// for the same 60 red science a minute so the end-to-end harness can check the
// figures the game itself works out. The rule sits first: a ratio question
// that also says "how many", "rate" or "where" still reaches it.
func TestRecipeNeedsArguments(t *testing.T) {
	for _, question := range []string{
		"recipe needs: 60 red science a minute",
		"recipe needs: how many assemblers, at what rate, and where do they go",
	} {
		step, err := New().Step(context.Background(), "", ask(question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", question, err)
		}
		block := firstBlock(t, step)
		if block.Name != "ai-agent-bridge-tools__recipe_needs" {
			t.Fatalf("%q called %q, want recipe_needs", question, block.Name)
		}
		args := input(t, block)
		items, _ := args["items"].([]any)
		rates, _ := args["per_min"].([]any)
		if len(items) != 1 || items[0] != "automation-science-pack" {
			t.Errorf("%q: items = %v, want [automation-science-pack]", question, args["items"])
		}
		if len(rates) != 1 || rates[0] != float64(60) {
			t.Errorf("%q: per_min = %v, want [60]", question, args["per_min"])
		}
		if _, both := args["machines"]; both {
			t.Errorf("%q: sent machines beside per_min: %v", question, args)
		}
	}
}

// Pirion's question, "I have 48 blue science, 24 red, 24 green", goes out
// the way the tool's desc teaches: all three packs in one call, as machine
// counts, never per_min, on the asker's surface. Words other rules read in
// the same question do not shadow it.
func TestScienceBeltsArguments(t *testing.T) {
	for _, question := range []string{
		"science belts: I have 48 blue science, 24 red, 24 green. How much iron and copper?",
		"science belts: how many plates, at what rate, and where do the belts go",
	} {
		step, err := New().Step(context.Background(), "", ask(question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", question, err)
		}
		block := firstBlock(t, step)
		if block.Name != "ai-agent-bridge-tools__recipe_needs" {
			t.Fatalf("%q called %q, want recipe_needs", question, block.Name)
		}
		args := input(t, block)
		want := []any{"chemical-science-pack", "automation-science-pack", "logistic-science-pack"}
		items, _ := args["items"].([]any)
		if len(items) != len(want) {
			t.Fatalf("%q: items = %v, want %v", question, args["items"], want)
		}
		for i := range want {
			if items[i] != want[i] {
				t.Errorf("%q: items[%d] = %v, want %v", question, i, items[i], want[i])
			}
		}
		counts, _ := args["machines"].([]any)
		if len(counts) != 3 || counts[0] != float64(48) || counts[1] != float64(24) || counts[2] != float64(24) {
			t.Errorf("%q: machines = %v, want [48 24 24]", question, args["machines"])
		}
		if _, rate := args["per_min"]; rate {
			t.Errorf("%q: a count went out as a rate too: %v", question, args)
		}
		if args["surface"] != "nauvis" {
			t.Errorf("%q: surface = %v, want nauvis", question, args["surface"])
		}
	}
}

// The answer to Pirion's question quotes the plate rows recipe_needs
// returned, iron then copper, each figure as the companion sent it: a whole
// number as a number, a fraction as a short string. Any other row stays out.
func TestScienceBeltsAnswerQuotesThePlates(t *testing.T) {
	result := `{"found":true,"targets":[{"item":"chemical-science-pack","per_min":300}],` +
		`"rows":[{"item":"petroleum-gas","per_min":11250,"fluid":true},` +
		`{"item":"copper-plate","per_min":3060,"belts":"1.13"},` +
		`{"item":"copper-cable","per_min":5400,"belts":"2"},` +
		`{"item":"iron-plate","per_min":"5970.5","belts":"2.21"}],"total":4,"shown":4}`
	step, err := New().Step(context.Background(), "", secondTurn("science belts: 48 blue, 24 red, 24 green", result), everyTool)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	a := submitted(t, firstBlock(t, step))
	if a.Shape != agent.ShapeTable {
		t.Fatalf("shape = %q, want table: %+v", a.Shape, a)
	}
	if strings.Join(a.Columns, ",") != "item,per_min,belts" {
		t.Errorf("columns = %v, want item, per_min, belts", a.Columns)
	}
	want := [][]string{{"iron-plate", "5970.5", "2.21"}, {"copper-plate", "3060", "1.13"}}
	if len(a.Rows) != len(want) {
		t.Fatalf("rows = %v, want %v", a.Rows, want)
	}
	for i := range want {
		if strings.Join(a.Rows[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %v, want %v", i, a.Rows[i], want[i])
		}
	}
	if len(a.Columns) > agent.MaxTableColumns || len(a.Rows) > agent.MaxTableRows {
		t.Errorf("the table is over the service's caps: %+v", a)
	}
}

// A result with no plate row, or one that is not JSON at all, is quoted the
// way every other result is, so a harness reads what really came back.
func TestScienceBeltsWithoutPlatesQuotesTheResult(t *testing.T) {
	for _, result := range []string{
		`{"found":false,"unknown":["chemical-science-pack"]}`,
		`{"found":true,"rows":[{"item":"copper-cable","per_min":10}]}`,
		"aab-rpc: provider_error: no force",
	} {
		step, err := New().Step(context.Background(), "", secondTurn("science belts: 48 blue, 24 red, 24 green", result), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", result, err)
		}
		a := submitted(t, firstBlock(t, step))
		if a.Shape != agent.ShapeSummary || len(a.Lines) != 1 || a.Lines[0] != result {
			t.Errorf("%q: submitted %+v, want the result quoted as a summary", result, a)
		}
	}
}

// A question that says chat reads the chat, whatever else it says: the
// privacy scenarios ask in a private channel and in the open and compare what
// comes back, so the rule must win over players, where and time, and pass no
// arguments of its own. Without recent_chat on the server it answers at once.
func TestChatReadsTheChat(t *testing.T) {
	for _, question := range []string{
		"what was said in chat",
		"what did the players say in chat",
		"where was that chat",
		"recent chat since the time we started",
		"show me the CHAT",
	} {
		step, err := New().Step(context.Background(), "", ask(question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", question, err)
		}
		block := firstBlock(t, step)
		if block.Name != "recent_chat" {
			t.Errorf("%q called %q, want recent_chat", question, block.Name)
			continue
		}
		if args := input(t, block); len(args) != 0 {
			t.Errorf("%q: recent_chat args = %v, want none", question, args)
		}
	}
	step, _ := New().Step(context.Background(), "", ask("what was said in chat"), defs("ai-agent-bridge-tools__list_forces"))
	if got := firstBlock(t, step).Name; got != "submit_answer" {
		t.Errorf("chat without recent_chat called %q, want submit_answer", got)
	}
}

func TestItemRateArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("what is the rate of copper-plate"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["item"] != "copper-plate" || args["surface"] != "nauvis" || args["window"] != "one_minute" {
		t.Errorf("item_rate args = %v", args)
	}

	step, _ = New().Step(context.Background(), "", ask("what is the production rate"), everyTool)
	if got := input(t, firstBlock(t, step))["item"]; got != "iron-plate" {
		t.Errorf("default item = %v, want iron-plate", got)
	}
}

// "bots" routes to logistics_summary even though the question also matches
// "how many": the addendum lists logistic/bots ahead of the generic
// entity_count rule, so a robot-shaped question keeps its dedicated tool.
func TestBotsBeatsGenericEntityCount(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how many bots do we have"), everyTool)
	if got := firstBlock(t, step).Name; got != "ai-agent-bridge-tools__logistics_summary" {
		t.Errorf("called %q, want logistics_summary", got)
	}
}

func TestTechStatusArguments(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"technology automation-2", "automation-2"},
		{"tech status of automation-2", "automation-2"},
		{"tech logistics-2", "logistics-2"},
		{"what is the tech status for mining-productivity-1", "mining-productivity-1"},
	} {
		step, _ := New().Step(context.Background(), "", ask(tc.question), everyTool)
		args := input(t, firstBlock(t, step))
		if got := args["tech"]; got != tc.want {
			t.Errorf("%q: tech = %v, want %v", tc.question, got, tc.want)
		}
	}
}

func TestEntityCountArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how many turrets are there"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["name"] != "turrets" || args["surface"] != "nauvis" {
		t.Errorf("entity_count args = %v", args)
	}
}

// logistics_summary, evolution and pollution all take a required surface
// that the fake model has no way to read out of the question, so all three
// default to nauvis, matching item_rate's existing default.
func TestSurfaceDefaultsToNauvis(t *testing.T) {
	for _, tc := range []struct{ question, tool string }{
		{"logistic network status", "ai-agent-bridge-tools__logistics_summary"},
		{"what is the evolution factor", "ai-agent-bridge-tools__evolution"},
		{"how much pollution is there", "ai-agent-bridge-tools__pollution"},
	} {
		step, _ := New().Step(context.Background(), "", ask(tc.question), everyTool)
		block := firstBlock(t, step)
		if block.Name != tc.tool {
			t.Fatalf("%q called %q, want %q", tc.question, block.Name, tc.tool)
		}
		if got := input(t, block)["surface"]; got != "nauvis" {
			t.Errorf("%q: surface = %v, want nauvis", tc.question, got)
		}
	}
}

func TestProductionSinceArguments(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("iron plate production since the start"), everyTool)
	args := input(t, firstBlock(t, step))
	if args["surface"] != "nauvis" || args["item"] != "iron-plate" {
		t.Errorf("default production_since args = %v", args)
	}
	if got := args["since_tick"]; got != float64(0) {
		t.Errorf("since_tick = %v, want 0", got)
	}

	step, _ = New().Step(context.Background(), "", ask("production of copper-plate since the start"), everyTool)
	if got := input(t, firstBlock(t, step))["item"]; got != "copper-plate" {
		t.Errorf("item = %v, want copper-plate", got)
	}
}

// A question that matches nothing is answered at once, with the question
// echoed back.
func TestUnmatchedQuestionSubmitsImmediately(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("how is the weather"), everyTool)
	block := firstBlock(t, step)
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	if lines := input(t, block)["lines"].([]any); !strings.Contains(lines[0].(string), "how is the weather") {
		t.Errorf("the question was not echoed: %v", lines)
	}
}

// A tool the server does not expose falls back rather than calling something
// that is not there.
func TestMissingToolFallsBack(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("hello"), defs("ai-agent-bridge-tools__list_forces"))
	if got := firstBlock(t, step).Name; got != "ai-agent-bridge-tools__list_forces" {
		t.Errorf("hello without a provider called %q, want the list_forces fallback", got)
	}

	step, _ = New().Step(context.Background(), "", ask("what surfaces exist"), defs())
	if got := firstBlock(t, step).Name; got != "submit_answer" {
		t.Errorf("a missing tool called %q, want submit_answer", got)
	}
}

// The second step submits, carrying the tool result so a test can assert on
// real game data.
func TestSecondStepSubmitsTheToolResult(t *testing.T) {
	msgs := append(ask("table of players"),
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: "ai-agent-bridge-tools__list_players"}}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{
			Type: model.BlockToolResult, ID: "fake-1",
			Content: "{\n  \"players\": [{\"name\": \"rig\"}]\n}",
		}}},
	)
	step, err := New().Step(context.Background(), "", msgs, everyTool)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	block := firstBlock(t, step)
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	args := input(t, block)
	if args["shape"] != "table" {
		t.Errorf("shape = %v, want table", args["shape"])
	}
	rows := args["rows"].([]any)
	cell := rows[0].([]any)[0].(string)
	if !strings.Contains(cell, `"name": "rig"`) {
		t.Errorf("the tool result did not reach the artifact: %q", cell)
	}
	if strings.Contains(cell, "\n") {
		t.Errorf("the tool result was not compacted: %q", cell)
	}
}

func TestShapeByKeyword(t *testing.T) {
	for _, tc := range []struct{ question, want string }{
		{"table of players", "table"},
		{"list the players", "list"},
		{"compare the players", "comparison"},
		{"notice about players", "notice"},
		{"who are the players", "summary"},
	} {
		msgs := append(ask(tc.question),
			model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: "x"}}},
			model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: "fake-1", Content: `{"ok":true}`}}},
		)
		step, _ := New().Step(context.Background(), "", msgs, everyTool)
		if got := input(t, firstBlock(t, step))["shape"]; got != tc.want {
			t.Errorf("%q submitted shape %v, want %v", tc.question, got, tc.want)
		}
	}
}

func TestUsageIsFixed(t *testing.T) {
	step, _ := New().Step(context.Background(), "", ask("what forces are there"), everyTool)
	if step.Usage.InputTokens != 100 || step.Usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want 100 in and 20 out", step.Usage)
	}
	if step.StopReason != model.StopToolUse {
		t.Errorf("stop reason = %q, want tool_use", step.StopReason)
	}
}

// The two sweep rules Stage 5 added, and the two questions the first version
// of the made rule stole: "made" is a common past participle, and placed
// ahead of the entity_count and production_since rules it sent "how many
// turrets have we made" to a sweep of iron plate. The rule wants the shape
// of an each-team question and sits below the two it shadowed.
func TestSweepKeywordsDoNotShadowTheNarrowTools(t *testing.T) {
	for _, tc := range []struct {
		question, want string
		args           map[string]any
	}{
		{"which team has the most kills", "ai-agent-bridge-tools__sweep", map[string]any{"metric": "kills"}},
		{"how much iron plate has each team made", "ai-agent-bridge-tools__sweep", map[string]any{"metric": "item_made", "subject": "iron-plate"}},
		{"which team made the most iron plate", "ai-agent-bridge-tools__sweep", map[string]any{"metric": "item_made", "subject": "iron-plate"}},
		{"how many turrets have we made", "ai-agent-bridge-tools__entity_count", nil},
		{"iron plate made since the start", "ai-agent-bridge-tools__production_since", nil},
		// A which-ITEM question ranks items within one force, not forces.
		{"which item have we made the most of", "ai-agent-bridge-tools__top_items", nil},
	} {
		step, err := New().Step(context.Background(), "", ask(tc.question), everyTool)
		if err != nil {
			t.Fatalf("%q: %v", tc.question, err)
		}
		block := firstBlock(t, step)
		if block.Name != tc.want {
			t.Errorf("%q called %q, want %q", tc.question, block.Name, tc.want)
			continue
		}
		if tc.args == nil {
			continue
		}
		args := input(t, block)
		for key, want := range tc.args {
			if got := args[key]; got != want {
				t.Errorf("%q: arg %s = %v, want %v", tc.question, key, got, want)
			}
		}
	}
}

// secondTurn is the conversation after the tool has run: the question, the
// call that was made, and what the tool returned.
func secondTurn(question, result string) []model.Message {
	return append(ask(question),
		model.Message{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockToolUse, ID: "fake-1", Name: "x"}}},
		model.Message{Role: model.RoleUser, Blocks: []model.Block{{Type: model.BlockToolResult, ID: "fake-1", Content: result}}},
	)
}

// submitted decodes a submit_answer block the way the service does.
func submitted(t *testing.T, block model.Block) agent.Artifact {
	t.Helper()
	if block.Name != "submit_answer" {
		t.Fatalf("called %q, want submit_answer", block.Name)
	}
	var a agent.Artifact
	if err := json.Unmarshal(block.Input, &a); err != nil {
		t.Fatalf("the artifact is not one the service reads: %v", err)
	}
	return a
}

// bodyCell is the one cell of an artifact that carries the tool result,
// whatever the shape.
func bodyCell(t *testing.T, a agent.Artifact) string {
	t.Helper()
	switch {
	case len(a.Lines) > 0:
		return a.Lines[0]
	case a.Text != "":
		return a.Text
	case len(a.Items) > 0:
		return a.Items[0]
	case len(a.Rows) > 0 && len(a.Rows[0]) > 0:
		return a.Rows[0][0]
	case len(a.Pairs) > 0:
		return a.Pairs[0].A
	}
	t.Fatalf("no cell carries the tool result: %+v", a)
	return ""
}

var oneOfEachShape = []struct{ question, shape string }{
	{"who are the players", agent.ShapeSummary},
	{"table of players", agent.ShapeTable},
	{"list the players", agent.ShapeList},
	{"compare the players", agent.ShapeComparison},
	{"notice about players", agent.ShapeNotice},
}

// The artifact carries the first 300 runes of what the tool returned, in
// every shape. At 160 an end-to-end answer ran to no more than two chat
// lines. Counted in runes: a byte clip would cut a multi-byte character in
// half, and 300 runes of it are still one cell to the service.
func TestArtifactCarriesThreeHundredRunesOfTheResult(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		want         int
	}{
		{"ascii far past the clip", strings.Repeat("a", 1000), 300},
		{"two-byte runes far past the clip", strings.Repeat("é", 1000), 300},
		{"three-byte runes far past the clip", strings.Repeat("日", 1000), 300},
		{"one rune past the clip", strings.Repeat("a", 301), 300},
		{"exactly the clip", strings.Repeat("a", 300), 300},
		{"one rune under the clip", strings.Repeat("a", 299), 299},
		{"what the old clip cut at 160", strings.Repeat("a", 200), 200},
		{"two-byte runes under the clip, over it in bytes", strings.Repeat("é", 200), 200},
	} {
		for _, shape := range oneOfEachShape {
			step, err := New().Step(context.Background(), "", secondTurn(shape.question, tc.result), everyTool)
			if err != nil {
				t.Fatalf("%s, %s: %v", tc.name, shape.shape, err)
			}
			a := submitted(t, firstBlock(t, step))
			if a.Shape != shape.shape {
				t.Errorf("%s: %q submitted shape %q, want %q", tc.name, shape.question, a.Shape, shape.shape)
			}
			body := bodyCell(t, a)
			if got := utf8.RuneCountInString(body); got != tc.want {
				t.Errorf("%s, %s: the cell holds %d runes, want %d", tc.name, shape.shape, got, tc.want)
			}
			if !utf8.ValidString(body) {
				t.Errorf("%s, %s: the clip cut a character in half", tc.name, shape.shape)
			}
			if !strings.HasPrefix(tc.result, body) {
				t.Errorf("%s, %s: the cell is not the start of the tool result", tc.name, shape.shape)
			}
			if got := utf8.RuneCountInString(body); got > agent.MaxCellChars {
				t.Errorf("%s, %s: %d runes is over the service's %d-character cell", tc.name, shape.shape, got, agent.MaxCellChars)
			}
		}
	}
}

// The other two places the fake clips put a few words in front of the cut
// text: the echo of a question nothing matched, and the first exchange a
// session recalls. They must still fit one cell, or the service sends the
// answer back to the model to rewrite and the harness never sees the fake's.
func TestEchoAndRecallFitTheCellCap(t *testing.T) {
	long := strings.Repeat("x", 1000)
	recalled := []model.Message{{Role: model.RoleUser, Blocks: []model.Block{{
		Type: model.BlockText, Text: "Bob asked: " + long + "\nQuestion: recall",
	}}}}
	for _, tc := range []struct {
		name string
		msgs []model.Message
	}{
		{"the echo of a question nothing matched", ask(long)},
		{"the first exchange a session recalls", recalled},
	} {
		step, err := New().Step(context.Background(), "", tc.msgs, everyTool)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		a := submitted(t, firstBlock(t, step))
		if len(a.Lines) == 0 {
			t.Fatalf("%s: no lines: %+v", tc.name, a)
		}
		for _, line := range a.Lines {
			if n := utf8.RuneCountInString(line); n > agent.MaxCellChars {
				t.Errorf("%s: a line of %d runes is over the service's %d-character cell: %q", tc.name, n, agent.MaxCellChars, line)
			}
		}
		longest := 0
		for _, line := range a.Lines {
			if n := utf8.RuneCountInString(line); n > longest {
				longest = n
			}
		}
		if longest < 300 {
			t.Errorf("%s: the longest line holds %d runes, want at least 300 of the question", tc.name, longest)
		}
	}
}
