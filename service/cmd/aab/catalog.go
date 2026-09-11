// Reading the catalog: the providers list, then one manifest per provider, plus
// the history tools the service owns itself.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/arith"
	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// catalogTTL is how long a catalog snapshot is reused. A provider added or
// removed mid-session is picked up either at the next refresh or the first
// time a call reports an unknown provider, whichever comes first.
const catalogTTL = 10 * time.Minute

// toolsFor returns the catalog to run a question against, rebuilding it when a
// provider went missing or the snapshot is old (CONTEXT.md invariant 3). The
// second result says whether there is a catalog at all.
func (r *runner) toolsFor(ctx context.Context) ([]tools.Tool, bool) {
	if r.tools == nil || r.caller.stale.Load() || time.Since(r.builtAt) > catalogTTL {
		r.rebuild(ctx)
	}
	return r.tools, r.tools != nil
}

// rebuild reads the catalog in two steps: the providers list, then one manifest
// per provider (second review-fix contract 1). One provider whose manifest does
// not arrive loses its own tools and nobody else's, where one oversized reply
// used to cost every mod on the server every tool it had.
func (r *runner) rebuild(ctx context.Context) {
	providers, found, err := r.rpc.Catalog(ctx)
	switch {
	case rpc.HasCode(err, rpc.CodeBadOp):
		r.rebuildFromOneReply(ctx)
		return
	case err != nil:
		r.cannotRead(fmt.Sprintf("the providers list did not arrive: %v", err))
		return
	case found > 0 && len(providers) == 0:
		r.cannotRead(fmt.Sprintf("no manifest arrived from any of the %d provider(s)", found))
		return
	}
	r.install(providers)
}

// rebuildFromOneReply is the fallback for a companion older than the providers
// op: the whole catalog in one tools reply, which is what small servers get
// anyway.
func (r *runner) rebuildFromOneReply(ctx context.Context) {
	providers, err := r.rpc.Tools(ctx)
	if err != nil {
		r.cannotRead(fmt.Sprintf("this companion has no providers op and its tools op failed: %v", err))
		return
	}
	log.Print("catalog: this companion has no providers op, reading the whole catalog in one reply")
	r.install(providers)
}

// install swaps in a fresh snapshot: the game tools the providers offer, then the
// history tools the service serves out of its own store.
func (r *runner) install(providers rpc.ToolsReply) {
	game := catalog.Build(providers, r.caller).Tools()
	stored := r.store.Tools()

	fresh := make([]tools.Tool, 0, len(game)+len(stored))
	fresh = append(fresh, game...)
	fresh = append(fresh, stored...)
	fresh = append(fresh, arith.Tools()...)

	r.tools = fresh
	r.builtAt = time.Now()
	r.caller.stale.Store(false)
	r.catalogWaiting = false
	log.Printf("catalog: %d tool(s) from %d provider(s), %d history tool(s), %d ranking tool(s)", len(game), len(providers), len(stored), len(arith.Tools()))
}

// cannotRead reports a rebuild that did not happen. A snapshot already in hand is
// kept and used, stale and all, which is the point of a snapshot. With none, the
// line says so in those words, because the question waits for it.
func (r *runner) cannotRead(reason string) {
	if r.tools != nil {
		log.Printf("catalog: %s, keeping the last one", reason)
		return
	}
	log.Printf("catalog: no catalog available: %s", reason)
}

// waitForCatalog leaves a question pending. With no catalog the model has nothing
// but the prompt to answer from, and a confident answer carrying no game data is
// worse than a late one (second review-fix contract 2). One line per streak, not
// one per question per tick.
func (r *runner) waitForCatalog(id int64) {
	if r.catalogWaiting {
		return
	}
	r.catalogWaiting = true
	log.Printf("question %d: no catalog available, leaving it pending and trying again next tick", id)
}

// toolCaller is the rpc client with a flag on it: a call that reports an
// unknown provider or an unknown tool means the catalog no longer matches the
// server, so the next tick rebuilds it.
type toolCaller struct {
	client *rpc.Client
	stale  atomic.Bool
}

// slowTool is the RCON round trip past which a tool call is logged: the
// companion runs tools on the game thread, so a slow one is a stutter every
// player felt.
const slowTool = 100 * time.Millisecond

func (t *toolCaller) CallTool(ctx context.Context, iface, fn string, args any) (json.RawMessage, error) {
	started := time.Now()
	out, err := t.client.CallTool(ctx, iface, fn, args)
	if took := time.Since(started); took > slowTool {
		log.Printf("tool %s.%s took %s on the game thread; players felt that", iface, fn, took.Round(time.Millisecond))
	}
	if rpc.HasCode(err, rpc.CodeNoProvider) || rpc.HasCode(err, rpc.CodeNoTool) {
		t.stale.Store(true)
	}
	return out, err
}
