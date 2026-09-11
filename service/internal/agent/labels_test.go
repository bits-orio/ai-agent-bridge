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
	}
	for in, want := range map[string]string{
		"how is Team Ace doing":                "how is team-1 doing",
		"compare team ace with TEAM LOSERS":    "compare team-1 with team-2",
		"is Ace ahead of losers?":              "is team-1 ahead of team-2?",
		"Team Ace Two vs Team Ace":             "team-10 vs team-1",
		"who are the players of The Engineers": "who are the players of The Engineers",
		"space ace and acer are not teams":     "space team-1 and acer are not teams",
		"nothing here":                         "nothing here",
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
	for _, want := range []string{"[img=item.iron-ore]/min, never Iron ore/min", "[recipe=repair-pack]", "[gps=x,y,surface]", "ask which surface in a notice"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}
