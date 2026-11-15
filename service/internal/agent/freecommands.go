// The free commands: help, top and card. Each answers from one game read, or
// none, with no model round, so it costs RCON and never money. top and card
// still count against the hourly quotas, which exist to bound what one player
// can make the game do; help reads nothing and counts against nothing.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// The catalog names of the two companion tools the commands read. The
// companion's provider interface is fixed (CONTEXT.md invariant 6).
var (
	SweepTool     = catalog.ToolName(companionTools, "sweep")
	ForceCardTool = catalog.ToolName(companionTools, "force_card")
)

const companionTools = "ai-agent-bridge-tools"

// topLimit is how many rows top asks the sweep for: two more than a table
// shows, so the title can say when more exist without a second call.
const topLimit = 10

// helpItems is what help prints, one short line each.
var helpItems = []string{
	"Ask any question in plain words.",
	"top <metric> [item]: who leads, e.g. top rockets, top researched, top item_made iron-plate",
	"card [force]: one force at a glance",
	"new: start a fresh session; new <question> asks in one",
	"#name <question>: ask in a named session",
	"sessions: list the sessions here",
}

// tooOldNotice is the answer when the catalog lacks the command's tool: the
// server runs a companion from before it.
func tooOldNotice(command string) string {
	return "The server's mod is too old for the " + command + " command. Ask it as a question instead."
}

// freeCommand answers help, top or card. It reports false for any other
// command, which the caller answers itself.
func (a *Agent) freeCommand(ctx context.Context, q Question, req Request, mark SessionMark, now time.Time, rawText string, ts []tools.Tool) (Result, bool) {
	record := func(res Result, lookups int, msRCON int64, refused *string) Result {
		rec := a.questionRecord(q, rawText, mark, res.Artifact.Shape, 0, lookups, 0, 0, msRCON, refused != nil, refused, nil, false, ledger.BriefingOff, 0, 0, 0, false, false)
		rec.Command = req.Command
		a.writeQuestion(rec)
		return res
	}
	var name string
	switch req.Command {
	case CommandHelp:
		art := Artifact{Shape: ShapeList, Title: "What you can type", Items: helpItems, ToAsker: true}
		return record(Result{Artifact: art, Session: mark}, 0, 0, nil), true
	case CommandTop:
		name = SweepTool
	case CommandCard:
		name = ForceCardTool
	default:
		return Result{}, false
	}
	t, ok := index(ts)[name]
	if !ok {
		return record(Result{Artifact: refusal(tooOldNotice(req.Command)), Session: mark}, 0, 0, nil), true
	}
	if !a.quota.take(q.key(), now) {
		return record(Result{Artifact: refusal(quotaNotice), Session: mark}, 0, 0, strPtr(quotaNotice)), true
	}
	if !a.server.take(serverKey, now) {
		a.quota.refund(q.key(), now)
		return record(Result{Artifact: refusal(serverQuotaNotice), Session: mark}, 0, 0, strPtr(serverQuotaNotice)), true
	}
	ctx = catalog.WithForce(ctx, q.force())
	call := &commandCall{ctx: ctx, tool: t, force: q.force()}
	var art Artifact
	if req.Command == CommandTop {
		art = top(call, req.Arg)
	} else {
		art = card(call, substituteLabels(req.Arg, q.Labels), q.force())
	}
	if _, err := validate(art); err != nil {
		art = Notice(LevelWarning, stalledNotice)
	}
	return record(Result{Artifact: art, Session: mark}, call.lookups, call.ms, nil), true
}

// commandCall runs a command's tool and counts what it spent.
type commandCall struct {
	ctx     context.Context
	tool    tools.Tool
	force   string
	lookups int
	ms      int64
}

// run calls the tool with args, the force filled in by the rule a game tool
// call from the model gets (fillForce).
func (c *commandCall) run(args map[string]any) (json.RawMessage, error) {
	raw, _ := json.Marshal(args) // strings and numbers always encode
	started := time.Now()
	out, err := c.tool.Call(c.ctx, fillForce(c.tool, raw, c.force))
	c.lookups++
	c.ms += time.Since(started).Milliseconds()
	return out, err
}

// num is a number from a companion reply: a JSON number for a whole value
// and a short decimal string for a fraction, the companion's rounding rule.
type num string

func (n *num) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*n = num(s)
		return nil
	}
	var f json.Number
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = num(f.String())
	return nil
}

// list is a JSON array from a companion reply. The companion's encoder
// cannot tell an empty Lua array from an empty object and sends {} for both,
// so {} reads as an empty list.
type list[T any] []T

