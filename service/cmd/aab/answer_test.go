package main

import (
	"context"
	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// The plain path: one question in, one answer delivered, nothing left behind.
func TestTickAnswersAndForgets(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	if companion.answers != 1 {
		t.Fatalf("answers = %d, want 1", companion.answers)
	}
	if len(companion.pending) != 0 {
		t.Fatalf("companion still holds %d question(s)", len(companion.pending))
	}
	if r.after != 1 {
		t.Errorf("poll cursor = %d, want 1: a delivered question leaves the page", r.after)
	}

	// The companion stops offering an answered question, so the service forgets
	// it and never runs the model against it again.
	r.tick(ctx)
	if companion.answers != 1 {
		t.Errorf("answers = %d after a second tick, want still 1", companion.answers)
	}
	if len(r.inFlight) != 0 {
		t.Errorf("inFlight holds %d entry(ies), want 0", len(r.inFlight))
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1", r.answered)
	}
}

// A delivery that fails is retried on the next tick with the artifact already in
// hand: the question is not lost, and the model is not paid twice.
func TestFailedDeliveryIsRetriedWithoutRerunningTheModel(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.answerErr = "too_large"
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	if r.answered != 1 || len(r.inFlight) != 1 {
		t.Fatalf("answered = %d, inFlight = %d; want 1 and 1", r.answered, len(r.inFlight))
	}
	first := r.inFlight[1].result
	if r.after != 0 {
		t.Fatalf("poll cursor = %d, want 0: the question still needs delivering", r.after)
	}

	companion.answerErr = ""
	r.tick(ctx)
	if companion.answers != 2 {
		t.Errorf("answers = %d, want 2: the delivery was not retried", companion.answers)
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1: the model ran again for the same question", r.answered)
	}
	if len(companion.shapes) != 1 || companion.shapes[0] != first.Artifact.Shape {
		t.Errorf("the companion accepted %v, want the artifact the first run produced", companion.shapes)
	}
	if len(companion.pending) != 0 {
		t.Errorf("the question was never answered: %v", companion.pending)
	}
	if r.after != 1 {
		t.Errorf("poll cursor = %d, want 1 once the retry landed", r.after)
	}
}

// Three failed deliveries and the service stops trying, so one stuck question
// cannot have the model run against it on every tick forever.
func TestDeliveryGivesUpAfterThreeFailures(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.answerErr = "too_large"
	r := newTestRunner(t, companion)
	ctx := context.Background()

	for range 5 {
		r.tick(ctx)
	}
	if companion.answers != maxDeliveries {
		t.Errorf("answers = %d, want %d", companion.answers, maxDeliveries)
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1: the model must run once", r.answered)
	}
	if r.after != 1 {
		t.Errorf("poll cursor = %d, want 1: a question given up on leaves the page", r.after)
	}
}

// bad_artifact is the companion saying the artifact itself is wrong, so there is
// nothing to retry. The asker still gets told something, once (second review-fix
// contract 3).
func TestBadArtifactIsFollowedByOneNotice(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.badShape = "summary" // the shape the scripted model submits
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	if companion.answers != 2 {
		t.Fatalf("answer calls = %d, want 2: the refusal plus one notice", companion.answers)
	}
	if len(companion.shapes) != 1 || companion.shapes[0] != "notice" {
		t.Fatalf("the companion accepted %v, want one notice", companion.shapes)
	}
	if len(companion.pending) != 0 {
		t.Errorf("the question is still pending: %v", companion.pending)
	}
	if r.after != 1 {
		t.Errorf("poll cursor = %d, want 1: the question is finished with", r.after)
	}

	r.tick(ctx)
	if companion.answers != 2 {
		t.Errorf("answer calls = %d after a second tick, want still 2", companion.answers)
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1: the model must not run again", r.answered)
	}
}

// The notice goes out even when the companion never rendered the artifact, and a
// notice the companion also refuses costs one more call and no more.
func TestRefusedNoticeIsNotRetried(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.answerErr = rpc.CodeBadArtifact
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	r.tick(ctx)
	if companion.answers != 2 {
		t.Errorf("answer calls = %d, want 2: the refusal plus one notice attempt", companion.answers)
	}
}

// A restart is not a replay: the service keeps no cursor on disk, and the
// companion only offers what it has not rendered, so a fresh runner picks up
// exactly what is still waiting.
func TestRestartAnswersOnlyWhatIsStillPending(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping", 2: "ping"})
	first := newTestRunner(t, companion)
	first.tick(context.Background())
	if companion.answers != 2 {
		t.Fatalf("answers = %d, want 2", companion.answers)
	}

	companion.pending[3] = "ping"
	second := newTestRunner(t, companion)
	second.tick(context.Background())
	if companion.answers != 3 {
		t.Errorf("answers = %d, want 3: the restart re-answered questions", companion.answers)
	}
	if second.answered != 1 {
		t.Errorf("the restarted service ran the model %d times, want 1", second.answered)
	}
}

// The label the log line and the system prompt both carry is built by the
// agent from the fields agentQuestion hands it, so this covers the mapping and
// the label together.
func TestAskerLabel(t *testing.T) {
	index := 2
	for _, tc := range []struct {
		name string
		q    rpc.Question
		want string
	}{
		{"name and index", rpc.Question{PlayerIndex: &index, PlayerName: "Bob", Force: "player"}, "Bob (player 2, force player)"},
		{"index only", rpc.Question{PlayerIndex: &index, Force: "enemy"}, "player 2 (force enemy)"},
		{"another mod", rpc.Question{Force: "enemy"}, "another mod (force enemy)"},
		{"no force hint", rpc.Question{PlayerIndex: &index}, "player 2 (force player)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentQuestion(tc.q).AskerLabel(); got != tc.want {
				t.Errorf("asker label = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two services on one server each answer every question; the companion keeps
// the first and says yes to the second. The rendered read-back is where that
// becomes visible, by shape and line count, which survive the companion's
// own label and sprite decoration.
func TestPrintedSomethingElseSeesAnotherServicesAnswer(t *testing.T) {
	sent := agent.Artifact{Shape: agent.ShapeSummary, Lines: []string{"16 teams have been claimed", "and 5 empty slots"}}
	if got := printedSomethingElse(sent, "summary", 2); got != "" {
		t.Errorf("same shape and count reported a mismatch: %q", got)
	}
	if got := printedSomethingElse(sent, "summary", 1); got == "" {
		t.Error("a different line count went unreported")
	}
	if got := printedSomethingElse(sent, "list", 2); got == "" {
		t.Error("a different shape went unreported")
	}
	// A title prints as its own line, and a table prints more lines than it
	// has rows, so only the shapes printed one line per entry are counted.
	titled := agent.Artifact{Shape: agent.ShapeList, Title: "Teams", Items: []string{"team-1", "team-2"}}
	if got := printedSomethingElse(titled, "list", 3); got != "" {
		t.Errorf("a titled list of two printed as three lines was reported: %q", got)
	}
	table := agent.Artifact{Shape: agent.ShapeTable, Columns: []string{"a"}, Rows: [][]string{{"1"}}}
	if got := printedSomethingElse(table, "table", 5); got != "" {
		t.Errorf("a table's line count was compared: %q", got)
	}
}
