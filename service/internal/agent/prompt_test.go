package agent

import (
	"strings"
	"testing"
)

// The fence phase4-spec.md section 3 ("Fencing") gives is a security
// boundary, not decoration: Assemble's own text must ride into the user
// turn unchanged, not re-escaped or re-wrapped.
func TestPromptEmbedsTheBriefingFenceVerbatim(t *testing.T) {
	q := Question{Text: "what's my team researching", Force: "team-1", Surface: "nauvis"}
	briefing := fenceHeader + `{"t":184320,"h":51.2,"me":{"n":"Xx_Steve_xX","f":"team-1","s":"nauvis"},"ses":{"name":"","fresh":true}}` + fenceFooter

	turn := prompt(q, nil, briefing)

	if !strings.Contains(turn, briefing) {
		t.Errorf("briefing fence not reproduced verbatim in the user turn:\n%s\n---\nwant substring:\n%s", turn, briefing)
	}
}

// Placement per phase4-spec.md section 3: ahead of Question:, after any
// earlier session exchanges and after the existing asker-context line. The
// transcript only grows at its end, so a follow-up shares its cached prefix
// with the exchange before it; the briefing is per-question and sits after
// it for the same reason.
func TestPromptPlacesTheBriefingAfterTheAskerLineAndBeforeTheQuestion(t *testing.T) {
	q := Question{Text: "status check", Force: "team-1", Surface: "nauvis"}
	briefing := fenceHeader + `{"t":1,"h":1,"me":{"n":"a","f":"team-1","s":"nauvis"},"ses":{"name":"","fresh":true}}` + fenceFooter
	earlier := []Exchange{{Asker: "bob", Question: "q", Answer: "a"}}

	turn := prompt(q, earlier, briefing)

	positions := map[string]int{
		"earlier exchange": strings.Index(turn, "bob asked"),
		"asker line":       strings.Index(turn, `standing on surface "nauvis"`),
		"briefing":         strings.Index(turn, "--- briefing ---"),
		"question":         strings.Index(turn, "Question: status check"),
	}
	for name, idx := range positions {
		if idx < 0 {
			t.Fatalf("user turn is missing %s:\n%s", name, turn)
		}
	}
	if !(positions["earlier exchange"] < positions["asker line"] &&
		positions["asker line"] < positions["briefing"] &&
		positions["briefing"] < positions["question"]) {
		t.Errorf("user turn order must be transcript, asker line, briefing, question; got:\n%s", turn)
	}
	if !strings.HasSuffix(turn, "Question: status check") {
		t.Errorf("the question line must be last in the user turn:\n%s", turn)
	}
}

// An absent briefing renders nothing at all: no empty fence, no placeholder
// (task instruction 3). Leaving the argument off and passing "" must render
// identically, and neither may leave any fence marker behind.
func TestPromptWithNoBriefingRendersNothing(t *testing.T) {
	q := Question{Text: "x", Force: "team-1", Surface: "nauvis"}

	omitted := prompt(q, nil)
	empty := prompt(q, nil, "")

	if omitted != empty {
		t.Errorf("an empty briefing string must render the same as omitting the argument:\n%s\n---\n%s", omitted, empty)
	}
	for _, marker := range []string{"--- briefing ---", "--- end briefing ---", "Server briefing."} {
		if strings.Contains(omitted, marker) {
			t.Errorf("no briefing was given, but the user turn carries fence marker %q:\n%s", marker, omitted)
		}
	}
}
