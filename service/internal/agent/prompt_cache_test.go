package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// The system prompt is the model's cached prefix, so it must not change with
// the asker: two questions from different players on different surfaces get
// the same system text, and the user turn carries what differs, after the
// transcript and before the question.
func TestSystemPromptIsTheSameForEveryAsker(t *testing.T) {
	a := Question{Text: "x", PlayerName: "alice", PlayerIndex: player(3), Force: "team-1", Surface: "nauvis"}
	b := Question{Text: "y", Force: "spectator", Surface: "mts-nauvis-2", PhysicalSurface: "nauvis"}
	if systemPrompt(a) != systemPrompt(b) {
		t.Errorf("the system prompt varies with the asker:\n%s\n---\n%s", systemPrompt(a), systemPrompt(b))
	}

	turn := prompt(a, []Exchange{{Asker: "bob", Question: "q", Answer: "a"}})
	for _, want := range []string{"bob asked: q", "alice (player 3, force team-1)", `standing on surface "nauvis"`} {
		if !strings.Contains(turn, want) {
			t.Errorf("user turn lacks %q:\n%s", want, turn)
		}
	}
	if !strings.HasSuffix(turn, "Question: x") || strings.Index(turn, "bob asked") > strings.Index(turn, "alice (player 3") {
		t.Errorf("user turn order is transcript, asker, question; got:\n%s", turn)
	}
}

// alternateGroupATools is a second, deliberately different Group A reply
// set from happyGroupATools (briefing_test.go): different forces, research,
// surface and game_time, so the briefing text it produces cannot coincide
// with the other set's by accident.
func alternateGroupATools() []tools.Tool {
	return []tools.Tool{
		engineTool("list_forces", `{"total":2,"empty":0,"shown":2,"forces":[
			{"name":"red","player_count":9,"connected_player_count":4},
			{"name":"blue","player_count":2,"connected_player_count":0}
		]}`, nil),
		engineTool("current_research", `{"total":2,"shown":2,"forces":[
			{"force":"red","researching":true,"tech":"rocket-silo","level":1,"progress":0.1},
			{"force":"blue","researching":false}
		]}`, nil),
		engineTool("list_surfaces", `{"force":"red","total":1,"shown":1,"surfaces":[
			{"name":"vulcanus","index":2,"planet":"vulcanus","force_players":4}
		]}`, nil),
		engineTool("list_players", `{"force":"red","connected_only":true,"known":9,"total":4,"shown":4,"players":[]}`, nil),
		engineTool("game_time", `{"force":"red","tick":900000,"ticks_played":900000,"hours":250,"connected_players":6,"force_connected_players":4}`, nil),
	}
}

// TestAnswerKeepsTheSystemPromptCacheStableAcrossBriefingAndAskers is the
// through-Answer regression the old TestSystemPromptIsTheSameForEveryAsker
// could not catch: that test's own before/after comparison called
// systemPrompt directly, which never sees a briefing at all, so a line in
// Answer folding per-question text (the briefing included) into the system
// string would still leave that comparison green. This drives the real
// path, model.Model included, and reads back what the scripted model
// actually received (agent_test.go's scriptedModel), the same asker-line
// regression of 2026-09-12 (phase4-spec.md section 3) but for the briefing.
func TestAnswerKeepsTheSystemPromptCacheStableAcrossBriefingAndAskers(t *testing.T) {
	run := func(q Question, briefingOn bool, ts []tools.Tool) *scriptedModel {
		t.Helper()
		m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
		a := New(m, caps())
		a.BriefingEnabled = briefingOn
		if _, err := a.Answer(context.Background(), q, ts); err != nil {
			t.Fatalf("answer: %v", err)
		}
		return m
	}

	alice := run(Question{ID: 2101, Text: "status", PlayerName: "alice", PlayerIndex: player(3), Force: "north", Surface: "nauvis"},
		true, happyGroupATools())
	bob := run(Question{ID: 2102, Text: "a completely different question", PlayerName: "bob", Force: "blue", Surface: "vulcanus"},
		true, alternateGroupATools())
	noBriefing := run(Question{ID: 2103, Text: "yet another question", PlayerName: "carol", Force: "south", Surface: "gleba"},
		false, happyGroupATools())

	if alice.system != bob.system {
		t.Errorf("system prompt differs between two askers with a briefing on:\n%s\n---\n%s", alice.system, bob.system)
	}
	if alice.system != noBriefing.system {
		t.Errorf("system prompt changed once the briefing was disabled:\n%s\n---\n%s", alice.system, noBriefing.system)
	}
	for _, marker := range []string{"--- briefing ---", "--- end briefing ---", "Server briefing."} {
		if strings.Contains(alice.system, marker) {
			t.Errorf("the briefing fence leaked into the system prompt: found %q in\n%s", marker, alice.system)
		}
	}

	for _, tc := range []struct {
		name string
		m    *scriptedModel
	}{{"alice", alice}, {"bob", bob}} {
		turn := tc.m.msgs[0].Blocks[0].Text
		if !strings.Contains(turn, "--- briefing ---") || !strings.Contains(turn, "--- end briefing ---") {
			t.Errorf("%s: briefing fence markers missing from the first user message:\n%s", tc.name, turn)
		}
	}
	if turn := noBriefing.msgs[0].Blocks[0].Text; strings.Contains(turn, "--- briefing ---") {
		t.Errorf("briefing was off but fence markers appeared in the user message:\n%s", turn)
	}
}
