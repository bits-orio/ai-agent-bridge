// Per-asker follow-up context: the last few exchanges, kept for a while, so
// "and the other force?" means something.
//
// It is deliberately thin. Only the question and the one-line form of the
// answer are kept, never tool results, so a follow-up costs a handful of
// tokens and no stale game state is ever presented as current.

package agent

import (
	"sync"
	"time"
)

// maxRemembered is how many exchanges one asker carries forward.
const maxRemembered = 4

type exchange struct {
	question string
	answer   string
	at       time.Time
}

type memory struct {
	mu      sync.Mutex
	ttl     time.Duration
	byAsker map[string][]exchange
}

func newMemory(ttl time.Duration) *memory {
	return &memory{ttl: ttl, byAsker: map[string][]exchange{}}
}

// recall returns what this asker has been told lately, oldest first, dropping
// anything past the TTL.
func (m *memory) recall(key string, now time.Time) []exchange {
	if m.ttl <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.fresh(key, now)
	if len(live) == 0 {
		delete(m.byAsker, key)
		return nil
	}
	m.byAsker[key] = live
	out := make([]exchange, len(live))
	copy(out, live)
	return out
}

// record adds one exchange, keeping only the most recent few.
func (m *memory) record(key, question, answer string, now time.Time) {
	if m.ttl <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live := append(m.fresh(key, now), exchange{question: question, answer: answer, at: now})
	if len(live) > maxRemembered {
		live = live[len(live)-maxRemembered:]
	}
	m.byAsker[key] = live
}

// fresh drops expired exchanges. Callers hold the lock.
func (m *memory) fresh(key string, now time.Time) []exchange {
	kept := m.byAsker[key][:0:0]
	for _, e := range m.byAsker[key] {
		if now.Sub(e.at) < m.ttl {
			kept = append(kept, e)
		}
	}
	return kept
}
