// The whole path a polled question takes, from its poll row to every history
// read the briefing and the model make, keeps a private channel's rows to the
// question asked in that channel (internal/chatscope, internal/history).

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// secretLines is team-4's and team-9's private chat beside public chat, as
// events.jsonl carries it: the scoped shape and the legacy one, a private
// chat-prefix question with its echo and answer, and two lines a provider
// gave no usable key.
var secretLines = []string{
	`{"event":"player_joined","tick":900,"data":{"player":"Bob","force":"team-4"}}`,
	`{"event":"console_chat","tick":1000,"data":{"player":"Alice","force":"team-4","message":"public hello","scope":"global","private":false}}`,
	`{"event":"console_chat","tick":1001,"data":{"player":"Bob","force":"team-4","message":"SECRET-T4 chat","scope":"team-4","private":true}}`,
	`{"event":"console_chat","tick":1002,"data":{"player":"Bob","force":"team-4","message":"SECRET-T4 legacy"}}`,
	`{"event":"console_chat","tick":1003,"data":{"player":"Bob","force":"team-4","message":"?SECRET-T4 question","scope":"team-4","private":true}}`,
	`{"event":"question","tick":1003,"data":{"qid":11,"player":"Bob","force":"team-4","text":"SECRET-T4 question","scope":"team-4","private":true}}`,
	`{"event":"answer","tick":1004,"data":{"qid":11,"shape":"summary"}}`,
	`{"event":"console_chat","tick":1005,"data":{"player":"Cid","force":"team-9","message":"SECRET-T9 chat","scope":"team-9","private":true}}`,
	`{"event":"console_chat","tick":1006,"data":{"player":"Dee","force":"team-5","message":"SECRET-KEYLESS line","scope":"global","private":true}}`,
	`{"event":"console_chat","tick":1007,"data":{"player":"Server","message":"server says hi"}}`,
	`{"event":"player_left","tick":1008,"data":{"player":"Bob","force":"team-4"}}`,
}

func secretStore(t *testing.T) *history.Store {
	t.Helper()
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, line := range secretLines {
		if err := store.Ingest([]byte(line)); err != nil {
			t.Fatalf("ingest %q: %v", line, err)
		}
	}
	return store
}

// readsEverything is a model that, in its first round, calls every history
// tool the store serves with every argument shape below, and submits in its
// second. It keeps everything it was handed: the user turn with the briefing
// and every tool result.
type readsEverything struct {
	history []tools.Tool
	round   int
	seen    strings.Builder
}

var everyArgs = []map[string]any{
	{},
	{"limit": 20},
	{"event": "console_chat", "limit": 20},
	{"event": "console_chat", "force": "team-9", "limit": 20},
	{"event": "console_chat", "force": "team-5", "limit": 20},
	{"event": "question", "limit": 20},
	{"event": "answer"},
	{"event": "console_chat", "player": "Bob"},
	{"player": "Bob"},
	{"since_tick": 1, "limit": 20},
}

func (m *readsEverything) Name() string { return "reads-everything" }

func (m *readsEverything) Step(_ context.Context, _ string, msgs []model.Message, _ []model.ToolDef) (model.Step, error) {
	m.round++
	for _, msg := range msgs {
		for _, b := range msg.Blocks {
			fmt.Fprintf(&m.seen, "%s%s\n", b.Text, b.Content)
		}
	}
	usage := model.Usage{InputTokens: 10, OutputTokens: 2}
	if m.round > 1 {
		raw, _ := json.Marshal(map[string]any{"shape": "summary", "lines": []string{"done"}})
		return model.Step{Blocks: []model.Block{{Type: model.BlockToolUse, ID: "submit", Name: agent.SubmitTool, Input: raw}}, StopReason: model.StopToolUse, Usage: usage}, nil
	}
	var calls []model.Block
	for _, tool := range m.history {
		for i, args := range everyArgs {
			raw, _ := json.Marshal(args)
			calls = append(calls, model.Block{Type: model.BlockToolUse, ID: fmt.Sprintf("%s-%d", tool.Name, i), Name: tool.Name, Input: raw})
		}
	}
	return model.Step{Blocks: calls, StopReason: model.StopToolUse, Usage: usage}, nil
}

