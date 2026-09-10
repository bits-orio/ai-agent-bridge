package main

import (
	"fmt"
	"testing"
	"time"
)

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
