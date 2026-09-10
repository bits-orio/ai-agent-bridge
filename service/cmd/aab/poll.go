// One tick: poll a page of questions, answer what is new, deliver what is
// waiting, and move the cursor past what is finished with.

package main

import (
	"context"
	"log"
	"slices"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

// pollLimit is how many questions one poll asks for at most. The companion
// serves the oldest unanswered ones, so a page at a time is all the service ever
// needs and a backlog can never encode past the companion's reply cap.
const pollLimit = 16

func (r *runner) tick(ctx context.Context) {
	limit := r.pageSize()
	questions, err := r.rpc.Poll(ctx, r.after, limit)
	if err != nil {
		if rpc.HasCode(err, rpc.CodeTooLarge) && limit > 1 {
			r.shrinkPage(limit)
			return
		}
		r.stats.SetConnected(false)
		r.pollFailed(err)
		return
	}
	r.growPage(limit)
	r.stats.SetConnected(true)
	r.pollRecovered()
	r.forgetGone(questions, limit)
	r.answerPage(ctx, questions)
	r.advanceWatermark(questions)
}

// pageSize is the poll page in use, pollLimit until a reply proves too large.
func (r *runner) pageSize() int {
	if r.page <= 0 {
		return pollLimit
	}
	return r.page
}

// shrinkPage halves a page the companion refused. Sixteen long questions can
// outgrow its reply cap, and the whole reply then comes back as one too_large
// error, so the page is what the service brings down until a reply fits.
func (r *runner) shrinkPage(refused int) {
	r.page = refused / 2
	log.Printf("poll reply too large at %d questions, trying %d", refused, r.page)
}

// growPage doubles the page back toward pollLimit after a poll that landed.
// Growing only on a page that came back short read as the same signal and was
// not: at a page of one, a single pending question fills the page, so the page
// stayed at one for as long as anything was waiting (second review-fix contract
// 4). One refused poll every other tick while a backlog of long questions drains
// is what probing upward costs.
func (r *runner) growPage(landed int) {
	if landed >= pollLimit {
		r.page = pollLimit
		return
	}
	r.page = min(landed*2, pollLimit)
}

// advanceWatermark moves the poll cursor up to the last id below which every
// question on this page is finished with, and forgets those questions: the next
// poll asks for what comes after them. Everything the cursor passes leaves the
// page for good, which is what keeps a question the service gave up on from
// occupying a slot of every later page until the service was answering nothing at
// all (second review-fix contract 3).
//
// It stops at the lowest id still waiting. A delivery that failed on the wire is
// retried next tick, and moving past it would throw away an answer the operator
// has already paid for. The ids are sorted here rather than taken in the order the
// reply happened to list them, so the cursor cannot step over a question the page
// put out of order.
func (r *runner) advanceWatermark(offered rpc.PollReply) {
	ids := make([]int64, 0, len(offered))
	for _, q := range offered {
		ids = append(ids, q.ID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		state := r.inFlight[id]
		if state == nil || !state.done {
			return
		}
		if id > r.after {
			r.after = id
		}
		delete(r.inFlight, id)
	}
}

// forgetGone drops what the service remembers about questions the companion no
// longer offers: answered and rendered, or aged out of its ring. A full page may
// be hiding newer questions, so an id above it is kept until the service has
// been shown everything pending.
func (r *runner) forgetGone(offered rpc.PollReply, limit int) {
	if len(r.inFlight) == 0 {
		return
	}
	live := make(map[int64]bool, len(offered))
	var highest int64
	for _, q := range offered {
		live[q.ID] = true
		if q.ID > highest {
			highest = q.ID
		}
	}
	full := len(offered) >= limit
	for id := range r.inFlight {
		if live[id] || (full && id > highest) {
			continue
		}
		delete(r.inFlight, id)
	}
}
