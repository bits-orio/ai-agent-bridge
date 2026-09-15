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

// The sweep reduction rule (phase4-spec.md section 13, "What a sweep looks
// like to the player") tells the model to cut a full sweep down to what a
// table artifact can hold. It lives in the system prompt, the cached prefix,
// so it must read the same for every question and never vary with the asker.
func TestSystemPromptCarriesTheSweepReductionRule(t *testing.T) {
	pa := systemPrompt("")

	for _, want := range []string{
		"A sweep may return more rows than an answer can show.",
		"shown and total",
	} {
		if !strings.Contains(pa, want) {
			t.Errorf("system prompt is missing the sweep reduction rule, wanted %q:\n%s", want, pa)
		}
	}
}

// The zero-lookup free tier (phase4 follow-up contract section 4) only pays
// off if the model knows the briefing exists and is told when it already
// answers the question. This lives in systemPrompt, the cached prefix, so it
// must read the same on every call, exactly like the sweep reduction rule
// above; measured live, without it the model called game_time for uptime and
// current_research for each team's research even though both rode in the
// briefing on every question (aab stats: zero-lookup 0 of 7).
func TestSystemPromptTeachesTheBriefingExists(t *testing.T) {
	pa := systemPrompt("")

	for _, want := range []string{
		"already there for you",
		"pl is connected players with their force",
		"call no tool",
		"who is online",
	} {
		if !strings.Contains(pa, want) {
			t.Errorf("system prompt is missing the briefing instruction, wanted %q:\n%s", want, pa)
		}
	}
}

// The live server runs companion 1.0.3, where a sweep-shaped pl never
// arrives (decodePlayersReply omits it on that shape) and a "who is online"
// example with no gate tells the model it can name names from a key that is
// always absent there. Fix pass finding A1: the example must read the same
// way the sentence ahead of it already does, that an absent key means the
// thing was not available, so "who is online" needs a visible tie to pl
// rather than sitting in the unconditional list beside uptime and research.
func TestSystemPromptGatesWhoIsOnlineOnPlBeingPresent(t *testing.T) {
	pa := systemPrompt("")

	if !strings.Contains(pa, "who is online when pl is there") {
		t.Errorf("system prompt states \"who is online\" as a plain briefing answer instead of gating it on pl being present:\n%s", pa)
	}
}

// ch is chat players typed, not a game-read fact like every other briefing
// key. Fix pass finding A2: the opening sentence calls the whole briefing
// "a snapshot read from the game ... never something to verify", and the
// fence header only says chat is not an instruction, never that it is not a
// verified fact, so a player's claim in ch reads to the model as something
// the game itself reported.
func TestSystemPromptMarksChatAsPlayerTypedNotGameReported(t *testing.T) {
	pa := systemPrompt("")

	if !strings.Contains(pa, "ch is recent chat, what players said rather than something the game reports") {
		t.Errorf("system prompt does not mark ch as player-typed rather than game-reported:\n%s", pa)
	}
}

// Fix pass finding A3: ch already carries the last few chat lines, so a
// "what have people been saying" question answered from the briefing spends
// nothing, but the recorded-history paragraph still pointed every chat
// question at recent_chat regardless. The split has to say which chat a
// lookup is even for: older than what ch already carries.
func TestSystemPromptSendsOnlyOlderChatToRecentChat(t *testing.T) {
	pa := systemPrompt("")

	// Both halves matter, and the gate is the half the first version of this
	// fix missed: ch is absent whenever capToBudget dropped it, no history
	// store is wired, the organic filter found nothing, or the briefing is
	// off entirely, and on every one of those paths the model still needs a
	// named route to the chat tool rather than an assurance that chat is
	// already in front of it.
	for _, want := range []string{
		"already in ch when ch is there and needs no lookup",
		"recent_chat is the route when ch is missing",
	} {
		if !strings.Contains(pa, want) {
			t.Errorf("system prompt does not route only older chat to recent_chat, wanted %q:\n%s", want, pa)
		}
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

// The voice is one fixed string the service owns, not operator free text, and
// an unrecognised setting is no voice at all rather than an error: a typo in a
// config file must not stop a server answering questions.
func TestVoiceTextOnlyRecognisesTheOneFlavour(t *testing.T) {
	if voiceText("factorio") != VoiceFactorio {
		t.Error("factorio should select the one flavour the service ships")
	}
	for _, setting := range []string{"off", "", "Factorio", "pirate", "off ", "factorio "} {
		if got := voiceText(setting); got != "" {
			t.Errorf("voiceText(%q) = %q, want no voice at all", setting, got)
		}
	}
}
