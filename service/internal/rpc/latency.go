// The RCON round trip, watched so a tool's time on the game thread can be
// told from the wire: the floor of recent trips is what the network costs,
// and a call that takes much longer than that spent the rest in the game.

package rpc

import (
	"sync"
	"time"
)

// latency keeps the last few successful round trips and reports their
// minimum. Failed trips are not recorded: a refused connection returns in
// microseconds and would make every later call look slow.
type latency struct {
	mu   sync.Mutex
	ring [32]time.Duration
	n    int // filled slots, up to len(ring)
	next int
}

func (l *latency) add(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = d
	l.next = (l.next + 1) % len(l.ring)
	if l.n < len(l.ring) {
		l.n++
	}
}

// floor is the fastest recent round trip, zero before any.
func (l *latency) floor() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	var min time.Duration
	for i := 0; i < l.n; i++ {
		if i == 0 || l.ring[i] < min {
			min = l.ring[i]
		}
	}
	return min
}
