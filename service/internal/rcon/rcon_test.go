package rcon

import (
	"errors"
	"strings"
	"sync"
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

	_, _, err := c.Execute("help")
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

	_, _, err := c.Execute("")
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

	_, _, err := c.Execute(strings.Repeat("x", MaxCommandLen+1))
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

// Every connection gets the next number, so a caller can tell a reply that
// came over a new connection from one over the connection before: a server
// restarted between two commands is otherwise invisible, since Execute dials
// again on its own.
func TestConnectionNumbersEveryDial(t *testing.T) {
	server := newFakeServer(t, "pw", echo)
	c := New(server.Addr(), "pw")
	defer c.Close()

	if got := c.Connection(); got != 0 {
		t.Fatalf("Connection before any dial = %d, want 0", got)
	}
	mustExecute(t, c, "one")
	mustExecute(t, c, "two")
	if got := c.Connection(); got != 1 {
		t.Fatalf("Connection after two commands on one connection = %d, want 1", got)
	}
	server.DropConnections()
	mustExecute(t, c, "three")
	if got := c.Connection(); got != 2 {
		t.Errorf("Connection after the redial = %d, want 2", got)
	}

	refused := New(server.Addr(), "wrong")
	defer refused.Close()
	if _, _, err := refused.Execute("help"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if got := refused.Connection(); got != 0 {
		t.Errorf("Connection after a refused password = %d, want 0: no connection was opened", got)
	}
}

// ExecuteOn sends over the connection it names or not at all. After the
// server drops it, the command fails and is not sent again over a new
// connection; once Execute has dialed again, the old number refuses and the
// new one works.
func TestExecuteOnNeverDialsAndNeverRetries(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	server := newFakeServer(t, "pw", func(cmd string) string {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, cmd)
		return cmd
	})
	c := New(server.Addr(), "pw")
	defer c.Close()

	if _, _, err := c.ExecuteOn(0, "before any dial"); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("ExecuteOn before any dial: err = %v, want ErrConnectionChanged", err)
	}
	mustExecute(t, c, "first")
	first := c.Connection()
	if got, _, err := c.ExecuteOn(first, "held"); err != nil || got != "held" {
		t.Fatalf("ExecuteOn on the live connection = %q, %v", got, err)
	}

	server.DropConnections()
	if _, _, err := c.ExecuteOn(first, "after the drop"); err == nil {
		t.Fatal("ExecuteOn over a dropped connection reported success")
	}
	if _, _, err := c.ExecuteOn(first, "again"); !errors.Is(err, ErrConnectionChanged) {
		t.Errorf("ExecuteOn after the failure: err = %v, want ErrConnectionChanged", err)
	}
	if got := server.auths.Load(); got != 1 {
		t.Errorf("auth count = %d, want 1: ExecuteOn dialed", got)
	}

	mustExecute(t, c, "redial")
	if _, _, err := c.ExecuteOn(first, "stale"); !errors.Is(err, ErrConnectionChanged) {
		t.Errorf("ExecuteOn on the old number after a redial: err = %v, want ErrConnectionChanged", err)
	}
	if got, _, err := c.ExecuteOn(c.Connection(), "fresh"); err != nil || got != "fresh" {
		t.Errorf("ExecuteOn on the new number = %q, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, cmd := range seen {
		if cmd == "after the drop" || cmd == "again" || cmd == "stale" || cmd == "before any dial" {
			t.Errorf("the server received %q, which ExecuteOn must never have sent", cmd)
		}
	}
}
