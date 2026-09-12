package rpc

import (
	"testing"
	"time"
)

// The floor is the fastest recent trip: a slow one does not raise it, and it
// forgets trips that have fallen off the ring, so a network that speeds up
// is noticed.
func TestLatencyFloor(t *testing.T) {
	var l latency
	if l.floor() != 0 {
		t.Fatalf("floor before any trip = %v, want 0", l.floor())
	}
	l.add(90 * time.Millisecond)
	l.add(400 * time.Millisecond)
	l.add(110 * time.Millisecond)
	if got := l.floor(); got != 90*time.Millisecond {
		t.Errorf("floor = %v, want 90ms", got)
	}
	for i := 0; i < len(l.ring); i++ {
		l.add(30 * time.Millisecond)
	}
	if got := l.floor(); got != 30*time.Millisecond {
		t.Errorf("floor after the ring turned over = %v, want 30ms", got)
	}
}
