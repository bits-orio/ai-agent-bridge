package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
)

var techs = config.DefaultToastTechnologies

func TestMilestoneFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   judgeInput
		want string // the plain line, "" for no toast
	}{
		{"first rocket", judgeInput{event: "rocket_launched", force: "team-1", rockets: 1, rocketForces: 1}, "team-1 launched its first rocket."},
		{"second rocket is silent", judgeInput{event: "rocket_launched", force: "team-1", rockets: 2, rocketForces: 1}, ""},
		{"tenth rocket", judgeInput{event: "rocket_launched", force: "team-1", rockets: 10, rocketForces: 1}, "team-1 has launched 10 rockets."},
		{"hundredth rocket", judgeInput{event: "rocket_launched", force: "team-1", rockets: 100, rocketForces: 3}, "team-1 has launched 100 rockets."},
		{"thousandth rocket", judgeInput{event: "rocket_launched", force: "team-1", rockets: 1000, rocketForces: 3}, "team-1 has launched 1,000 rockets."},
		{"eleventh rocket is silent", judgeInput{event: "rocket_launched", force: "team-1", rockets: 11, rocketForces: 1}, ""},
		{"a second force's first rocket counts forces", judgeInput{event: "rocket_launched", force: "team-2", rockets: 1, rocketForces: 2}, "team-2 launched its first rocket. That makes 2 forces in space."},
		{"first headline tech", judgeInput{event: "research_finished", force: "team-1", tech: "logistic-science-pack"}, "team-1 is the first to research [technology=logistic-science-pack]."},
		{"second force, same tech, is silent", judgeInput{event: "research_finished", force: "team-2", tech: "logistic-science-pack", techBefore: true}, ""},
		{"non-headline tech is silent", judgeInput{event: "research_finished", force: "team-1", tech: "automation"}, ""},
		{"rocket silo", judgeInput{event: "research_finished", force: "team-1", tech: "rocket-silo"}, "team-1 is the first to research [technology=rocket-silo]."},
		{"planet discovery", judgeInput{event: "research_finished", force: "team-1", tech: "planet-discovery-vulcanus"}, "team-1 is the first to research [technology=planet-discovery-vulcanus]."},
		{"no force", judgeInput{event: "rocket_launched", rockets: 1}, ""},
		{"another event", judgeInput{event: "player_died", force: "team-1", rockets: 1}, ""},
	} {
		m, ok := milestoneFor(tc.in, techs)
		got := ""
		if ok {
			got = plainLine(m)
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHeadlineGlobs(t *testing.T) {
	for tech, want := range map[string]bool{
		"automation-science-pack":   true,
		"promethium-science-pack":   true,
		"space-platform":            true,
		"space-platform-thruster":   false,
		"planet-discovery-aquilo":   true,
		"rocket-silo":               true,
		"mining-productivity-3":     false,
		"science-pack":              false,
		"electromagnetic-plant":     false,
		"logistic-science-pack-ish": false,
	} {
		if got := headline(tech, techs); got != want {
			t.Errorf("headline(%q) = %v, want %v", tech, got, want)
		}
	}
	if !headline("steel-processing", []string{"steel-*"}) || headline("steel-processing", nil) {
		t.Error("the patterns are the configured ones")
	}
}

func openStore(t *testing.T) *history.Store {
	t.Helper()
	s, err := history.Open(filepath.Join(t.TempDir(), "h.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// judge reads the counts from history, in the row's own epoch.
func TestJudgeCountsInTheRowsEpoch(t *testing.T) {
	s := openStore(t)
	feed := &toastFeed{}
	feed.caughtUp()
	lines := []string{
		`{"event":"session","tick":1,"data":{"map_id":"M1"}}`,
		`{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`,
		`{"event":"rocket_launched","tick":20001,"data":{"force":"team-1","surface":"nauvis"}}`,
		`{"event":"research_finished","tick":20002,"data":{"force":"team-1","tech":"rocket-silo","level":1}}`,
		`{"event":"research_finished","tick":20003,"data":{"force":"team-2","tech":"rocket-silo","level":1}}`,
		`{"event":"research_finished","tick":20004,"data":{"force":"team-2","tech":"automation","level":1}}`,
		`{"event":"rocket_launched","tick":20005,"data":{"force":"team-2","surface":"nauvis"}}`,
	}
	for _, l := range lines {
		if err := ingestLine(s, feed, techs, []byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for c, ok := feed.take(); ok; c, ok = feed.take() {
		got = append(got, plainLine(c.m))
	}
	want := []string{
		"team-1 launched its first rocket.",
		"team-1 is the first to research [technology=rocket-silo].",
		"team-2 launched its first rocket. That makes 2 forces in space.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("queued:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A new map starts the counts again.
	ingestLine(s, feed, techs, []byte(`{"event":"session","tick":1,"data":{"map_id":"M2"}}`))
	ingestLine(s, feed, techs, []byte(`{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`))
	if c, ok := feed.take(); !ok || c.m.kind != toastFirstRocket || c.epoch != s.CurrentEpoch() {
		t.Errorf("first rocket on the new map: %+v, %v", c, ok)
	}
}

// Nothing the tailer delivers before it first caught up is news: that is
// the first pass after a start.
func TestNoToastFromTheStartupPass(t *testing.T) {
	s := openStore(t)
	feed := &toastFeed{}
	ingestLine(s, feed, techs, []byte(`{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`))
	if _, ok := feed.take(); ok {
		t.Error("a toast was queued before the tailer caught up")
	}
	feed.caughtUp()
	ingestLine(s, feed, techs, []byte(`{"event":"rocket_launched","tick":20001,"data":{"force":"team-1","surface":"nauvis"}}`))
	if _, ok := feed.take(); ok {
		t.Error("the second rocket was announced: the first one was stored, only not announced")
	}
}

// A line the history guards quarantined (it may be the map before) is not
// public, and makes no toast.
func TestNoToastFromAQuarantinedRow(t *testing.T) {
	s := openStore(t)
	feed := &toastFeed{}
	feed.caughtUp()
	ingestLine(s, feed, techs, []byte(`{"event":"session","tick":1,"data":{"map_id":"OLD"}}`))
	if _, err := s.NoteMap(s.Mark(), "NEW", 900, 0); err != nil {
		t.Fatal(err)
	}
	ingestLine(s, feed, techs, []byte(`{"event":"rocket_launched","tick":20000,"data":{"force":"team-1","surface":"nauvis"}}`))
	if c, ok := feed.take(); ok {
		t.Errorf("a quarantined line made a toast: %+v", c)
	}
}

func TestToastFeedIsBounded(t *testing.T) {
	f := &toastFeed{}
	for i := range feedCap {
		if !f.offer(candidate{tick: int64(i)}) {
			t.Fatalf("offer %d refused below the cap", i)
		}
	}
	if f.offer(candidate{}) {
		t.Error("an offer past the cap was taken")
	}
	if n := f.drop(); n != feedCap {
		t.Errorf("drop = %d", n)
	}
}

func TestToasterRules(t *testing.T) {
	now := time.Now()
	ok := sendState{players: 1, epoch: 3, statusTick: 30_000}
	c := candidate{m: milestone{kind: toastFirstRocket, force: "team-1"}, epoch: 3, tick: 29_000}
	for _, tc := range []struct {
		name   string
		edit   func(*sendState, *candidate, *toaster)
		reason string // a word of the reason, "" to send
	}{
		{"sends", func(*sendState, *candidate, *toaster) {}, ""},
		{"wrong epoch", func(s *sendState, _ *candidate, _ *toaster) { s.epoch = 4 }, "another map"},
		{"stale tick", func(_ *sendState, c *candidate, _ *toaster) { c.tick = 30_000 - staleTicks - 1 }, "five game minutes"},
		{"exactly five minutes is news", func(_ *sendState, c *candidate, _ *toaster) { c.tick = 30_000 - staleTicks }, ""},
		{"an event after the status is news", func(_ *sendState, c *candidate, _ *toaster) { c.tick = 40_000 }, ""},
		{"no players", func(s *sendState, _ *candidate, _ *toaster) { s.players = 0 }, "nobody"},
		{"no players, when_empty", func(s *sendState, _ *candidate, t *toaster) { s.players = 0; t.cfg.WhenEmpty = true }, ""},
		{"lost", func(s *sendState, _ *candidate, _ *toaster) { s.lost = true }, "out of reach"},
		{"unsupported", func(_ *sendState, _ *candidate, t *toaster) { t.unsupported = true }, "does not take say"},
		{"cap", func(_ *sendState, _ *candidate, t *toaster) { t.sent = []time.Time{now, now} }, "max_per_hour"},
		{"cap forgets an hour ago", func(_ *sendState, _ *candidate, t *toaster) { t.sent = []time.Time{now.Add(-61 * time.Minute), now} }, ""},
	} {
		st, cand := ok, c
		tt := &toaster{cfg: config.ToastsConfig{Mode: "plain", MaxPerHour: 2}, feed: &toastFeed{}}
		tc.edit(&st, &cand, tt)
		got := tt.why(cand, st, now)
		if (tc.reason == "") != (got == "") || !strings.Contains(got, tc.reason) {
			t.Errorf("%s: why = %q, want %q", tc.name, got, tc.reason)
		}
	}
}

// toastRunner is a runner on the fake companion with toasts on, greeted.
func toastRunner(t *testing.T, companion *fakeCompanion, cfg config.ToastsConfig) *runner {
	t.Helper()
	r := newTestRunner(t, companion)
	r.toasts = newToaster(cfg)
	r.greet(context.Background())
	return r
}

func plainToasts() config.ToastsConfig {
	return config.ToastsConfig{Mode: "plain", MaxPerHour: 6, Technologies: techs}
}

// queue puts one milestone in the runner's feed as the tailer would.
func queue(t *testing.T, r *runner, line string) {
	t.Helper()
	r.toasts.feed.caughtUp()
	if err := ingestLine(r.store, r.toasts.feed, techs, []byte(line)); err != nil {
		t.Fatal(err)
	}
}

const firstRocket = `{"event":"rocket_launched","tick":1000,"data":{"force":"team-1","surface":"nauvis"}}`

// One toast a tick, after the page, as a one-line summary.
func TestTheTickSaysOneToast(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick = 1000
	r := toastRunner(t, companion, plainToasts())
	queue(t, r, firstRocket)
	queue(t, r, `{"event":"rocket_launched","tick":1001,"data":{"force":"team-2","surface":"nauvis"}}`)
	r.tick(context.Background())
	if len(companion.says) != 1 || !strings.Contains(companion.says[0], `"lines":["team-1 launched its first rocket."]`) || !strings.Contains(companion.says[0], `"shape":"summary"`) {
		t.Fatalf("says after one tick: %v", companion.says)
	}
	if strings.Contains(companion.says[0], "to_asker") || strings.Contains(companion.says[0], "session") {
		t.Errorf("a toast carries no asker and no session: %s", companion.says[0])
	}
	r.tick(context.Background())
	if len(companion.says) != 2 {
		t.Errorf("says after two ticks: %v", companion.says)
	}
	if !strings.Contains(log.String(), "toast: said first_rocket: team-1 launched its first rocket.") {
		t.Errorf("log:\n%s", log)
	}
}

func TestNoToastWithNobodyOnline(t *testing.T) {
	companion := newFakeCompanion(nil)
	companion.gameTick = 1000
	zero := 0
	companion.players = &zero
	r := toastRunner(t, companion, plainToasts())
	queue(t, r, firstRocket)
	r.tick(context.Background())
	if len(companion.says) != 0 {
		t.Errorf("said %v with nobody online", companion.says)
	}
}

// bad_op stops the toasts until the next reconnection, logged once.
func TestSayBadOpLatchesUntilReconnection(t *testing.T) {
	log := captureLog(t)
	companion := newFakeCompanion(nil)
	companion.gameTick, companion.conn = 1000, 1
	companion.sayErr = "bad_op"
	r := toastRunner(t, companion, plainToasts())
	ctx := context.Background()
	queue(t, r, firstRocket)
	r.tick(ctx)
	queue(t, r, `{"event":"rocket_launched","tick":1001,"data":{"force":"team-2","surface":"nauvis"}}`)
	r.tick(ctx)
	if n := strings.Count(log.String(), "does not take say (an older companion"); n != 1 {
		t.Errorf("the unsupported line was logged %d times, want once:\n%s", n, log)
	}
	if !r.toasts.unsupported {
		t.Fatal("not latched")
	}
	// A restart: the next reply over a new connection starts again, and a
	// companion that now takes say gets the next toast.
	companion.sayErr = ""
	companion.restart(1000, nil)
	r.tick(ctx) // a poll over the new connection: lost
	r.tick(ctx) // the look that starts again
	if r.toasts.unsupported {
		t.Error("still latched after a reconnection")
	}
	queue(t, r, `{"event":"rocket_launched","tick":1002,"data":{"force":"team-3","surface":"nauvis"}}`)
	r.tick(ctx)
	if len(companion.says) != 1 {
		t.Errorf("says after the reconnection: %v", companion.says)
	}
}

// An outage drops what was queued: after a restart the game may be on
// another save.
func TestAnOutageDropsTheQueuedToasts(t *testing.T) {
	companion := newFakeCompanion(nil)
	companion.gameTick = 1000
	r := toastRunner(t, companion, plainToasts())
	ctx := context.Background()
	companion.crash()
	r.tick(ctx)
	queue(t, r, firstRocket)
	companion.restart(1000, nil)
	r.tick(ctx)
	r.tick(ctx)
	if len(companion.says) != 0 {
		t.Errorf("a toast queued before the outage was said after it: %v", companion.says)
	}
}

// A toast for an event from another epoch than the one history reads when
// the tick sends it is dropped.
func TestAToastFromAnEarlierMapIsDropped(t *testing.T) {
	companion := newFakeCompanion(nil)
	companion.gameTick = 1000
	r := toastRunner(t, companion, plainToasts())
	queue(t, r, firstRocket)
	queue(t, r, `{"event":"session","tick":1,"data":{"map_id":"OTHER"}}`)
	r.tick(context.Background())
	if len(companion.says) != 0 {
		t.Errorf("said %v for the map before", companion.says)
	}
}

func TestToastsOffMakesNoToaster(t *testing.T) {
	if newToaster(config.ToastsConfig{Mode: "off"}) != nil {
		t.Error("mode off built a toaster")
	}
}

// Model mode: the model writes the line, and the plain line goes out when
// it gives nothing usable. The fake model echoes, which is a line.
func TestModelModeSaysTheModelsLine(t *testing.T) {
	companion := newFakeCompanion(nil)
	companion.gameTick = 1000
	cfg := plainToasts()
	cfg.Mode = "model"
	r := toastRunner(t, companion, cfg)
	queue(t, r, firstRocket)
	r.tick(context.Background())
	if len(companion.says) != 1 || strings.Contains(companion.says[0], `"lines":["team-1 launched its first rocket."]`) {
		t.Errorf("says %v, want the model's own line", companion.says)
	}
}

// The whole path: a line appended to the events file reaches the game as
// exactly one say, and a runner started again on the same file says
// nothing, since the tailer starts at the file's end.
func TestAnEventLineMakesOneSayAndARestartReplaysNone(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(events, []byte(`{"event":"session","tick":1,"data":{"map_id":"M1"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	historyPath := filepath.Join(dir, "history.sqlite")

	start := func(companion *fakeCompanion) (*runner, context.CancelFunc) {
		store, err := history.Open(historyPath)
		if err != nil {
			t.Fatal(err)
		}
		r := newTestRunner(t, companion)
		r.store = store
		r.cfg.Factorio.EventsFile = events
		r.cfg.PollInterval = config.Duration(5 * time.Millisecond)
		r.cfg.Toasts = plainToasts()
		r.toasts = newToaster(r.cfg.Toasts)
		ctx, cancel := context.WithCancel(context.Background())
		r.greet(ctx)
		done := make(chan struct{})
		go func() { tailEvents(ctx, r.cfg, store, r.toasts.feed); close(done) }()
		stop := func() { cancel(); <-done; _ = store.Close() }
		for !r.toasts.feed.isLive() {
			time.Sleep(time.Millisecond)
		}
		return r, stop
	}
	tickFor := func(r *runner, d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			r.tick(context.Background())
		}
	}

	companion := newFakeCompanion(nil)
	companion.gameTick, companion.mapID = 2000, "M1"
	r, stop := start(companion)
	f, _ := os.OpenFile(events, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"event":"rocket_launched","tick":2000,"data":{"force":"player","surface":"nauvis"}}` + "\n")
	f.Close()
	for end := time.Now().Add(3 * time.Second); len(companion.says) == 0 && time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		r.tick(context.Background())
	}
	tickFor(r, 100*time.Millisecond)
	stop()
	if len(companion.says) != 1 || !strings.Contains(companion.says[0], "player launched its first rocket.") {
		t.Fatalf("says %v, want exactly the one first-rocket toast", companion.says)
	}

	again := newFakeCompanion(nil)
	again.gameTick, again.mapID = 2100, "M1"
	r, stop = start(again)
	tickFor(r, 150*time.Millisecond)
	stop()
	if len(again.says) != 0 {
		t.Errorf("a restarted runner said %v from the same file", again.says)
	}
}