// gameTime is the one briefing trip the briefing cannot go without.
func gameTime() tools.Tool {
	return tools.Tool{
		Name:   catalog.ToolName("ai-agent-bridge-tools", "game_time"),
		Schema: tools.ObjectSchema(map[string]any{"force": map[string]any{"type": "string"}}, "force"),
		Call: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"tick":1010,"hours":1}`), nil
		},
	}
}

// TestAPolledQuestionReadsOnlyWhatItsAudienceMayHear runs one polled question
// per kind of scope through runner.run, the production path from poll row to
// answer, with the briefing on and the model reading every history tool.
func TestAPolledQuestionReadsOnlyWhatItsAudienceMayHear(t *testing.T) {
	for _, c := range []struct {
		name    string
		q       rpc.Question
		sees    []string
		briefed []string // what the briefing's own chat must carry
		never   []string
	}{
		{"global, asked by a team-4 player", rpc.Question{Force: "team-4", Scope: "global"},
			[]string{"public hello"}, []string{"public hello"}, []string{"SECRET-T4", "SECRET-T9", "SECRET-KEYLESS"}},
		{"from a companion that sends no scope", rpc.Question{Force: "team-4"},
			[]string{"public hello"}, []string{"public hello"}, []string{"SECRET-T4", "SECRET-T9", "SECRET-KEYLESS"}},
		{"private with the global key", rpc.Question{Force: "team-5", Scope: "global", Private: true},
			[]string{"public hello"}, []string{"public hello"}, []string{"SECRET-T4", "SECRET-T9", "SECRET-KEYLESS"}},
		{"team-4's private channel", rpc.Question{Force: "team-4", Scope: "team-4", Private: true},
			[]string{"SECRET-T4 chat", "SECRET-T4 legacy", "SECRET-T4 question"}, []string{"SECRET-T4 chat"}, []string{"SECRET-T9", "SECRET-KEYLESS"}},
		{"team-9's private channel", rpc.Question{Force: "team-9", Scope: "team-9", Private: true},
			[]string{"SECRET-T9 chat"}, []string{"SECRET-T9 chat"}, []string{"SECRET-T4", "SECRET-KEYLESS"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := secretStore(t)
			mdl := &readsEverything{history: store.Tools()}
			a := agent.New(mdl, agent.Caps{MaxRounds: 3, MaxToolCalls: 1000, MaxToolResultBytes: 1 << 20,
				MaxRoundToolResultBytes: 1 << 24, QuestionsPerPlayerPerHour: 100})
			a.BriefingEnabled = true
			a.Chat = newHistoryChat(store)
			client := rpc.New(newFakeCompanion(nil))
			r := &runner{cfg: &config.Config{}, rpc: client, caller: &toolCaller{client: client}, store: store,
				stats: controlapi.NewStats("reads-everything", time.Now()), agent: a, inFlight: map[int64]*delivery{}}
			captureLog(t)

			q := c.q
			q.ID, q.Text, q.PlayerIndex, q.PlayerName = 1, "what has been said", new(int), "Asker"
			game := append([]tools.Tool{gameTime()}, store.Tools()...)
			if res := r.run(context.Background(), q, game); res.Rounds != 2 {
				t.Fatalf("the question ran %d rounds, want 2 (read everything, then submit): %+v", res.Rounds, res.Artifact)
			}

			seen := mdl.seen.String()
			for _, m := range c.never {
				if strings.Contains(seen, m) {
					t.Errorf("the model was handed %q:\n%s", m, seen)
				}
			}
			for _, m := range c.sees {
				if !strings.Contains(seen, m) {
					t.Errorf("the model never saw %q:\n%s", m, seen)
				}
			}
			briefing := briefingOf(seen)
			for _, m := range c.briefed {
				if !strings.Contains(briefing, m) {
					t.Errorf("the briefing's chat is missing %q:\n%s", m, briefing)
				}
			}
		})
	}
}

// briefingOf cuts the fenced briefing out of what the model saw.
func briefingOf(seen string) string {
	start := strings.Index(seen, "--- briefing ---")
	end := strings.Index(seen, "--- end briefing ---")
	if start < 0 || end < start {
		return ""
	}
	return seen[start:end]
}
