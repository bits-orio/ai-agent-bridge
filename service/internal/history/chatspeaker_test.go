package history

// Every chat row names the speaker's force and whether the line was said in
// a private channel. On the rig, a team-6 member asked privately what their
// team had said; the model read team-6's own private line by Pirion with no
// force beside it and answered that the only chat came from other teams.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// teamSix is team-6's private line and another team's public one.
func teamSix(t *testing.T) *Store {
	t.Helper()
	s := mustOpen(t, filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = s.Close() })
	for _, l := range []string{
		`{"event":"console_chat","tick":5000,"data":{"player":"Pirion","force":"team-6","message":"we need more iron","scope":"team-6","private":true}}`,
		`{"event":"console_chat","tick":5100,"data":{"player":"Trophium","force":"team-2","message":"gg all","scope":"global","private":false}}`,
	} {
		ingest(t, s, l)
	}
	return s
}

// chatRows is a chat tool's rows keyed by player, each row's cells. The
// player is the column the header names so.
func chatRows(t *testing.T, s *Store, ctx context.Context, tool string, args map[string]any) map[string][]string {
	t.Helper()
	var raw json.RawMessage
	if args != nil {
		raw, _ = json.Marshal(args)
	}
	out, err := findTool(t, s.Tools(), tool).Call(ctx, raw)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	header, rows := splitColumnar(t, out)
	player := slices.Index(splitRow(header), "player")
	byPlayer := map[string][]string{}
	for _, row := range rows {
		cells := splitRow(row)
		byPlayer[cells[player]] = cells
	}
	return byPlayer
}

func TestChatRowsNameTheSpeakersForceAndPrivateLines(t *testing.T) {
	s := teamSix(t)
	team6 := scoped("team-6", true)

	recent := chatRows(t, s, team6, "recent_chat", nil)
	for player, want := range map[string][]string{
		"Pirion":   {"5000", "Pirion", "team-6", "yes", "we need more iron"},
		"Trophium": {"5100", "Trophium", "team-2", "no", "gg all"},
	} {
		if got := strings.Join(recent[player], "|"); got != strings.Join(want, "|") {
			t.Errorf("recent_chat row for %s = %q, want %q", player, got, strings.Join(want, "|"))
		}
	}

	caught := chatRows(t, s, team6, "catch_up", map[string]any{"since_tick": 1})
	for player, want := range map[string][]string{
		"Pirion":   {"5000", "console_chat", "Pirion", "team-6", "yes", "we need more iron"},
		"Trophium": {"5100", "console_chat", "Trophium", "team-2", "no", "gg all"},
	} {
		if got := strings.Join(caught[player], "|"); got != strings.Join(want, "|") {
			t.Errorf("catch_up row for %s = %q, want %q", player, got, strings.Join(want, "|"))
		}
	}

	lines := s.RecentChatLines(team6, 5)
	if len(lines) != 2 || lines[0].Force != "team-6" || !lines[0].Private || lines[1].Force != "team-2" || lines[1].Private {
		t.Errorf("the briefing's chat = %+v, want Pirion on team-6 private, then Trophium on team-2 in the open", lines)
	}
}

// The flag never shows a public question anything: it reads public lines
// only, each marked as such.
func TestAPublicQuestionsChatRowsAreAllPublic(t *testing.T) {
	s := teamSix(t)
	for _, ctx := range []context.Context{context.Background(), scoped("global", false), scoped("team-6", false)} {
		recent := chatRows(t, s, ctx, "recent_chat", nil)
		if _, seen := recent["Pirion"]; seen || len(recent) != 1 || recent["Trophium"][3] != "no" {
			t.Errorf("a public question's recent_chat = %v, want Trophium's public line alone", recent)
		}
		for _, line := range s.RecentChatLines(ctx, 5) {
			if line.Private {
				t.Errorf("a public question's briefing chat carries a private line: %+v", line)
			}
		}
	}
}

// catch_up names each row's force once, in its own column: a research or a
// rocket no longer repeats it in the detail.
func TestCatchUpNamesEachRowsForceOnce(t *testing.T) {
	s := teamSix(t)
	ingest(t, s, `{"event":"research_finished","tick":5200,"data":{"force":"team-6","tech":"automation","level":1}}`)
	ingest(t, s, `{"event":"rocket_launched","tick":5300,"data":{"force":"team-2","surface":"nauvis"}}`)
	out, err := findTool(t, s.Tools(), "catch_up").Call(context.Background(), json.RawMessage(`{"since_tick":1}`))
	if err != nil {
		t.Fatalf("catch_up: %v", err)
	}
	_, rows := splitColumnar(t, out)
	want := map[string]string{
		"research_finished": "5200|research_finished||team-6|no|automation (level 1)",
		"rocket_launched":   "5300|rocket_launched||team-2|no|surface nauvis",
	}
	for _, row := range rows {
		cells := splitRow(row)
		if w, ok := want[cells[1]]; ok && strings.Join(cells, "|") != w {
			t.Errorf("catch_up row = %q, want %q", strings.Join(cells, "|"), w)
		}
		delete(want, cells[1])
	}
	if len(want) != 0 {
		t.Errorf("catch_up left out %v:\n%s", want, out)
	}
}
