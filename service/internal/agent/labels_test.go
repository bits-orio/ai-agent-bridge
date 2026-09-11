package agent

import (
	"strings"
	"testing"
)

func TestSubstituteLabels(t *testing.T) {
	labels := []ForceLabel{
		{Name: "team-1", Label: "Team Ace"},
		{Name: "team-2", Label: "Team losers"},
		{Name: "team-10", Label: "Team Ace Two"},
		{Name: "player", Label: "The Engineers"},
		{Name: "team-3", Label: "team-3"},
		{Name: "team-4", Label: "Team 04"},
		{Name: "team-14", Label: "Team 14"},
	}
	for in, want := range map[string]string{
		"how is Team Ace doing":                "how is team-1 doing",
		"compare team ace with TEAM LOSERS":    "compare team-1 with team-2",
		"is Ace ahead of losers?":              "is team-1 ahead of team-2?",
		"Team Ace Two vs Team Ace":             "team-10 vs team-1",
		"who are the players of The Engineers": "who are the players of The Engineers",
		"space ace and acer are not teams":     "space team-1 and acer are not teams",
		"nothing here":                         "nothing here",
		// An unnamed team\'s label is its force name; a player types it with a space.
		"how is team 3 doing": "how is team-3 doing",
		// MTS names unnamed slots "Team 04": the zero and the hyphen are optional.
		"how much iron has team 4 produced":      "how much iron has team-4 produced",
		"Team 04 vs team-4 vs team 4 vs TEAM 04": "team-4 vs team-4 vs team-4 vs team-4",
		"team 14 is not team 4":                  "team-14 is not team-4",
	} {
		if got := substituteLabels(in, labels); got != want {
			t.Errorf("substituteLabels(%q) = %q, want %q", in, got, want)
		}
	}
	if got := substituteLabels("Team Ace", nil); got != "Team Ace" {
		t.Errorf("no labels must change nothing: %q", got)
	}
}

// The system prompt no longer carries labels, and does carry the clickable
// and where rules.
func TestSystemPromptRules(t *testing.T) {
	got := systemPrompt(Question{Text: "x", Force: "team-1", Labels: []ForceLabel{{Name: "team-1", Label: "Team Ace"}}})
	if strings.Contains(got, "Team Ace") {
		t.Errorf("labels leaked into the prompt:\n%s", got)
	}
	if with := systemPrompt(Question{Text: "x", Force: "team-1", Surface: "mts-nauvis-1"}); !strings.Contains(with, `standing on surface "mts-nauvis-1"`) {
		t.Errorf("the asker's surface is missing:\n%s", with)
	}
	if strings.Contains(got, "standing on surface") {
		t.Errorf("a question without a surface must not claim one:\n%s", got)
	}
	for _, want := range []string{"[img=item.iron-ore]/min, never Iron ore/min", "[recipe=repair-pack]", "[gps=x,y,surface]", "ask which surface in a notice", "compare forces by that, never by game time", "Never answer a total with a rate", "divided by its own online hours"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}
