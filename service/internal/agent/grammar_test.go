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
