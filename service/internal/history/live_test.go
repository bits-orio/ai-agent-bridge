package history

// The live server's history, migrated. Set AAB_LIVE_HISTORY to a copy of the
// file, never the one a service is writing: the test copies it again and
// migrates the copy. Skipped otherwise. AAB_LIVE_HISTORY_CURRENT_DAY, when
// set, is the day (2026-10-09) the current map's first row must carry.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/chatscope"
)

// legacyEpochs recomputes the tick rule in SQL alone, for a file with no
// session lines, and counts the rows the migration placed differently.
const legacyEpochs = `WITH x AS (SELECT id, tick, LAG(tick) OVER (ORDER BY id) AS prev FROM events),
	b AS (SELECT id, CASE WHEN prev IS NOT NULL AND tick < prev AND (tick < ? OR prev - tick > ?) THEN 1 ELSE 0 END AS brk FROM x),
	e AS (SELECT id, 1 + SUM(brk) OVER (ORDER BY id) AS epoch FROM b)
	SELECT COUNT(*) FROM events JOIN e ON e.id = events.id WHERE events.epoch IS NOT e.epoch`

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatalf("open %s: %v", from, err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatalf("create %s: %v", to, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("copy: %v", err)
	}
}

func TestLiveHistoryCopyMigrates(t *testing.T) {
	src := os.Getenv("AAB_LIVE_HISTORY")
	if src == "" {
		t.Skip("AAB_LIVE_HISTORY is not set")
	}
	path := filepath.Join(t.TempDir(), "history.sqlite")
	copyFile(t, src, path)

	start := time.Now()
	s := mustOpen(t, path)
	opened := time.Since(start)
	defer s.Close()

	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	if n := scalar("SELECT COUNT(*) FROM events WHERE epoch IS NULL OR private IS NULL"); n != 0 {
		t.Fatalf("%d rows were left unplaced or unclassified", n)
	}
	if scalar("SELECT COUNT(*) FROM events WHERE event = ?", sessionEvent) == 0 {
		if n := scalar(legacyEpochs, newMapTicks, rollbackTicks); n != 0 {
			t.Errorf("%d rows sit in another epoch than the tick rule computed in SQL puts them", n)
		}
	}

	rows := scalar("SELECT COUNT(*) FROM events")
	epochs := scalar("SELECT COUNT(DISTINCT epoch) FROM events")
	current := scalar("SELECT epoch FROM events ORDER BY id DESC LIMIT 1")
	var first, last, firstAt, lastAt string
	var currentRows, maxTick int64
	if err := s.db.QueryRow(`SELECT MIN(id), MAX(id), MIN(received_at), MAX(received_at), COUNT(*), MAX(tick)
		FROM events WHERE epoch = ?`, current).Scan(&first, &last, &firstAt, &lastAt, &currentRows, &maxTick); err != nil {
		t.Fatalf("current epoch: %v", err)
	}
	madePublic := scalar(`SELECT COUNT(*) FROM events WHERE event = 'console_chat' AND force <> ''
		AND private = 0 AND json_type(data, '$.private') IS NULL`)
	legacyChatRows := scalar(`SELECT COUNT(*) FROM events WHERE event = 'console_chat' AND force <> ''
		AND json_type(data, '$.private') IS NULL`)
	t.Logf("%d rows in %d epochs, migrated in %s", rows, epochs, opened.Round(time.Millisecond))
	t.Logf("current epoch %d: ids %s..%s, %d rows, received %s to %s, highest tick %d", current, first, last, currentRows, firstAt, lastAt, maxTick)
	t.Logf("one-force rule: %d of %d legacy chat rows made public", madePublic, legacyChatRows)
	for epoch := int64(1); epoch <= epochs; epoch++ {
		var from, to, at string
		var n, forces int64
		if err := s.db.QueryRow(`SELECT MIN(id), MAX(id), MIN(received_at), COUNT(*), COUNT(DISTINCT CASE WHEN force <> '' THEN force END)
			FROM events WHERE epoch = ?`, epoch).Scan(&from, &to, &at, &n, &forces); err != nil {
			t.Fatalf("epoch %d: %v", epoch, err)
		}
		t.Logf("  epoch %d: ids %s..%s, %d rows, %d forces, from %s", epoch, from, to, n, forces, at)
	}
	if day := os.Getenv("AAB_LIVE_HISTORY_CURRENT_DAY"); day != "" && !strings.HasPrefix(firstAt, day) {
		t.Errorf("the current epoch starts on %s, want %s", firstAt, day)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	start = time.Now()
	again := mustOpen(t, path)
	t.Logf("reopened in %s", time.Since(start).Round(time.Millisecond))
	defer again.Close()

	for _, c := range []struct {
		ctx  context.Context
		tool string
		args string
	}{
		{context.Background(), "recent_chat", `{"limit":20}`},
		{scoped("team-4", true), "recent_chat", `{"limit":20}`},
		{context.Background(), "count_events", `{"event":"console_chat"}`},
		{scoped("team-4", true), "count_events", `{"event":"console_chat"}`},
		{context.Background(), "catch_up", `{"since_tick":1}`},
		{context.Background(), "recent_events", `{"limit":20}`},
	} {
		start := time.Now()
		out, err := findTool(t, again.Tools(), c.tool).Call(c.ctx, json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		lines := strings.Count(string(out), "\n")
		t.Logf("%s %s, %s: %d rows in %s", c.tool, c.args, scopeOf(c.ctx), lines, time.Since(start).Round(time.Microsecond))
	}
}

func scopeOf(ctx context.Context) string {
	if key, ok := chatscope.From(ctx).PrivateKey(); ok {
		return "private " + key
	}
	return "public"
}
