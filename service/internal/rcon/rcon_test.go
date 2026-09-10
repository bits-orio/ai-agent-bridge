// Copied from open-discord-bridge/bridge/internal/rcon/rcon_test.go, unchanged.

package rcon

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	gorcon "github.com/gorcon/rcon"
	"github.com/gorcon/rcon/rcontest"
)

// TestIsLocalValidationErr checks that gorcon's client-side validation errors (raised
// before any bytes touch the socket) are classified separately from errors that
// indicate the connection itself is broken.
func TestIsLocalValidationErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"command too long", gorcon.ErrCommandTooLong, true},
		{"command empty", gorcon.ErrCommandEmpty, true},
		{"wrapped command too long", errors.New("rcon: " + gorcon.ErrCommandTooLong.Error()), false}, // not the same error, just similar text
		{"auth failed", gorcon.ErrAuthFailed, false},
		{"invalid packet id", gorcon.ErrInvalidPacketID, false},
		{"generic io error", errors.New("read tcp 127.0.0.1:1234: connection reset by peer"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalValidationErr(tt.err); got != tt.want {
				t.Errorf("isLocalValidationErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestExecute_CommandTooLong_DoesNotRedial verifies that sending a command over
// MaxCommandLen fails that single call without tearing down and re-dialing an otherwise
// healthy connection. Regression test for treating every Execute error as connection-dead.
func TestExecute_CommandTooLong_DoesNotRedial(t *testing.T) {
	var authCount int32
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{Password: "pw"}))
	defer server.Close()
	server.SetAuthHandler(func(c *rcontest.Context) {
		atomic.AddInt32(&authCount, 1)
		rcontest.AuthHandler(c)
	})

	c := New(server.Addr(), "pw")
	defer c.Close()

	if _, err := c.Execute("help"); err != nil {
		t.Fatalf("initial execute: %v", err)
	}
	if got := atomic.LoadInt32(&authCount); got != 1 {
		t.Fatalf("auth count after first execute = %d, want 1", got)
	}

	oversized := strings.Repeat("x", MaxCommandLen+1)
	if _, err := c.Execute(oversized); !errors.Is(err, gorcon.ErrCommandTooLong) {
		t.Fatalf("execute oversized command: got err %v, want ErrCommandTooLong", err)
	}
	if got := atomic.LoadInt32(&authCount); got != 1 {
		t.Errorf("auth count after oversized execute = %d, want still 1 (should not re-dial)", got)
	}

	// The connection must still be usable afterward: a real reconnect bug would have
	// nil'd out c.conn, and this call would either fail or trigger a fresh dial.
	if _, err := c.Execute("help"); err != nil {
		t.Fatalf("execute after oversized command: %v", err)
	}
	if got := atomic.LoadInt32(&authCount); got != 1 {
		t.Errorf("auth count after follow-up execute = %d, want still 1", got)
	}
}

// TestExecute_ConnectionError_Redials verifies the reconnect path still works for a
// genuine connection failure (as opposed to the client-side validation error above).
func TestExecute_ConnectionError_Redials(t *testing.T) {
	var authCount int32
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{Password: "pw"}))
	defer server.Close()
	server.SetAuthHandler(func(c *rcontest.Context) {
		atomic.AddInt32(&authCount, 1)
		rcontest.AuthHandler(c)
	})

	c := New(server.Addr(), "pw")
	defer c.Close()

	if _, err := c.Execute("help"); err != nil {
		t.Fatalf("initial execute: %v", err)
	}

	// Simulate a dropped connection (e.g. game restart) by closing the client's socket
	// out from under it, then confirm the next Execute re-dials and succeeds.
	c.mu.Lock()
	c.conn.Close()
	c.mu.Unlock()

	if _, err := c.Execute("help"); err != nil {
		t.Fatalf("execute after dropped connection: %v", err)
	}
	if got := atomic.LoadInt32(&authCount); got != 2 {
		t.Errorf("auth count after dropped connection = %d, want 2 (should re-dial)", got)
	}
}
