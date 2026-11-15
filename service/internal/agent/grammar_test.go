package agent

import "testing"

func TestParseGrammar(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Request
	}{
		{"what forces are there", Request{Question: "what forces are there"}},
		{"new what forces are there", Request{Fresh: true, Question: "what forces are there"}},
		{"#iron what is our plate rate", Request{Name: "iron", Question: "what is our plate rate"}},
		{"new #iron rate?", Request{Fresh: true, Name: "iron", Question: "rate?"}},
		{"#Iron NEW rate?", Request{Fresh: true, Name: "iron", Question: "rate?"}},
		{"new", Request{Fresh: true, Command: CommandNew}},
		{"new #oil", Request{Fresh: true, Name: "oil", Command: CommandNew}},
		{"sessions", Request{Command: CommandSessions}},
		{"#iron sessions", Request{Name: "iron", Command: CommandSessions}},
		{"  ", Request{Command: CommandEmpty}},
		{"#iron", Request{Name: "iron", Command: CommandEmpty}},
		// A second "new" or "#name" is part of the question, as is a name
		// with characters the pattern refuses.
		{"new new things", Request{Fresh: true, Question: "new things"}},
		{"#a #b question", Request{Name: "a", Question: "#b question"}},
		{"#Not-Valid! question", Request{Question: "#Not-Valid! question"}},
		{"#toolongtoolongtoolong q", Request{Question: "#toolongtoolongtoolong q"}},
		// "new sessions" asks about sessions, it does not list them.
		{"new sessions", Request{Fresh: true, Question: "sessions"}},
	} {
		if got := parse(tc.in); got != tc.want {
			t.Errorf("parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// The free commands match the whole question in exactly their own form, any
// case, after any #name. Anything else that only starts with the same word
// is a question for the model.
func TestParseFreeCommands(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Request
	}{
		{"help", Request{Command: CommandHelp}},
		{"HELP", Request{Command: CommandHelp}},
		{"?", Request{Command: CommandHelp}},
		{"commands", Request{Command: CommandHelp}},
		{"#ops help", Request{Name: "ops", Command: CommandHelp}},
		{"top rockets", Request{Command: CommandTop, Arg: "rockets"}},
		{"Top Robots", Request{Command: CommandTop, Arg: "robots"}},
		{"top item_made iron-plate", Request{Command: CommandTop, Arg: "item_made iron-plate"}},
		{"top logistic robots", Request{Command: CommandTop, Arg: "logistic robots"}},
		{"  top   researched  ", Request{Command: CommandTop, Arg: "researched"}},
		{"card", Request{Command: CommandCard}},
		{"card team-2", Request{Command: CommandCard, Arg: "team-2"}},
		{"CARD Team Ace", Request{Command: CommandCard, Arg: "Team Ace"}},
		{"#ops card", Request{Name: "ops", Command: CommandCard}},

		// Near misses: real questions that start with the same word.
		{"top 5 reasons my base is slow", Request{Question: "top 5 reasons my base is slow"}},
		{"top rockets please and thanks", Request{Question: "top rockets please and thanks"}},
		{"top", Request{Question: "top"}},
		{"top rockets?", Request{Question: "top rockets?"}},
		{"help me find iron", Request{Question: "help me find iron"}},
		{"help?", Request{Question: "help?"}},
		{"card games are fun to play", Request{Question: "card games are fun to play"}},
		{"cards", Request{Question: "cards"}},
		{"card for team-2?", Request{Question: "card for team-2?"}},
		{"what are the commands", Request{Question: "what are the commands"}},
		// "new" starts a session for a real question, so a command after it
		// is asked as one, as "new sessions" is.
		{"new help", Request{Fresh: true, Question: "help"}},
	} {
		if got := parse(tc.in); got != tc.want {
			t.Errorf("parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}
