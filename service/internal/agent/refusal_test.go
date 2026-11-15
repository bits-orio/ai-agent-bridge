package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/ledger"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
)

// A1 (review findings). A refused submission is recorded with what the model
// actually sent, in the ledger and in the trace: a host once looped on
// "a summary needs at least one entry in lines" until round 6 and nobody
// could say what it had written instead.
func TestARefusedSubmissionRecordsItsArguments(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "chart", "lines": []string{"the prose the model wrote"}}),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"fixed"}}),
	}}
	log := &traceLog{}
	a := New(m, caps())
	a.Trace = log.printf
	a.Ledger = ledger.Open(dir, true)

	if _, err := a.Answer(context.Background(), Question{ID: 5101, Text: "q", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()

	rounds := roundsFor(t, dir, 5101)
	if len(rounds) != 2 || len(rounds[0].SubmitRefusals) != 1 {
		t.Fatalf("rounds = %+v, want two with one refusal in the first", rounds)
	}
	if got := rounds[0].SubmitRefusals[0].Args; !strings.Contains(got, "the prose the model wrote") || !strings.Contains(got, `"shape":"chart"`) {
		t.Errorf("refusal args = %q, want the arguments as the model sent them", got)
	}
	if lines := log.with("submit_answer refused (invalid)"); len(lines) != 1 || !strings.Contains(lines[0], "the prose the model wrote") {
		t.Errorf("trace = %q, want the refused arguments on the refusal line", lines)
	}
}

// The arguments are clipped, so a model that writes a novel into a refused
// submission cannot blow up the ledger line.
func TestARefusedSubmissionsArgumentsAreClipped(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("word ", 2000)
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "chart", "text": huge}),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"fixed"}}),
	}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	if _, err := a.Answer(context.Background(), Question{ID: 5102, Text: "q", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	got := roundsFor(t, dir, 5102)[0].SubmitRefusals[0].Args
	if len(got) > refusedArgsBytes+100 || !strings.Contains(got, "[cut:") {
		t.Errorf("refusal args are %d bytes, want at most about %d and marked cut", len(got), refusedArgsBytes)
	}
}

// The obvious slips are repaired, not refused: the text arrived, just in a
// field the shape does not read.
func TestASubmissionWithItsTextInTheWrongFieldIsSalvaged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sent      map[string]any
		wantShape string
		want      []string // the lines, items, or the notice text as one entry
	}{
		{"summary prose in text", map[string]any{"shape": "summary", "text": "Iron is 42 a minute.\n\nCopper is 30 a minute."},
			ShapeSummary, []string{"Iron is 42 a minute.", "Copper is 30 a minute."}},
		{"summary prose in items", map[string]any{"shape": "summary", "items": []string{"one", "two"}},
			ShapeSummary, []string{"one", "two"}},
		{"summary with only a title", map[string]any{"shape": "summary", "title": "Nobody is online."},
			ShapeSummary, []string{"Nobody is online."}},
		{"summary with blank lines and text", map[string]any{"shape": "summary", "lines": []string{" ", ""}, "text": "Real answer."},
			ShapeSummary, []string{"Real answer."}},
		{"notice text in lines", map[string]any{"shape": "notice", "lines": []string{"I could not find it.", "Try nauvis."}},
			ShapeNotice, []string{"I could not find it. Try nauvis."}},
		{"notice text in title", map[string]any{"shape": "notice", "title": "Which surface?", "level": "confirmation"},
			ShapeNotice, []string{"Which surface?"}},
		{"list entries in lines", map[string]any{"shape": "list", "lines": []string{"alpha", "beta"}},
			ShapeList, []string{"alpha", "beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m := &scriptedModel{steps: []model.Step{submitStep("s1", tc.sent)}}
			a := New(m, caps())
			a.Ledger = ledger.Open(dir, true)
			res, err := a.Answer(context.Background(), Question{ID: 5103, Text: "q", PlayerIndex: player(1)}, nil)
			if err != nil {
				t.Fatalf("answer: %v", err)
			}
			a.Ledger.Close()
			if m.calls != 1 {
				t.Fatalf("model calls = %d, want 1: the slip is repaired, not sent back", m.calls)
			}
			got := res.Artifact
			if got.Shape != tc.wantShape {
				t.Fatalf("shape = %q, want %q: %+v", got.Shape, tc.wantShape, got)
			}
			var entries []string
			switch got.Shape {
			case ShapeSummary:
				entries = got.Lines
			case ShapeList:
				entries = got.Items
			case ShapeNotice:
				entries = []string{got.Text}
			}
			if strings.Join(entries, "|") != strings.Join(tc.want, "|") {
				t.Errorf("entries = %q, want %q", entries, tc.want)
			}
			rounds := roundsFor(t, dir, 5103)
			if len(rounds) != 1 || rounds[0].Salvaged == nil || rounds[0].Salvaged.Note == "" || rounds[0].Salvaged.Args == "" {
				t.Errorf("round record = %+v, want the salvage noted with the arguments sent", rounds)
			}
		})
	}
}

// A salvaged summary whose text is longer than the caps still obeys them:
// every line within the cell cap, at most the summary's line count.
func TestASalvagedSummaryObeysTheCaps(t *testing.T) {
	long := strings.Repeat("This sentence is about forty characters. ", 30)
	art, note, ok := salvage(Artifact{Shape: ShapeSummary, Text: long})
	if !ok || note == "" {
		t.Fatalf("salvage = %+v, %q, %v; want a summary built from text", art, note, ok)
	}
	if len(art.Lines) == 0 || len(art.Lines) > MaxSummaryLines {
		t.Fatalf("lines = %d, want 1 to %d", len(art.Lines), MaxSummaryLines)
	}
	for i, line := range art.Lines {
		if n := len([]rune(line)); n > MaxCellChars {
			t.Errorf("line %d is %d characters, over the %d cap", i, n, MaxCellChars)
		}
	}
}