func (l *list[T]) UnmarshalJSON(b []byte) error {
	if t := strings.TrimSpace(string(b)); t == "{}" || t == "null" {
		*l = nil
		return nil
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*l = out
	return nil
}

func (n num) float() (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	return f, err == nil
}

// sweepReply is the sweep tool's reply, a ranking or a refusal
// (companion-mod/scripts/sweep/envelope.lua and walk.lua).
type sweepReply struct {
	Found   *bool                   `json:"found"`
	Axis    string                  `json:"axis"`
	Metric  string                  `json:"metric"`
	Subject string                  `json:"subject"`
	Unit    string                  `json:"unit"`
	Rows    list[[]json.RawMessage] `json:"rows"`
	Total   int                     `json:"total"`
	Reason  string                  `json:"reason"`
	Metrics list[struct {
		Metric string `json:"metric"`
	}] `json:"metrics"`
}

func (r sweepReply) metricNames() []string {
	out := make([]string, 0, len(r.Metrics))
	for _, m := range r.Metrics {
		if m.Metric != "" {
			out = append(out, m.Metric)
		}
	}
	return out
}

// top ranks one metric. arg is the metric word and at most one more: a
// subject, or the second half of a two-word metric name. A name the
// companion does not know is read once more against the metrics its reply
// lists (friendlyMetric); the rows come back sorted by the companion and are
// shown as they are.
func top(call *commandCall, arg string) Artifact {
	metric, subject, _ := strings.Cut(arg, " ")
	reply, err := sweep(call, metric, subject)
	if err == nil && reply.Found != nil && !*reply.Found {
		if retry, sub, ok := friendlyMetric(metric, subject, reply.metricNames()); ok {
			reply, err = sweep(call, retry, sub)
		}
	}
	if err != nil {
		return refusal("The ranking could not be read: " + companionMessage(err))
	}
	if reply.Found != nil && !*reply.Found {
		return refusal(sweepRefusal(reply))
	}
	if len(reply.Rows) == 0 {
		return Summary(fmt.Sprintf("Nothing to rank for %s yet.", metricLabel(reply, metric)))
	}
	axis := reply.Axis
	if axis == "" {
		axis = "name"
	}
	shown := min(len(reply.Rows), MaxTableRows)
	art := Artifact{Shape: ShapeTable, Title: "Top " + metricLabel(reply, metric), Columns: []string{"#", axis, "value"}}
	if total := max(reply.Total, len(reply.Rows)); total > shown {
		art.Title += fmt.Sprintf(", %d of %d shown", shown, total)
	}
	for i, row := range reply.Rows[:shown] {
		cells := []string{strconv.Itoa(i + 1), "", ""}
		for j := 0; j < 2 && j < len(row); j++ {
			cells[j+1] = cellText(row[j])
		}
		art.Rows = append(art.Rows, cells)
	}
	return art
}

func sweep(call *commandCall, metric, subject string) (sweepReply, error) {
	args := map[string]any{"metric": metric, "limit": topLimit}
	if subject != "" {
		args["subject"] = subject
	}
	out, err := call.run(args)
	if err != nil {
		return sweepReply{}, err
	}
	var reply sweepReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return sweepReply{}, fmt.Errorf("the reply was not a ranking: %v", err)
	}
	return reply, nil
}

// friendlyMetric is the metric a player meant by a word the companion did
// not take, among the names its reply listed: two words joined into one
// name, hyphens for underscores, or a singular for a plural. ok is false
// when none of them is a listed metric other than the one already tried.
func friendlyMetric(metric, subject string, names []string) (string, string, bool) {
	listed := map[string]bool{}
	for _, n := range names {
		listed[n] = true
	}
	if listed[metric] {
		return "", "", false // the name was known: the refusal is about something else
	}
	under := strings.ReplaceAll(metric, "-", "_")
	type guess struct{ metric, subject string }
	guesses := []guess{}
	if subject != "" {
		guesses = append(guesses, guess{under + "_" + strings.ReplaceAll(subject, "-", "_"), ""})
	}
	guesses = append(guesses, guess{under, subject}, guess{under + "s", subject}, guess{strings.TrimSuffix(under, "s"), subject})
	for _, g := range guesses {
		if g.metric != metric && listed[g.metric] {
			return g.metric, g.subject, true
		}
	}
	return "", "", false
}

