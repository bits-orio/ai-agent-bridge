// Copied from open-discord-bridge/bridge/internal/rcon/rcon.go, unchanged beyond this
// header and the "Discord message" -> "aab-rpc request" wording below.

package rcon

import (
	"errors"
	"sync"

	gorcon "github.com/gorcon/rcon"
)

// MaxCommandLen mirrors gorcon's client-side limit on RCON command length, re-exported
// so callers can size a command (e.g. a JSON-wrapped aab-rpc request) before calling
// Execute instead of discovering the limit only after a failed send.
const MaxCommandLen = gorcon.MaxCommandLen

// Client is a thin, reconnecting wrapper around a Factorio RCON connection.
type Client struct {
	addr     string
	password string

	mu   sync.Mutex
	conn *gorcon.Conn
}

func New(addr, password string) *Client {
	return &Client{addr: addr, password: password}
}

// Execute runs a command, dialing on first use and retrying once if the connection
// has dropped (game restart, idle timeout).
//
// gorcon rejects an empty or over-MaxCommandLen command before it ever touches the
// socket (see isLocalValidationErr): that's a bad command, not a broken connection, so
// it must not tear down and re-dial a perfectly healthy conn. Only genuine connection
// errors (I/O errors, protocol desync, auth failure on the retry) trigger a reconnect.
func (c *Client) Execute(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.dial(); err != nil {
			return "", err
		}
	}

	resp, err := c.conn.Execute(cmd)
	if err != nil && !isLocalValidationErr(err) {
		c.conn.Close()
		c.conn = nil
		if derr := c.dial(); derr != nil {
			return "", derr
		}
		resp, err = c.conn.Execute(cmd)
	}
	return resp, err
}

// isLocalValidationErr reports whether err is one of gorcon's client-side checks that
// run before any bytes are written to the connection (command too long / empty). These
// mean the caller sent a bad command, not that the connection died.
func isLocalValidationErr(err error) bool {
	return errors.Is(err, gorcon.ErrCommandTooLong) || errors.Is(err, gorcon.ErrCommandEmpty)
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) dial() error {
	conn, err := gorcon.Dial(c.addr, c.password)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}
