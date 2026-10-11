package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// The resolution table: the question's in-game setting wins whenever it says
// anything, off and a preset name are matched in any case, and anything else
// is free text that always ends with the guard.
func TestResolveVoice(t *testing.T) {
	for _, tc := range []struct {
		name               string
		inGame, configured string
		wantLabel          string
		wantText           string // exact text, or "" for none; ignored when wantFree is set
		wantFree           string // the free text the voice must carry
	}{
		{"nothing set anywhere", "", "", VoiceOff, "", ""},
		{"config off", "", "off", VoiceOff, "", ""},
		{"config preset", "", "factorio", "factorio", VoiceFactorio, ""},
		{"in game preset over config off", "factorio", "off", "factorio", VoiceFactorio, ""},
		{"in game off over config preset", "off", "factorio", VoiceOff, "", ""},
		{"in game free text over config preset", "Talk like a ship's captain", "factorio", VoiceCustom, "", "Talk like a ship's captain."},
		{"config free text", "", "Answer like a tired night-shift engineer!", VoiceCustom, "", "Answer like a tired night-shift engineer!"},
		{"blank in game falls back to config", "   ", "factorio", "factorio", VoiceFactorio, ""},
		{"case does not matter for off", "OFF", "factorio", VoiceOff, "", ""},
		{"case does not matter for a preset", " Factorio ", "off", "factorio", VoiceFactorio, ""},
		{"free text that sanitises to nothing is off", "[color=red][/color]", "factorio", VoiceOff, "", ""},
		{"false in game is off", "false", "factorio", VoiceOff, "", ""},
		{"No in game is off", " No ", "factorio", VoiceOff, "", ""},
		{"NONE in game is off", "NONE", "factorio", VoiceOff, "", ""},
		{"disabled in game is off", "Disabled", "factorio", VoiceOff, "", ""},
		{"0 in game is off", "0", "factorio", VoiceOff, "", ""},
		{"n in game is off", "N", "factorio", VoiceOff, "", ""},
		{"false in the config is off", "", "FALSE", VoiceOff, "", ""},
		{"no in the config is off", "", "no", VoiceOff, "", ""},
		{"a word that only starts like no is free text", "nope", "off", VoiceCustom, "", "nope."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveVoice(tc.inGame, tc.configured)
			if got.label != tc.wantLabel {
				t.Errorf("label = %q, want %q", got.label, tc.wantLabel)
			}
			if tc.wantFree != "" {
				if want := "Voice: " + tc.wantFree + " " + VoiceGuard; got.text != want {
					t.Errorf("text = %q, want %q", got.text, want)
				}
				return
			}
			if got.text != tc.wantText {
				t.Errorf("text = %q, want %q", got.text, tc.wantText)
			}
		})
	}
}

// Free text is one line, carries no rich-text tag or bracket and no control
// character, and is at most 500 runes, cut between words. The guard follows
// it however it was written.
func TestFreeTextVoiceIsSanitised(t *testing.T) {
	got := resolveVoice("Talk like a [color=red]pirate[/color],\nnever [item=iron-plate] [gps=1,2,nauvis] wordy\x00\x1b]", "off")
	if got.label != VoiceCustom {
		t.Fatalf("label = %q, want custom", got.label)
	}
	want := "Voice: Talk like a pirate , never wordy. " + VoiceGuard
	if got.text != want {
		t.Errorf("text = %q\nwant  %q", got.text, want)
	}

	long := strings.Repeat("grumpy ", 100) // 700 runes
	voice := resolveVoice(long, "off").text
	free := strings.TrimSuffix(strings.TrimPrefix(voice, "Voice: "), ". "+VoiceGuard)
	if n := utf8.RuneCountInString(free); n > maxVoiceRunes || n < maxVoiceRunes-len("grumpy ") {
		t.Errorf("free text is %d runes, want it cut to at most %d and not far under", n, maxVoiceRunes)
	}
	if strings.Contains(free, "grump ") || !strings.HasSuffix(free, "grumpy") {
		t.Errorf("free text was cut mid-word: ...%q", free[len(free)-20:])
	}
	if !strings.HasSuffix(voice, VoiceGuard) {
		t.Errorf("a cut free-text voice lost its guard: %q", voice)
	}
}

// The companion holds a setting to 500 bytes, so whatever it sends fits the
// service's 500 runes whole: nothing an admin typed in game is cut here.
func TestAVoiceAtTheCompanionsCapIsNeverCut(t *testing.T) {
	for _, setting := range []string{
		strings.Repeat("a", 497) + " zz",     // 500 bytes, 500 runes
		strings.Repeat("\u00e9t\u00e9 ", 83), // 497 bytes once trimmed, mostly two-byte runes
	} {
		setting = strings.TrimSpace(setting)
		if len(setting) > 500 {
			t.Fatalf("test setting is %d bytes, over the companion's cap: fix the test", len(setting))
		}
		want := "Voice: " + endSentence(setting) + " " + VoiceGuard
		if got := resolveVoice(setting, "off").text; got != want {
			t.Errorf("a %d-byte setting was changed:\n got %q\nwant %q", len(setting), got, want)
		}
	}
}

