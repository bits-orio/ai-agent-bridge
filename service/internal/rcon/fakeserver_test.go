package rcon

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeServer is a Source RCON server with just enough of the protocol to test
// the client against: one password, one handler per command, and a count of how
// many times a client has authenticated, which is how the tests tell a reused
// connection from a fresh dial.
type fakeServer struct {
	t        *testing.T
	ln       net.Listener
	password string
	handler  func(cmd string) string

	auths atomic.Int32

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// newFakeServer starts a listener on a loopback port of the system's choosing.
func newFakeServer(t *testing.T, password string, handler func(cmd string) string) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{t: t, ln: ln, password: password, handler: handler}
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

func (s *fakeServer) Addr() string { return s.ln.Addr().String() }

func (s *fakeServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.ln.Close()
	for _, conn := range s.conns {
		conn.Close()
	}
}

// DropConnections closes every open session without closing the listener, which
// is what a game restart looks like to the client.
func (s *fakeServer) DropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
}

func (s *fakeServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		stop := s.closed
		if !stop {
			s.conns = append(s.conns, conn)
		}
		s.mu.Unlock()
		if stop {
			conn.Close()
			return
		}
		go s.serve(conn)
	}
}

func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	authed := false
	for {
		req, err := readPacket(conn)
		if err != nil {
			return // the client hung up, or the test closed the socket
		}
		switch req.Type {
		case typeAuth:
			s.auths.Add(1)
			reply := packet{ID: req.ID, Type: typeExec}
			if req.Body != s.password {
				reply.ID = authFailedID
			} else {
				authed = true
			}
			if err := writePacket(conn, reply); err != nil {
				return
			}
			if !authed {
				return
			}
		case typeExec:
			body := ""
			if s.handler != nil {
				body = s.handler(req.Body)
			}
			if err := writePacket(conn, packet{ID: req.ID, Type: typeResponse, Body: body}); err != nil {
				return
			}
		default:
			return
		}
	}
}

// echo is the handler most tests want: the command back, unchanged.
func echo(cmd string) string { return cmd }

// mustExecute fails the test if a command does not come back cleanly.
func mustExecute(t *testing.T, c *Client, cmd string) string {
	t.Helper()
	resp, err := c.Execute(cmd)
	if err != nil {
		t.Fatalf("execute %q: %v", truncateForMessage(cmd), err)
	}
	return resp
}

func truncateForMessage(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// wantErr fails the test unless err is target.
func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}
