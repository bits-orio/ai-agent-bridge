// The voice an answer is written in: the last part of the system prompt.
//
// The first design shipped one fixed voice and refused free text, because
// free text drifts and can be worded to change what an answer says
// (docs/design/phase4-spec.md section 16). The owner now wants an easy way to
// change the voice, so free text is accepted, and both concerns are answered
// here rather than by refusing it. The text is sanitised to one short line
// with no rich-text tags, and it is always followed by the same guard
// sentence the preset ends with, so a voice can change how an answer sounds
// but never a number, a unit, an item name or a table cell.
//
// The setting travels with each question: the companion stamps the map's
// aab-personality setting onto every question it hands over, and one that
// says anything wins over the operator's agent.personality. It is still one
// value for the whole server at any moment, and it sits last in the system
// prompt: every question in the same voice reads the whole prompt from the
// cache, and a change of voice costs one cache write, not one per question.

package agent

import (
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
)

// VoiceOff turns the voice off. It is the setting's default and the label the
// ledger records when no voice is in use.
const VoiceOff = ledger.VoiceOff

// VoiceCustom is the label the ledger records for a free-text voice.
const VoiceCustom = ledger.VoiceCustom

// VoiceGuard ends every voice, preset and free text alike.
const VoiceGuard = "Flavour never changes a number, a unit, an item name or a table cell."

// VoiceFactorio is the preset quoted from docs/design/phase4-spec.md section
// 16 word for word. Its last sentence is the guard.
const VoiceFactorio = "Speak like an engineer on the factory floor: plain, dry, and fond of the " +
	"machines. Factorio's own words are welcome where they fit, even when the question " +
	"is not about Factorio. Keep it to the summary line. " + VoiceGuard

// voicePresets are the voices the service owns, by the name a setting uses.
var voicePresets = map[string]string{
	"factorio": VoiceFactorio,
}

// offWords turn the voice off, in any case, beside VoiceOff itself: what an
// operator or an admin types for "no". YAML reads an unquoted false or no as a
// boolean, and a voice called "false" is never what anyone meant.
var offWords = map[string]bool{
	VoiceOff: true, "false": true, "no": true, "none": true, "disabled": true, "0": true, "n": true,
}

// maxVoiceRunes bounds a free-text voice. The companion stops a setting at 500
// bytes, never more than 500 runes, so a value it sends is never cut here;
// this bound is for any other client, and for the config.
const maxVoiceRunes = 500

// voice is what one question is answered in: the text that ends the system
// prompt, "" for none, and the label the ledger records.
type voice struct {
	text  string
	label string
}

// voiceFor is the voice q is answered in.
func (a *Agent) voiceFor(q Question) voice {
	v := resolveVoice(q.Personality, a.Personality)
	a.voiceLog.note(strings.TrimSpace(q.Personality) != "", v.label, a.Personality, a.Trace)
	return v
}

// VoiceLabel is the label of the voice a setting resolves to with nothing set
// in game: off, a preset's name, or custom. The service logs it at startup.
func VoiceLabel(configured string) string {
	return resolveVoice("", configured).label
}

// voiceLog says in the log when the map's own setting starts or stops
// deciding the voice, once per change rather than once per question.
type voiceLog struct {
	mu         sync.Mutex
	known      bool
	overriding bool
	label      string
}

// note records the voice one question resolved to. The state before the
// first question is the config's voice, which the service logged at startup.
func (l *voiceLog) note(overriding bool, label, configured string, trace func(string, ...any)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.known {
		l.known, l.label = true, VoiceLabel(configured)
	}
	if overriding == l.overriding && label == l.label {
		return
	}
	l.overriding, l.label = overriding, label
	if trace == nil {
		return
	}
	if overriding {
		trace("voice: %s, from the map's personality setting, which overrides agent.personality", label)
		return
	}
	trace("voice: %s, from agent.personality: the map's personality setting is blank", label)
}

// resolveVoice picks the voice for one question from its in-game setting and
// the operator's config. A setting that says anything wins over the config.
// off and its synonyms (offWords) are no voice and a preset name is that
// preset, either in any case. Anything else is free text, sanitised and
// followed by the guard. Free text that sanitises to nothing is no voice,
// never the guard on its own.
func resolveVoice(inGame, configured string) voice {
	setting := strings.TrimSpace(inGame)
	if setting == "" {
		setting = strings.TrimSpace(configured)
	}
	name := strings.ToLower(setting)
	if name == "" || offWords[name] {
		return voice{label: VoiceOff}
	}
	if text, ok := voicePresets[name]; ok {
		return voice{text: text, label: name}
	}
	free := sanitiseVoice(setting)
	if free == "" {
		return voice{label: VoiceOff}
	}
	return voice{text: "Voice: " + endSentence(free) + " " + VoiceGuard, label: VoiceCustom}
}

// richTag is one rich-text tag, [color=red], [/color] or [item=iron-plate]: a
// bracket, anything but a bracket, a bracket.
var richTag = regexp.MustCompile(`\[[^\[\]]*\]`)

// sanitiseVoice makes free text fit to sit in the system prompt: every
// rich-text tag gone with any bracket left over, every control character
// gone, one line, and at most maxVoiceRunes, cut between words.
func sanitiseVoice(s string) string {
	s = richTag.ReplaceAllString(s, " ")
	s = strings.Map(func(r rune) rune {
		if r == '[' || r == ']' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(wordCut([]rune(flat(s)), maxVoiceRunes, maxVoiceRunes/2))
}

// endSentence makes sure the free text ends like a sentence, so the guard
// after it reads as one of its own.
func endSentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}
