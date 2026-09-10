package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// fakeCompanion answers aab-rpc commands in memory: a question ring the poll op
// reads, and an answer op that can be made to fail. It is the companion's own
// contract, not a mock of the service: poll offers only questions that have not
// been answered, oldest first, at most limit of them.
type fakeCompanion struct {
	// tooLargeAbove, when set, makes poll refuse any page larger than it, the
	// way the companion refuses a reply over its byte cap.
	tooLargeAbove int
	pending       map[int64]string // question id to text
	// answerErr is the protocol error code the next answer is refused with.
	// Empty means the answer lands.
	answerErr string

	polls   int
	answers int
}

func newFakeCompanion(texts map[int64]string) *fakeCompanion {
	return &fakeCompanion{pending: texts}
}

func (f *fakeCompanion) Execute(cmd string) (string, error) {
	body, ok := strings.CutPrefix(cmd, "/aab-rpc ")
	if !ok {
		return "", fmt.Errorf("not an aab-rpc command: %q", cmd)
	}
	var req struct {
		Op    string `json:"op"`
		After int64  `json:"after"`
		Limit int    `json:"limit"`
		QID   int64  `json:"qid"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return `{"ok":false,"e":"bad_json","m":"unparseable"}`, nil
	}

	switch req.Op {
	case "status":
		return `{"ok":true,"r":{"protocol":1,"mod_version":"test","tick":1,"player_count":1,"pending":0,"ask_command":"ask","last_id":0}}`, nil
	case "tools":
		return `{"ok":true,"r":{}}`, nil
	case "poll":
		f.polls++
		return f.poll(req.After, req.Limit), nil
	case "answer":
		f.answers++
		if f.answerErr != "" {
			return fmt.Sprintf(`{"ok":false,"e":%q,"m":"refused"}`, f.answerErr), nil
		}
		delete(f.pending, req.QID)
		return `{"ok":true,"r":true}`, nil
	}
	return `{"ok":false,"e":"bad_op","m":"unknown op"}`, nil
}

func (f *fakeCompanion) poll(after int64, limit int) string {
	if f.tooLargeAbove > 0 && limit > f.tooLargeAbove {
		return `{"ok":false,"e":"too_large","m":"result exceeds the reply cap"}`
	}
	ids := make([]int64, 0, len(f.pending))
	for id := range f.pending {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	rows := make([]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"text":%q,"player_index":1,"player_name":"Bob","force":"player","tick":100}`, id, f.pending[id]))
	}
	return `{"ok":true,"r":[` + strings.Join(rows, ",") + `]}`
}

func newTestRunner(t *testing.T, companion *fakeCompanion) *runner {
	t.Helper()
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	client := rpc.New(companion)
	return &runner{
		cfg:      &config.Config{},
		rpc:      client,
		caller:   &toolCaller{client: client},
		store:    store,
		stats:    controlapi.NewStats(fake.ModelID, time.Now()),
		agent:    agent.New(fake.New(), agent.Caps{MaxRounds: 2}),
		inFlight: map[int64]*delivery{},
	}
}

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

	companion.answerErr = ""
	r.tick(ctx)
	if companion.answers != 2 {
		t.Errorf("answers = %d, want 2: the delivery was not retried", companion.answers)
	}
	if r.answered != 1 {
		t.Errorf("answered = %d, want 1: the model ran again for the same question", r.answered)
	}
	if r.inFlight[1].result.Artifact.Shape != first.Artifact.Shape {
		t.Errorf("the retry delivered a different artifact")
	}
	if len(companion.pending) != 0 {
		t.Errorf("the question was never answered: %v", companion.pending)
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
	if !r.inFlight[1].done {
		t.Error("the question is still open after three failures")
	}
}

// bad_artifact is the companion saying the artifact itself is wrong, so there is
// nothing to retry (review-fix contract 5).
func TestBadArtifactIsNotRetried(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.answerErr = rpc.CodeBadArtifact
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	r.tick(ctx)
	if companion.answers != 1 {
		t.Errorf("answers = %d, want 1: bad_artifact must not be retried", companion.answers)
	}
}

// A restart is not a replay: the service keeps no cursor, and the companion only
// offers what it has not rendered, so a fresh runner picks up exactly what is
// still waiting.
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

// A poll that keeps failing logs once, not once per tick.
func TestPollFailureLogsOncePerStreak(t *testing.T) {
	r := newTestRunner(t, newFakeCompanion(nil))
	r.pollFailed(fmt.Errorf("first"))
	if !r.pollFailing {
		t.Fatal("the streak was not recorded")
	}
	r.pollFailed(fmt.Errorf("second"))
	r.pollRecovered()
	if r.pollFailing {
		t.Error("the streak survived a successful poll")
	}
}

// The heartbeat stays quiet while questions are arriving and speaks up when the
// service has been idle for the whole window.
func TestHeartbeatOnlyWhenIdle(t *testing.T) {
	r := newTestRunner(t, newFakeCompanion(nil))
	r.lastActivity = time.Now()
	r.heartbeat()
	if time.Since(r.lastActivity) > time.Second {
		t.Error("the heartbeat fired while the service was busy")
	}

	r.lastActivity = time.Now().Add(-2 * heartbeatEvery)
	r.heartbeat()
	if time.Since(r.lastActivity) > time.Second {
		t.Error("the heartbeat did not fire after a whole idle window")
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

func TestPollPageHalvesOnTooLargeAndRecovers(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "one", 2: "two", 3: "three"})
	companion.tooLargeAbove = 4
	r := newTestRunner(t, companion)

	for want := pollLimit / 2; want >= 4; want /= 2 {
		r.tick(context.Background())
		if r.pageSize() != want {
			t.Fatalf("page after a too_large reply = %d, want %d", r.pageSize(), want)
		}
		if r.answered != 0 {
			t.Fatalf("answered %d questions while every poll was refused", r.answered)
		}
	}
	r.tick(context.Background())
	if r.answered != 3 {
		t.Fatalf("answered %d after the page shrank under the cap, want 3", r.answered)
	}
	if r.pageSize() != pollLimit {
		t.Fatalf("page after a short reply = %d, want %d again", r.pageSize(), pollLimit)
	}
}
