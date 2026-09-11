package agent

import (
	"strings"
	"testing"
)

// Labels reach the system prompt as "name is label" pairs with the rule
// that answers use the label; without labels the paragraph is absent.
func TestSystemPromptCarriesForceLabels(t *testing.T) {
	q := Question{Text: "x", Force: "team-1", PlayerName: "Alice", Labels: []ForceLabel{
		{Name: "team-1", Label: "Team Ace"}, {Name: "team-2", Label: "Team losers"},
	}}
	got := systemPrompt(q)
	for _, want := range []string{"team-1 is Team Ace; team-2 is Team losers.", "write the player's name for a force in every answer"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if plain := systemPrompt(Question{Text: "x", Force: "player"}); strings.Contains(plain, "names players use") {
		t.Errorf("a question without labels grew a labels paragraph:\n%s", plain)
	}
	if !strings.Contains(got, "[img=item.iron-ore]/min, never Iron ore/min") {
		t.Errorf("the sprite rule for columns is missing:\n%s", got)
	}
}

// The label list is capped so a server with many forces does not turn the
// prompt into a directory.
func TestSystemPromptCapsLabels(t *testing.T) {
	var labels []ForceLabel
	for i := 0; i < maxLabels+5; i++ {
		labels = append(labels, ForceLabel{Name: "f", Label: "l"})
	}
	got := systemPrompt(Question{Text: "x", Labels: labels})
	if strings.Count(got, "f is l") != maxLabels || !strings.Contains(got, "and more") {
		t.Errorf("label cap not applied: %d entries", strings.Count(got, "f is l"))
	}
}
