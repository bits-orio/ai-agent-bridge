// The question grammar (docs/design/phase3-spec.md, "Grammar"):
//
//	[new] [#name] question    ask, continuing or starting the session
//	new [#name]               start a fresh session, no question
//	sessions                  list the sessions in the asker's scope
//	help | ? | commands       what can be typed (commands.go)
//	top <metric> [subject]    a sweep, ranked, with no model round
//	card [force]              one force's card, with no model round
//
// Parsed here, in the service, so the companion sends every line verbatim
// and the grammar can grow without a mod release.

package agent

import (
	"regexp"
	"strings"
)

// Request is one parsed question line.
type Request struct {
	Fresh    bool   // "new" was given
	Name     string // "#name" without the hash, "" for the default session
	Question string // what is left for the model; "" for a command
	Command  string // "", or one of the Command* values
	Arg      string // what follows top or card, spaces squeezed; top's lowercased
}

const (
	CommandSessions = "sessions"
	CommandNew      = "new"
	CommandEmpty    = "empty" // nothing to ask and nothing to do
	CommandHelp     = "help"
	CommandTop      = "top"
	CommandCard     = "card"
)

// The free commands' forms. Each must be the whole line, so a question that
// merely starts with the same word ("top 5 reasons my base is slow", "help me
// find iron") still reaches the model: top takes a metric that starts with a
// letter and at most one more word, card at most three words of a force name
// or label, and neither takes punctuation a question would carry.
var (
	topForm  = regexp.MustCompile(`^top\s+([a-z][a-z0-9_.-]*(?:\s+[a-z0-9][a-z0-9_.-]*)?)$`)
	cardForm = regexp.MustCompile(`^card(?:\s+([^\s?!,;:]+(?:\s+[^\s?!,;:]+){0,2}))?$`)
)

var sessionName = regexp.MustCompile(`^#([a-z0-9_-]{1,16})$`)

// parse reads the leading control words off a question. Words are taken
// in any order until one is neither "new" nor a valid "#name"; the rest of
// the line, spaces included, is the question.
func parse(text string) Request {
	var req Request
	rest := strings.TrimSpace(text)
	for rest != "" {
		word, tail := splitWord(rest)
		lower := strings.ToLower(word)
		switch {
		case lower == "new" && !req.Fresh:
			req.Fresh = true
		case req.Name == "" && sessionName.MatchString(lower):
			req.Name = sessionName.FindStringSubmatch(lower)[1]
		default:
			req.Question = rest
			return finish(req)
		}
		rest = strings.TrimSpace(tail)
	}
	return finish(req)
}

func finish(req Request) Request {
	switch {
	case req.Question == "" && req.Fresh:
		req.Command = CommandNew
	case req.Question == "":
		req.Command = CommandEmpty
	case strings.EqualFold(req.Question, "sessions") && !req.Fresh:
		req.Command = CommandSessions
		req.Question = ""
	case !req.Fresh:
		freeCommand(&req)
	}
	return req
}

// freeCommand turns a question that is exactly one of the free commands'
// forms into that command. A line after "new" is left a question, as "new
// sessions" is: "new" asks for a fresh session to ask it in.
func freeCommand(req *Request) {
	text := strings.Join(strings.Fields(req.Question), " ")
	lower := strings.ToLower(text)
	switch {
	case lower == "help" || lower == "?" || lower == "commands":
		req.Command = CommandHelp
	case topForm.MatchString(lower):
		req.Command, req.Arg = CommandTop, topForm.FindStringSubmatch(lower)[1]
	case cardForm.MatchString(lower):
		req.Command = CommandCard
		if len(text) > len("card") {
			req.Arg = strings.TrimSpace(text[len("card"):]) // the label keeps its case
		}
	default:
		return
	}
	req.Question = ""
}

func splitWord(s string) (word, tail string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}
