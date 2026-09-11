// The question grammar (docs/design/phase3-spec.md, "Grammar"):
//
//	[new] [#name] question    ask, continuing or starting the session
//	new [#name]               start a fresh session, no question
//	sessions                  list the sessions in the asker's scope
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
	Command  string // "", CommandSessions, CommandNew or CommandEmpty
}

const (
	CommandSessions = "sessions"
	CommandNew      = "new"
	CommandEmpty    = "empty" // nothing to ask and nothing to do
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
	}
	return req
}

func splitWord(s string) (word, tail string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}
