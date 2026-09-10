package main

import (
	"context"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// The page comes down one half at a time until a reply fits, then probes back up.
func TestPollPageHalvesOnTooLargeAndDoublesBack(t *testing.T) {
	companion := newFakeCompanion(questionRing(3))
	companion.tooLargeAbove = 4
	r := newTestRunner(t, companion)
	ctx := context.Background()

	for want := pollLimit / 2; want >= 4; want /= 2 {
		r.tick(ctx)
		if r.pageSize() != want {
			t.Fatalf("page after a too_large reply = %d, want %d", r.pageSize(), want)
		}
		if r.answered != 0 {
			t.Fatalf("answered %d questions while every poll was refused", r.answered)
		}
	}

	r.tick(ctx)
	if r.answered != 3 {
		t.Fatalf("answered %d after the page shrank under the cap, want 3", r.answered)
	}
	if r.pageSize() != 8 {
		t.Fatalf("page after a poll that landed = %d, want 8: it must probe upward", r.pageSize())
	}
	// Eight is still over the cap, so the service drops straight back to a page
	// that works. One refused poll is what probing costs.
	r.tick(ctx)
	if r.pageSize() != 4 {
		t.Fatalf("page after the probe was refused = %d, want 4", r.pageSize())
	}
}

// The page grows back while the backlog stays full. Waiting for a short page
// instead pinned it at one for ever: at a page of one, a single pending question
// makes every page full (second review-fix contract 4).
func TestPollPageGrowsBackWhileTheBacklogStaysFull(t *testing.T) {
	companion := newFakeCompanion(questionRing(6))
	companion.tooLargeAbove = 1
	r := newTestRunner(t, companion)
	ctx := context.Background()

	// Four refusals bring the page to one, where the fifth poll fits.
	for range 5 {
		r.tick(ctx)
	}
	if r.pageSize() != 2 {
		t.Fatalf("page = %d, want 2: one landed, so the next probes upward", r.pageSize())
	}
	if r.answered != 1 {
		t.Fatalf("answered = %d, want 1 at a page of one", r.answered)
	}

	// The oversized reply is gone, the backlog is not. Every page is still full,
	// which is exactly the state that used to hold the page down.
	companion.tooLargeAbove = 0
	r.tick(ctx)
	if r.pageSize() <= 2 {
		t.Fatalf("page = %d, want it growing while the backlog is full", r.pageSize())
	}
	for range 4 {
		r.tick(ctx)
	}
	if len(companion.pending) != 0 {
		t.Errorf("%d question(s) never answered: %v", len(companion.pending), companion.pending)
	}
	if r.pageSize() != pollLimit {
		t.Errorf("page = %d, want it back at %d", r.pageSize(), pollLimit)
	}
}

// A question the service gave up on leaves the page, so it cannot occupy a slot
// of every later poll and make the service deaf to everything new (second
// review-fix contract 3).
func TestCursorAdvancesPastAGivenUpQuestion(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.answerErr = "too_large"
	r := newTestRunner(t, companion)
	ctx := context.Background()

	for range maxDeliveries {
		r.tick(ctx)
	}
	if r.after != 1 {
		t.Fatalf("poll cursor = %d, want 1 after giving up on question 1", r.after)
	}
	if len(r.inFlight) != 0 {
		t.Errorf("inFlight holds %d entry(ies) for a question finished with", len(r.inFlight))
	}

	// The abandoned question is still pending in the game, and a later one is
	// still picked up.
	companion.answerErr = ""
	companion.pending[2] = "ping"
	r.tick(ctx)
	if companion.answers != maxDeliveries+1 {
		t.Fatalf("answer calls = %d, want %d: the later question was not delivered", companion.answers, maxDeliveries+1)
	}
	if _, stillPending := companion.pending[2]; stillPending {
		t.Error("question 2 was never answered")
	}
	if _, gone := companion.pending[1]; !gone {
		t.Error("question 1 was answered after the service gave up on it")
	}
	if r.answered != 2 {
		t.Errorf("answered = %d, want 2: one model run per question", r.answered)
	}
}

// A page that is all done questions moves the cursor past every one of them, and
// a question still waiting stops it dead.
func TestCursorStopsAtTheFirstQuestionStillWaiting(t *testing.T) {
	r := newTestRunner(t, newFakeCompanion(nil))
	// Listed out of order on purpose: the cursor must not step over question 5
	// just because the page mentioned 6 first.
	offered := rpc.PollReply{{ID: 6}, {ID: 4}, {ID: 5}}
	r.inFlight[4] = &delivery{done: true}
	r.inFlight[5] = &delivery{}
	r.inFlight[6] = &delivery{done: true}

	r.advanceWatermark(offered)
	if r.after != 4 {
		t.Errorf("poll cursor = %d, want 4: question 5 is still waiting", r.after)
	}
	if _, kept := r.inFlight[5]; !kept {
		t.Error("the question still waiting was forgotten")
	}
	if _, kept := r.inFlight[6]; !kept {
		t.Error("a question above the one still waiting was forgotten")
	}

	r.inFlight[5].done = true
	r.advanceWatermark(rpc.PollReply{{ID: 5}, {ID: 6}})
	if r.after != 6 {
		t.Errorf("poll cursor = %d, want 6 once the whole page is finished with", r.after)
	}
	if len(r.inFlight) != 0 {
		t.Errorf("inFlight holds %d entry(ies), want 0", len(r.inFlight))
	}
}

// A question beyond a full poll page keeps its state: the service has not been
// shown everything pending yet.
func TestForgetGoneKeepsQuestionsBeyondAFullPage(t *testing.T) {
	r := newTestRunner(t, newFakeCompanion(nil))
	offered := make(rpc.PollReply, 0, pollLimit)
	for id := range pollLimit {
		offered = append(offered, rpc.Question{ID: int64(id + 1)})
		r.inFlight[int64(id+1)] = &delivery{done: true}
	}
	beyond := int64(pollLimit + 5)
	r.inFlight[beyond] = &delivery{done: true}
	r.inFlight[0] = &delivery{done: true}

	r.forgetGone(offered, pollLimit)
	if _, ok := r.inFlight[beyond]; !ok {
		t.Error("state for a question above a full page was dropped")
	}
	if _, ok := r.inFlight[0]; ok {
		t.Error("state for a question the companion no longer offers was kept")
	}

	r.forgetGone(offered[:1], pollLimit)
	if len(r.inFlight) != 1 {
		t.Errorf("inFlight holds %d entry(ies) after a short page, want 1", len(r.inFlight))
	}
}
