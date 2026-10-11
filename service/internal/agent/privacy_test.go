package agent

// A question's scope reaches every read it makes, and a private answer never
// reaches a question someone else will hear (chatscope, internal/history).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// scopeLog records the scope each read was handed, tool and chat source alike.
type scopeLog struct {
	mu   sync.Mutex
	seen map[string][]chatscope.Scope
}

func (l *scopeLog) note(who string, ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]chatscope.Scope{}
	}
	l.seen[who] = append(l.seen[who], chatscope.From(ctx))
}

// tool wraps a tool so every call it gets is recorded under its name.
func (l *scopeLog) tool(t tools.Tool) tools.Tool {
	call := t.Call
	t.Call = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		l.note(t.Name, ctx)
		return call(ctx, args)
	}
	return t
}

// RecentChat makes the log the briefing's chat source as well.
func (l *scopeLog) RecentChat(ctx context.Context, _ int) []ChatLine {
	l.note("briefing chat", ctx)
	return []ChatLine{{Who: "Alice", Msg: "hello"}}
}

// TestEveryReadCarriesTheQuestionScope traces each path from a question to a
// read: the briefing's five trips, its chat and the model's own tool calls
// all see the question's scope, set before the first of them ran.
func TestEveryReadCarriesTheQuestionScope(t *testing.T) {
	for _, c := range []struct {
		name string
		q    Question
		want chatscope.Scope
	}{
		{"a private question", Question{Scope: "team-4", Private: true}, chatscope.Scope{Key: "team-4", Private: true}},
		{"a global question", Question{Scope: "global"}, chatscope.Scope{Key: "global"}},
		{"a question from a companion that sent no scope", Question{}, chatscope.Scope{Key: "global"}},
		{"a public question with a channel's key", Question{Scope: "team-4"}, chatscope.Scope{Key: "team-4"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reads := &scopeLog{}
			var ts []tools.Tool
			for _, tl := range append(happyGroupATools(), stubTool("recent_chat", `"tick\tplayer\tmessage"`, nil)) {
				ts = append(ts, reads.tool(tl))
			}
			m := &scriptedModel{steps: []model.Step{
				toolStep("t1", "recent_chat", map[string]any{}),
				submitStep("t2", map[string]any{"shape": "summary", "lines": []string{"done"}}),
			}}
			a := New(m, caps())
			a.BriefingEnabled = true
			a.Chat = reads

			q := c.q
			q.ID, q.Text, q.PlayerIndex, q.Force = 7, "what did people say", player(1), "team-4"
			if _, err := a.Answer(context.Background(), q, ts); err != nil {
				t.Fatalf("answer: %v", err)
			}
			for _, who := range []string{"briefing chat", "recent_chat", "ai-agent-bridge-tools__game_time"} {
				got := reads.seen[who]
				if len(got) == 0 {
					t.Fatalf("%s was never read: %v", who, reads.seen)
				}
				for _, s := range got {
					if s != c.want {
						t.Errorf("%s ran under scope %+v, want %+v", who, s, c.want)
					}
				}
			}
		})
	}
}

// answering is a model that answers every question with the next line of
// lines, in order, and keeps the user turn each question opened with.
type answering struct {
	lines   []string
	prompts []string
}

func (m *answering) Name() string { return "answering" }

func (m *answering) Step(_ context.Context, _ string, msgs []model.Message, _ []model.ToolDef) (model.Step, error) {
	m.prompts = append(m.prompts, msgs[0].Blocks[0].Text)
	line := m.lines[0]
	m.lines = m.lines[1:]
	return submitStep("s", map[string]any{"shape": "summary", "lines": []string{line}}), nil
}

