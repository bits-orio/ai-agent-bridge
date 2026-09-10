// Package rcon is the service's Factorio RCON client, written straight to the
// Source RCON protocol in packet.go.
//
// It replaces the gorcon dependency the service started with. That library
// refused any command over 1000 bytes and any reply over 4 KB, both of them its
// own client-side constants: Factorio on 2.0.77 accepted command bodies of
// 1,000,042 bytes and returned 4 MB replies in one packet (TESTING.md check
// 1.5). The 1000-byte ceiling made every table and list artifact undeliverable.
//
// The shape callers see is unchanged: New, Execute, Close, one reconnect on a
// broken connection, and no reconnect when the command itself is the problem.
package rcon

import (
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

// MaxCommandLen is the largest command this client sends. It is a budget the
// service picks, not a limit the game imposes: Factorio accepted a 1,000,042
// byte command on 2.0.77 (TESTING.md check 1.5). 256 KB leaves every answer
// artifact room to spare and still keeps one runaway request from parking
// megabytes on the wire.
const MaxCommandLen = 262144

// maxResponseLen is the largest reply this client will allocate for, so a
// desynced or hostile length prefix cannot make it reserve without limit. Well
// clear of the 4 MB reply measured on the rig.
const maxResponseLen = 16 << 20

const (
	// dialTimeout bounds the TCP connect and the password handshake.
	dialTimeout = 10 * time.Second
	// ioTimeout bounds one command round trip. Generous: a multi-megabyte
	// reply still arrives well inside it, and a server that has stopped
	// answering frees the poll loop instead of parking it forever.
	ioTimeout = 30 * time.Second
)

// Errors a caller may want to tell apart. The two command errors are raised
// before anything touches the socket, so they never mean the connection died.
var (
	ErrCommandEmpty   = errors.New("rcon: command is empty")
	ErrCommandTooLong = errors.New("rcon: command is longer than MaxCommandLen")
	ErrAuthFailed     = errors.New("rcon: the server refused the password")
)

// Client is a reconnecting Factorio RCON connection. One command round trip at
// a time: the mutex is what makes concurrent callers safe, and the protocol has
// no way to interleave two requests on one socket anyway.
type Client struct {
	addr     string
	password string

	mu     sync.Mutex
	conn   net.Conn
	lastID int32
}

func New(addr, password string) *Client {
	return &Client{addr: addr, password: password}
}

// Execute runs one command and returns the server's reply, dialing on first use
// and retrying once if the connection has dropped (game restart, idle timeout).
//
// An empty or over-long command is refused locally. That is a bad command, not a
// broken connection, so it must not tear down a healthy socket: both checks run
// before the client dials at all.
func (c *Client) Execute(cmd string) (string, error) {
	if cmd == "" {
		return "", ErrCommandEmpty
	}
	if len(cmd) > MaxCommandLen {
		return "", fmt.Errorf("%w: %d bytes, limit %d", ErrCommandTooLong, len(cmd), MaxCommandLen)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.dial(); err != nil {
			return "", err
		}
	}
	resp, err := c.exec(cmd)
	if err == nil {
		return resp, nil
	}

	// A failed round trip may have left the stream mid-packet, so the socket
	// goes rather than being reused.
	c.drop()
	if err := c.dial(); err != nil {
		return "", err
	}
	resp, err = c.exec(cmd)
	if err != nil {
		c.drop()
	}
	return resp, err
}

// Close drops the connection. Execute dials again if it is called afterwards.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drop()
}

// exec sends one command and reads exactly one reply packet.
func (c *Client) exec(cmd string) (string, error) {
	if err := c.conn.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
		return "", fmt.Errorf("rcon: set deadline: %w", err)
	}
	id := c.nextID()
	if err := writePacket(c.conn, packet{ID: id, Type: typeExec, Body: cmd}); err != nil {
		return "", err
	}
	reply, err := readPacket(c.conn)
	if err != nil {
		return "", err
	}
	if reply.ID != id {
		return "", fmt.Errorf("rcon: reply carries request id %d, not %d: the stream is out of step", reply.ID, id)
	}
	return reply.Body, nil
}

// dial opens a socket and authenticates on it. Both halves share one deadline:
// a server that accepts the connection and then says nothing must not hang the
// caller.
func (c *Client) dial() error {
	conn, err := net.DialTimeout("tcp", c.addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("rcon: dial %s: %w", c.addr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		conn.Close()
		return fmt.Errorf("rcon: set deadline: %w", err)
	}
	c.conn = conn
	if err := c.auth(); err != nil {
		c.drop()
		return err
	}
	return nil
}

// auth sends the password and waits for the server's verdict. A server may send
// an empty command-output packet before the auth response, so a couple of those
// are skipped rather than read as the answer.
func (c *Client) auth() error {
	id := c.nextID()
	if err := writePacket(c.conn, packet{ID: id, Type: typeAuth, Body: c.password}); err != nil {
		return err
	}
	for range 4 {
		reply, err := readPacket(c.conn)
		if err != nil {
			return err
		}
		switch {
		case reply.Type == typeResponse:
			continue // filler the protocol allows before the real answer
		case reply.ID == authFailedID:
			return ErrAuthFailed
		case reply.ID != id:
			return fmt.Errorf("rcon: auth reply carries request id %d, not %d", reply.ID, id)
		default:
			return nil
		}
	}
	return errors.New("rcon: the server never sent an auth response")
}

func (c *Client) drop() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// nextID hands out request ids the server echoes back, so a reply can be
// matched to its request. It skips 0 and never reaches -1, which the protocol
// reserves for "password refused".
func (c *Client) nextID() int32 {
	if c.lastID >= math.MaxInt32-1 {
		c.lastID = 0
	}
	c.lastID++
	return c.lastID
}