// A submission with nothing anywhere is still refused: there is nothing to
// salvage.
func TestAGenuinelyEmptySubmissionIsStillRefused(t *testing.T) {
	for _, a := range []Artifact{
		{Shape: ShapeSummary},
		{Shape: ShapeSummary, Lines: []string{" ", "[color=red][/color]"}},
		{Shape: ShapeNotice, Level: LevelWarning},
		{Shape: ShapeList, Title: "   "},
	} {
		if _, _, ok := salvage(a); ok {
			t.Errorf("salvage(%+v) repaired an artifact with nothing in it", a)
		}
		if sub := parseSubmission(mustJSON(t, a)); sub.err == nil {
			t.Errorf("parseSubmission(%+v) accepted an artifact with nothing in it", a)
		}
	}
}

// The refusal tells the model exactly what to change and how many turns it
// has left to change it in.
func TestTheInvalidRefusalSaysHowToFixItAndTurnsLeft(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "summary"}),
		submitStep("s2", map[string]any{"shape": "summary", "lines": []string{"fixed"}}),
	}}
	c := caps()
	c.MaxRounds = 4
	a := New(m, c)
	if _, err := a.Answer(context.Background(), Question{ID: 5104, Text: "q", PlayerIndex: player(1)}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	results := m.msgs[2].Blocks // round 1's results, read by round 2
	if len(results) == 0 || !results[0].IsError {
		t.Fatalf("round 1 results = %+v, want the refusal", results)
	}
	got := results[0].Content
	for _, want := range []string{"put your sentences in `lines`, an array of strings", "3 turns left"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want it to say %q", got, want)
		}
	}
}

// Two identical invalid refusals in a row and the loop stops: a host that
// sends the same empty summary twice sends it six times. The player gets a
// notice saying what went wrong, not the generic rounds one.
func TestTwoIdenticalInvalidRefusalsEndTheQuestion(t *testing.T) {
	dir := t.TempDir()
	m := &scriptedModel{steps: []model.Step{submitStep("s1", map[string]any{"shape": "summary"})}}
	a := New(m, caps())
	a.Ledger = ledger.Open(dir, true)
	res, err := a.Answer(context.Background(), Question{ID: 5105, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	a.Ledger.Close()
	if m.calls != 2 {
		t.Errorf("model calls = %d, want 2: the second identical refusal ends it", m.calls)
	}
	if res.Artifact.Shape != ShapeNotice || res.Artifact.Text != unshowableNotice {
		t.Errorf("artifact = %+v, want the unshowable notice", res.Artifact)
	}
	if rec := findQuestion(t, dir, 5105); rec.Rounds != 2 {
		t.Errorf("ledger rounds = %d, want 2", rec.Rounds)
	}
}

// Two different mistakes are not a loop: the model is fixing something.
func TestTwoDifferentInvalidRefusalsKeepGoing(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "chart"}),
		submitStep("s2", map[string]any{"shape": "summary"}),
		submitStep("s3", map[string]any{"shape": "summary", "lines": []string{"there"}}),
	}}
	a := New(m, caps())
	res, err := a.Answer(context.Background(), Question{ID: 5106, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "there" || m.calls != 3 {
		t.Errorf("artifact = %+v after %d calls, want the third submission", res.Artifact, m.calls)
	}
}

func mustJSON(t *testing.T, a Artifact) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal %+v: %v", a, err)
	}
	return raw
}

// The invalid-streak stop and the final round meet: the same unshowable
// answer twice, the second on the last round, ends with the unshowable
// notice, not the rounds one; two different mistakes ending on the last round
// get the rounds notice.
func TestTheInvalidStreakNoticeBeatsTheRoundsNoticeOnTheLastRound(t *testing.T) {
	two := caps()
	two.MaxRounds = 2
	same := &scriptedModel{steps: []model.Step{submitStep("s1", map[string]any{"shape": "summary"})}}
	res, err := New(same, two).Answer(context.Background(), Question{ID: 5107, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Text != unshowableNotice {
		t.Errorf("artifact = %+v, want the unshowable notice for the same refusal twice", res.Artifact)
	}
	differ := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "chart"}),
		submitStep("s2", map[string]any{"shape": "summary"}),
	}}
	res, err = New(differ, two).Answer(context.Background(), Question{ID: 5108, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Text != roundsNotice {
		t.Errorf("artifact = %+v, want the rounds notice: two different mistakes are no streak", res.Artifact)
	}
}

// An over-cap round between two identical invalid refusals is a submission
// weighed on its merits, so it resets the streak: the model is rewriting.
func TestAnOverCapRoundBreaksTheInvalidStreak(t *testing.T) {
	m := &scriptedModel{steps: []model.Step{
		submitStep("s1", map[string]any{"shape": "summary"}),
		overlong("s2"),
		submitStep("s3", map[string]any{"shape": "summary"}),
		submitStep("s4", map[string]any{"shape": "summary", "lines": []string{"fixed"}}),
	}}
	res, err := New(m, caps()).Answer(context.Background(), Question{ID: 5109, Text: "q", PlayerIndex: player(1)}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if res.Artifact.Line() != "fixed" || m.calls != 4 {
		t.Errorf("artifact = %+v after %d calls, want the fourth submission", res.Artifact, m.calls)
	}
}
