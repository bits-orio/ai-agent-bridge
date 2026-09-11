package agent

import (
	"strings"
	"testing"
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
