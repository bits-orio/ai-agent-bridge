package rcon

import (
	"strings"
	"testing"
)

// The happy path: one dial, one password handshake, the reply the server sent.
func TestExecuteRoundTrip(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	if got := mustExecute(t, c, "/aab-rpc {}"); got != "/aab-rpc {}" {
		t.Errorf("reply = %q, want the command echoed back", got)
	}
	if got := server.auths.Load(); got != 1 {
		t.Errorf("auth count = %d, want 1", got)
	}
}

// One connection serves many commands: a second Execute must not re-authenticate.
func TestExecuteReusesTheConnection(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	mustExecute(t, c, "one")
	mustExecute(t, c, "two")
	if got := server.auths.Load(); got != 1 {
		t.Errorf("auth count = %d, want 1: the client re-dialled when it did not need to", got)
	}
}

// A wrong password is reported as such, not as a transport error, and the
// client does not keep the useless socket.
func TestAuthFailure(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "wrong")
	defer c.Close()

	_, err := c.Execute("help")
	wantErr(t, err, ErrAuthFailed)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		t.Error("the client kept a connection it failed to authenticate on")
	}
}

// An empty command is refused locally, before anything is dialled.
func TestExecuteEmptyCommandRefusedWithoutDialling(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	_, err := c.Execute("")
	wantErr(t, err, ErrCommandEmpty)
	if got := server.auths.Load(); got != 0 {
		t.Errorf("auth count = %d, want 0: an empty command must not reach the server", got)
	}
}

// A command over MaxCommandLen fails that one call without tearing down an
// otherwise healthy connection. Regression test for treating every Execute
// error as connection-dead.
func TestExecuteCommandTooLongDoesNotRedial(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	mustExecute(t, c, "help")
	if got := server.auths.Load(); got != 1 {
		t.Fatalf("auth count after the first execute = %d, want 1", got)
	}

	_, err := c.Execute(strings.Repeat("x", MaxCommandLen+1))
	wantErr(t, err, ErrCommandTooLong)
	if got := server.auths.Load(); got != 1 {
		t.Errorf("auth count after the oversized command = %d, want still 1", got)
	}

	// The connection must still be usable: a reconnect bug would have dropped
	// it and this call would dial again.
	mustExecute(t, c, "help")
	if got := server.auths.Load(); got != 1 {
		t.Errorf("auth count after the follow-up execute = %d, want still 1", got)
	}
}

// A genuine connection failure does reconnect, once.
func TestExecuteRedialsAfterADroppedConnection(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	mustExecute(t, c, "help")
	server.DropConnections()

	mustExecute(t, c, "help")
	if got := server.auths.Load(); got != 2 {
		t.Errorf("auth count after the drop = %d, want 2", got)
	}
}

// The whole point of dropping gorcon: a 100 KB command and a 100 KB reply both
// survive the round trip byte-exact. gorcon refused anything over 1000 bytes
// out and 4 KB back; Factorio itself takes a megabyte in and returns 4 MB
// (TESTING.md check 1.5).
func TestExecuteCarriesA100KBCommandAndReply(t *testing.T) {
	const size = 100 * 1024
	body := strings.Repeat("aab", size/3+1)[:size]

	server := newFakeServer(t, "pw", func(cmd string) string {
		if cmd != "/aab-rpc "+body {
			t.Errorf("server received %d bytes, want the %d-byte command intact", len(cmd), len(body)+9)
		}
		return body
	})
	c := New(server.Addr(), "pw")
	defer c.Close()

	got := mustExecute(t, c, "/aab-rpc "+body)
	if len(got) != size {
		t.Fatalf("reply is %d bytes, want %d", len(got), size)
	}
	if got != body {
		t.Error("reply came back changed")
	}
}

// A reply whose length prefix is absurd is refused rather than allocated for.
func TestReadPacketRefusesAnAbsurdLength(t *testing.T) {
	framed := []byte{0xff, 0xff, 0xff, 0x7f} // 2 GB, claimed
	if _, err := readPacket(strings.NewReader(string(framed))); err == nil {
		t.Fatal("expected an oversized reply length to be refused")
	}
}

// Ids are echoed back by the server, so they must never collide with the
// protocol's "password refused" sentinel.
func TestNextIDSkipsTheAuthFailureSentinel(t *testing.T) {
	c := New("127.0.0.1:0", "pw")
	if got := c.nextID(); got != 1 {
		t.Errorf("first id = %d, want 1", got)
	}
	c.lastID = 2147483645
	for range 4 {
		if got := c.nextID(); got == authFailedID || got == 0 {
			t.Fatalf("id %d collides with a reserved value", got)
		}
	}
}
