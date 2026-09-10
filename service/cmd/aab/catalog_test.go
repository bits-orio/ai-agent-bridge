package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// One provider whose manifest never arrives costs itself its tools and nobody
// else theirs (second review-fix contract 1).
func TestCatalogSurvivesOneFailingManifest(t *testing.T) {
	companion := newFakeCompanion(nil)
	companion.catalog = map[string][]string{
		"ai-agent-bridge-tools": {"list_forces", "list_players"},
		"too-verbose-mod":       {"standings"},
	}
	companion.manifestErr = map[string]string{"too-verbose-mod": rpc.CodeTooLarge}
	written := captureLog(t)
	r := newTestRunner(t, companion)

	game, ready := r.toolsFor(context.Background())
	if !ready {
		t.Fatal("no catalog, want the providers that did answer")
	}
	for _, fn := range []string{"list_forces", "list_players"} {
		if !hasToolNamed(game, fn) {
			t.Errorf("%s is missing from the catalog: %v", fn, toolNames(game))
		}
	}
	if hasToolNamed(game, "standings") {
		t.Errorf("the provider whose manifest failed kept its tools: %v", toolNames(game))
	}
	if companion.manifests != 2 {
		t.Errorf("manifest calls = %d, want one per provider", companion.manifests)
	}
	if !strings.Contains(written.String(), "skipping provider too-verbose-mod") {
		t.Errorf("the skipped provider was not named in the log:\n%s", written.String())
	}
}

// With no catalog at all the question waits. Answering it would charge the
// operator for a confident answer containing no game data (second review-fix
// contract 2).
func TestNoCatalogLeavesTheQuestionPending(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.providersErr = rpc.CodeTooLarge
	written := captureLog(t)
	r := newTestRunner(t, companion)
	ctx := context.Background()

	r.tick(ctx)
	if r.answered != 0 || companion.answers != 0 {
		t.Fatalf("ran the model %d time(s) and delivered %d answer(s) with no catalog", r.answered, companion.answers)
	}
	if len(companion.pending) != 1 {
		t.Errorf("the question left the companion: %v", companion.pending)
	}
	if r.after != 0 {
		t.Errorf("poll cursor = %d, want 0: the question was never answered", r.after)
	}
	if !strings.Contains(written.String(), "no catalog available") {
		t.Errorf("the log does not say why nothing happened:\n%s", written.String())
	}

	// The same question is answered as soon as a catalog arrives.
	companion.providersErr = ""
	r.tick(ctx)
	if companion.answers != 1 {
		t.Errorf("answer calls = %d, want 1 once the catalog arrived", companion.answers)
	}
}

// A catalog already in hand is used while a rebuild keeps failing: stale tools
// beat no tools, and the question is answered rather than held.
func TestStaleCatalogIsStillUsed(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	written := captureLog(t)
	r := newTestRunner(t, companion)
	ctx := context.Background()

	if _, ready := r.toolsFor(ctx); !ready {
		t.Fatal("the first catalog read failed")
	}
	companion.providersErr = rpc.CodeTooLarge
	r.caller.stale.Store(true)

	r.tick(ctx)
	if companion.answers != 1 {
		t.Errorf("answer calls = %d, want 1: a stale catalog is still a catalog", companion.answers)
	}
	if !strings.Contains(written.String(), "keeping the last one") {
		t.Errorf("the log does not say the old catalog was kept:\n%s", written.String())
	}
}

// A companion older than the providers op still yields a catalog: the whole thing
// in one tools reply, which is what a small server gets anyway.
func TestCatalogFallsBackToTheOneReplyToolsOp(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.providersErr = rpc.CodeBadOp
	r := newTestRunner(t, companion)

	game, ready := r.toolsFor(context.Background())
	if !ready {
		t.Fatal("no catalog from the tools op")
	}
	if !hasToolNamed(game, "list_forces") {
		t.Errorf("the tools op catalog is missing its tool: %v", toolNames(game))
	}
	if companion.manifests != 0 {
		t.Errorf("manifest calls = %d, want 0: that companion has no manifest op", companion.manifests)
	}
}

// Neither op answering is the no-catalog case, however it is reached.
func TestNeitherCatalogOpLeavesNoCatalog(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.providersErr = rpc.CodeBadOp
	companion.toolsErr = rpc.CodeTooLarge
	written := captureLog(t)
	r := newTestRunner(t, companion)

	if _, ready := r.toolsFor(context.Background()); ready {
		t.Fatal("a catalog appeared out of two failed reads")
	}
	if !strings.Contains(written.String(), "no catalog available") {
		t.Errorf("the log does not say there is no catalog:\n%s", written.String())
	}
}

// Every provider's manifest failing is the same as having no catalog: there is
// nothing to answer a question with.
func TestEveryManifestFailingLeavesNoCatalog(t *testing.T) {
	companion := newFakeCompanion(map[int64]string{1: "ping"})
	companion.manifestErr = map[string]string{"ai-agent-bridge-tools": rpc.CodeTooLarge}
	written := captureLog(t)
	r := newTestRunner(t, companion)

	if _, ready := r.toolsFor(context.Background()); ready {
		t.Fatal("a catalog appeared with no manifest in it")
	}
	if !strings.Contains(written.String(), "no catalog available") {
		t.Errorf("the log does not say there is no catalog:\n%s", written.String())
	}
}