// TestAPrivateAnswerNeverReachesAPublicSession: a private answer may hold its
// channel's own chat, so no question anyone else hears may read it back as an
// earlier exchange. That holds for a well-behaved provider and for the two
// mistakes a provider can make: a public scope carrying a private channel's
// key, and a private scope with no key of its own.
func TestAPrivateAnswerNeverReachesAPublicSession(t *testing.T) {
	m := &answering{}
	a := New(m, caps())
	ask := func(q Question, answer string) string {
		t.Helper()
		m.lines = append(m.lines, answer)
		if _, err := a.Answer(context.Background(), q, nil); err != nil {
			t.Fatalf("question %d: %v", q.ID, err)
		}
		return m.prompts[len(m.prompts)-1]
	}

	ask(Question{ID: 1, Text: "what did we plan", PlayerIndex: player(1), Force: "team-4", Scope: "team-4", Private: true}, "SECRET-T4 plan")
	for _, q := range []Question{
		{ID: 2, Text: "anything new", PlayerIndex: player(1), Force: "team-4", Scope: "global"},
		{ID: 3, Text: "anything new", PlayerIndex: player(1), Force: "team-4"},
		{ID: 4, Text: "anything new", PlayerIndex: player(1), Force: "team-4", Scope: "team-4"},
		{ID: 5, Text: "anything new", PlayerIndex: player(2), Force: "team-9", Scope: "team-9", Private: true},
	} {
		if prompt := ask(q, "ok"); strings.Contains(prompt, "SECRET-T4") {
			t.Errorf("question %d (scope %q, private %v) read team-4's private answer:\n%s", q.ID, q.Scope, q.Private, prompt)
		}
	}
	if prompt := ask(Question{ID: 6, Text: "and then", PlayerIndex: player(3), Force: "team-4", Scope: "team-4", Private: true}, "ok"); !strings.Contains(prompt, "SECRET-T4 plan") {
		t.Errorf("team-4's own follow-up lost its session:\n%s", prompt)
	}

	// A provider that answered private with the global key, for two askers.
	ask(Question{ID: 7, Text: "our secret", PlayerIndex: player(7), Scope: "global", Private: true}, "SECRET-KEYLESS seven")
	for _, q := range []Question{
		{ID: 8, Text: "anything new", PlayerIndex: player(1), Scope: "global"},
		{ID: 9, Text: "anything new", PlayerIndex: player(8), Scope: "global", Private: true},
	} {
		if prompt := ask(q, "ok"); strings.Contains(prompt, "SECRET-KEYLESS") {
			t.Errorf("question %d (scope %q, private %v) read another asker's keyless private answer:\n%s", q.ID, q.Scope, q.Private, prompt)
		}
	}
	if prompt := ask(Question{ID: 10, Text: "and then", PlayerIndex: player(7), Scope: "global", Private: true}, "ok"); !strings.Contains(prompt, "SECRET-KEYLESS seven") {
		t.Errorf("the keyless asker's own follow-up lost its session:\n%s", prompt)
	}
}

// TestThePublicSessionsListingShowsNoPrivateSession: "sessions" in the open
// lists the open sessions only.
func TestThePublicSessionsListingShowsNoPrivateSession(t *testing.T) {
	m := &answering{lines: []string{"private answer"}}
	a := New(m, caps())
	if _, err := a.Answer(context.Background(), Question{ID: 1, Text: "#plan what next", PlayerIndex: player(1), PlayerName: "Bob", Scope: "team-4", Private: true}, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []Question{
		{ID: 2, Text: "sessions", PlayerIndex: player(2), Scope: "global"},
		{ID: 3, Text: "sessions", PlayerIndex: player(2), Scope: "team-4"},
	} {
		res, _ := a.Answer(context.Background(), q, nil)
		if text := res.Artifact.Plain(); res.Artifact.Shape != ShapeNotice || strings.Contains(text, "plan") || strings.Contains(text, "Bob") {
			t.Errorf("sessions under scope %q listed a private session: %+v", q.Scope, res.Artifact)
		}
	}
	res, _ := a.Answer(context.Background(), Question{ID: 4, Text: "sessions", PlayerIndex: player(3), Scope: "team-4", Private: true}, nil)
	if res.Artifact.Shape != ShapeList || !strings.Contains(res.Artifact.Plain(), "#plan") {
		t.Errorf("team-4's own listing lost its session: %+v", res.Artifact)
	}
}

// TestAKeyHoldingANameNeverSharesAnotherChannelsSession: a provider's keys are
// opaque and may hold "#". The channel keyed "guild" asking "#ops ..." and
// the channel keyed "guild#ops" asking plainly are two channels, and neither
// reads the other's exchanges, private or public, either way round.
func TestAKeyHoldingANameNeverSharesAnotherChannelsSession(t *testing.T) {
	for _, private := range []bool{true, false} {
		m := &answering{}
		a := New(m, caps())
		ask := func(q Question, answer string) string {
			t.Helper()
			q.Private = private
			m.lines = append(m.lines, answer)
			if _, err := a.Answer(context.Background(), q, nil); err != nil {
				t.Fatalf("question %d: %v", q.ID, err)
			}
			return m.prompts[len(m.prompts)-1]
		}
		ask(Question{ID: 1, Text: "#ops what is next", PlayerIndex: player(1), Scope: "guild"}, "SECRET-GUILD-OPS plan")
		if prompt := ask(Question{ID: 2, Text: "what is next", PlayerIndex: player(2), Scope: "guild#ops"}, "SECRET-OTHER plan"); strings.Contains(prompt, "SECRET-GUILD-OPS") {
			t.Errorf("private %v: the channel keyed guild#ops read guild's #ops session:\n%s", private, prompt)
		}
		prompt := ask(Question{ID: 3, Text: "#ops and then", PlayerIndex: player(1), Scope: "guild"}, "ok")
		if strings.Contains(prompt, "SECRET-OTHER") {
			t.Errorf("private %v: guild's #ops session read the channel keyed guild#ops:\n%s", private, prompt)
		}
		if !strings.Contains(prompt, "SECRET-GUILD-OPS") {
			t.Errorf("private %v: guild's own #ops follow-up lost its session:\n%s", private, prompt)
		}
	}
}
