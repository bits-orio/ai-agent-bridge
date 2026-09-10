// Per-player quota: how many questions one asker may put to the model in a
// rolling hour. Over quota the loop answers with a notice and never calls the
// model, so a player holding down a macro costs nothing but RCON traffic.

package agent

import (
	"sync"
	"time"
)

const quotaWindow = time.Hour

type quota struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	asked  map[string][]time.Time
}

func newQuota(limit int) *quota {
	return &quota{limit: limit, window: quotaWindow, asked: map[string][]time.Time{}}
}

// take records one question against key and reports whether it is allowed. A
// limit of zero or less means no quota at all.
func (q *quota) take(key string, now time.Time) bool {
	if q.limit <= 0 {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	kept := q.asked[key][:0:0]
	for _, at := range q.asked[key] {
		if now.Sub(at) < q.window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= q.limit {
		q.asked[key] = kept
		return false
	}
	q.asked[key] = append(kept, now)
	return true
}