// The log says which voice is in use when the map's own setting starts or
// stops overriding the config, once per change, not once per question.
func TestTheVoiceLogSaysWhenTheMapsSettingTakesOver(t *testing.T) {
	log := &traceLog{}
	m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
	a := New(m, caps())
	a.Personality = "factorio"
	a.Trace = log.printf
	for i, inGame := range []string{"", "", "Talk like a pirate", "Talk like a pirate", "Speak in rhyme", "", "", "off", "OFF", ""} {
		if _, err := a.Answer(context.Background(), Question{ID: int64(i), Text: "status", PlayerIndex: player(i), Personality: inGame}, nil); err != nil {
			t.Fatalf("answer: %v", err)
		}
	}
	got := log.with("voice: ")
	want := []string{
		"voice: custom, from the map's personality setting, which overrides agent.personality",
		"voice: factorio, from agent.personality: the map's personality setting is blank",
		"voice: off, from the map's personality setting, which overrides agent.personality",
		"voice: factorio, from agent.personality: the map's personality setting is blank",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("voice lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The startup line names the config's voice the same way.
func TestVoiceLabel(t *testing.T) {
	for setting, want := range map[string]string{"": VoiceOff, "off": VoiceOff, "false": VoiceOff, "Factorio": "factorio", "Talk like a pirate": VoiceCustom} {
		if got := VoiceLabel(setting); got != want {
			t.Errorf("VoiceLabel(%q) = %q, want %q", setting, got, want)
		}
	}
}

// Every voice there is ends with the guard sentence, the one line that keeps
// a voice from changing a fact.
func TestEveryVoiceEndsWithTheGuard(t *testing.T) {
	for name, text := range voicePresets {
		if !strings.HasSuffix(text, VoiceGuard) {
			t.Errorf("preset %q does not end with the guard: %q", name, text)
		}
	}
	for _, free := range []string{"x", "Talk like a pirate.", "Speak in rhyme? Always", strings.Repeat("long ", 200), "ends with the guard already: " + VoiceGuard} {
		if v := resolveVoice(free, ""); !strings.HasSuffix(v.text, " "+VoiceGuard) {
			t.Errorf("free text %q gave a voice without the guard at the end: %q", free, v.text)
		}
	}
	if VoiceGuard != "Flavour never changes a number, a unit, an item name or a table cell." {
		t.Errorf("the guard sentence changed: %q", VoiceGuard)
	}
}

// Through Answer: the system prompt the model receives ends with the
// question's own voice, the rules before it are the same text every other
// voice gets, and the ledger records the label.
func TestAnswerSpeaksInTheQuestionsOwnVoice(t *testing.T) {
	run := func(id int64, inGame, configured string) (string, ledger.QuestionRecord) {
		t.Helper()
		dir := t.TempDir()
		m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
		a := New(m, caps())
		a.Personality = configured
		a.Ledger = ledger.Open(dir, true)
		if _, err := a.Answer(context.Background(), Question{ID: id, Text: "status", PlayerIndex: player(1), Personality: inGame}, nil); err != nil {
			t.Fatalf("answer: %v", err)
		}
		a.Ledger.Close()
		return m.system, findQuestion(t, dir, id)
	}

	plain, rec := run(1, "", "off")
	if rec.Voice != VoiceOff || plain != systemPrompt("") {
		t.Fatalf("no voice: voice %q, system prompt carries a voice", rec.Voice)
	}
	preset, rec := run(2, "factorio", "off")
	if rec.Voice != "factorio" || !strings.HasSuffix(preset, VoiceFactorio) {
		t.Errorf("in-game preset: voice %q, system prompt ends %q", rec.Voice, preset[len(preset)-80:])
	}
	custom, rec := run(3, "Talk like a ship's captain", "factorio")
	if rec.Voice != VoiceCustom || !strings.HasSuffix(custom, "Voice: Talk like a ship's captain. "+VoiceGuard) {
		t.Errorf("in-game free text: voice %q, system prompt ends %q", rec.Voice, custom[len(custom)-80:])
	}
	configured, rec := run(4, "", "factorio")
	if rec.Voice != "factorio" || configured != preset {
		t.Errorf("config preset: voice %q, and its prompt differs from the same preset set in game", rec.Voice)
	}
	silenced, rec := run(5, "off", "factorio")
	if rec.Voice != VoiceOff || silenced != plain {
		t.Errorf("in-game off over a config preset: voice %q", rec.Voice)
	}
	for name, voiced := range map[string]string{"preset": preset, "custom": custom} {
		if !strings.HasPrefix(voiced, plain+"\n\n") {
			t.Errorf("%s voice is not the last part of the system prompt: the rules before it changed", name)
		}
	}
}

// Two questions from different askers in the same voice get the same system
// prompt, byte for byte: the voice travels with each question but is one
// value for the server at a time, so the cached prefix holds.
func TestTheSameVoiceKeepsTheSystemPromptCacheStable(t *testing.T) {
	var systems []string
	for _, q := range []Question{
		{ID: 1, Text: "status", PlayerName: "alice", PlayerIndex: player(3), Force: "team-1", Surface: "nauvis", Personality: "Talk like a ship's captain"},
		{ID: 2, Text: "a different question", PlayerName: "bob", Force: "team-2", Surface: "vulcanus", Personality: "Talk like a ship's captain"},
	} {
		m := &scriptedModel{steps: []model.Step{submitStep("t1", map[string]any{"shape": "summary", "lines": []string{"fine"}})}}
		if _, err := New(m, caps()).Answer(context.Background(), q, nil); err != nil {
			t.Fatalf("answer: %v", err)
		}
		systems = append(systems, m.system)
	}
	if systems[0] != systems[1] {
		t.Errorf("same voice, different system prompts:\n%s\n---\n%s", systems[0], systems[1])
	}
}