// sweepRefusal is the notice for a sweep the companion refused: its reason,
// then the metrics its reply listed, when it listed any.
func sweepRefusal(r sweepReply) string {
	text := r.Reason
	if text == "" {
		text = "That ranking was refused"
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if names := r.metricNames(); len(names) > 0 {
		text += ". Metrics: " + strings.Join(names, ", ")
	}
	return clipRichText(text+".", MaxCellChars)
}

// metricLabel is the metric as a title says it: its name, the subject it
// measured and the unit, when the reply gave them.
func metricLabel(r sweepReply, asked string) string {
	label := r.Metric
	if label == "" {
		label = asked
	}
	if r.Subject != "" {
		label += " " + r.Subject
	}
	if r.Unit != "" {
		label += " (" + r.Unit + ")"
	}
	return label
}

// cellText is one reply cell as text: a string as written, anything else as
// its JSON.
func cellText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// companionMessage is a tool error as the companion worded it, without the
// protocol's own prefix: the player reads the sentence, not the code.
func companionMessage(err error) string {
	msg := err.Error()
	for _, code := range []string{"provider_error: ", "no_tool: ", "no_provider: "} {
		if i := strings.LastIndex(msg, code); i >= 0 {
			return msg[i+len(code):]
		}
	}
	return strings.TrimPrefix(msg, "aab-rpc: ")
}

// forceCard is the force_card tool's reply; any key may be absent.
type forceCard struct {
	Found            *bool  `json:"found"`
	Force            string `json:"force"`
	PlayersTotal     *num   `json:"players_total"`
	PlayersOnline    *num   `json:"players_online"`
	Researched       *num   `json:"researched"`
	CurrentResearch  string `json:"current_research"`
	ResearchProgress *num   `json:"research_progress"`
	Queue            *num   `json:"queue"`
	Rockets          *num   `json:"rockets"`
	RobotsTotal      *num   `json:"robots_total"`
	Logistic         *num   `json:"logistic_robots"`
	Construction     *num   `json:"construction_robots"`
	EnemiesKilled    *num   `json:"enemies_killed"`
	TopItems         list[struct {
		Item   string `json:"item"`
		PerMin num    `json:"per_min"`
	}] `json:"top_items"`
	Reason string `json:"reason"`
}

// card is one force at a glance: a two-column table of at most a table's
// rows, in a fixed order, a key the reply left out skipped. force is the
// named force, labels already swapped for names, or "" for the asker's own.
func card(call *commandCall, force, own string) Artifact {
	if force == "" {
		force = own
	}
	out, err := call.run(map[string]any{"force": force})
	if err != nil {
		return refusal("No card for " + force + ": " + companionMessage(err))
	}
	var c forceCard
	if err := json.Unmarshal(out, &c); err != nil {
		return refusal("No card for " + force + ": the reply was not a card.")
	}
	if c.Found != nil && !*c.Found {
		why := c.Reason
		if why == "" {
			why = "no such force"
		}
		return refusal("No card for " + force + ": " + why)
	}
	if c.Force == "" {
		c.Force = force
	}
	var rows [][]string
	add := func(what, value string) {
		if value != "" && len(rows) < MaxTableRows {
			rows = append(rows, []string{what, value})
		}
	}
	if c.PlayersTotal != nil {
		online := "0"
		if c.PlayersOnline != nil {
			online = string(*c.PlayersOnline)
		}
		add("players", online+" online of "+string(*c.PlayersTotal))
	}
	add("research", researchText(c))
	if c.Researched != nil {
		add("technologies researched", string(*c.Researched))
	}
	if c.Rockets != nil {
		add("rockets", string(*c.Rockets))
	}
	add("robots", robotsText(c))
	if c.EnemiesKilled != nil {
		add("enemies killed", string(*c.EnemiesKilled))
	}
	var items []string
	for _, it := range c.TopItems {
		if it.Item != "" {
			items = append(items, ItemSprite(it.Item)+" "+string(it.PerMin)+"/min")
		}
	}
	add("top items", clipRichText(strings.Join(items, ", "), MaxCellChars))
	if len(rows) == 0 {
		return refusal("The card for " + c.Force + " came back empty.")
	}
	return Artifact{Shape: ShapeTable, Title: c.Force, Columns: []string{"what", "value"}, Rows: rows}
}

// researchText is what the force is researching and how far along, "idle"
// when nothing is, or "" when the reply said nothing about research.
func researchText(c forceCard) string {
	if c.CurrentResearch == "" {
		if c.ResearchProgress != nil || c.Queue != nil || c.Researched != nil {
			return "idle"
		}
		return ""
	}
	text := TechnologySprite(c.CurrentResearch)
	if c.ResearchProgress != nil {
		if f, ok := c.ResearchProgress.float(); ok {
			text += fmt.Sprintf(" %.0f%%", f*100)
		}
	}
	if c.Queue != nil {
		if n, ok := c.Queue.float(); ok && n > 1 {
			text += fmt.Sprintf(", %.0f queued", n)
		}
	}
	return text
}

func robotsText(c forceCard) string {
	if c.RobotsTotal == nil {
		return ""
	}
	text := string(*c.RobotsTotal)
	if c.Logistic != nil && c.Construction != nil {
		text += fmt.Sprintf(" (%s logistic, %s construction)", *c.Logistic, *c.Construction)
	}
	return text
}

// ItemSprite is an item as the system prompt has the model write one: its
// sprite tag and nothing else.
func ItemSprite(name string) string { return "[img=item." + name + "]" }

// TechnologySprite is a technology's sprite tag, the same convention.
func TechnologySprite(name string) string { return "[img=technology." + name + "]" }

// TechnologyLink is a technology's clickable tag, which the prompt keeps for
// the thing itself: a player can open it from the line.
func TechnologyLink(name string) string { return "[technology=" + name + "]" }
