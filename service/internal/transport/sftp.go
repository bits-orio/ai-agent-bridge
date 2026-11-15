// Copied from open-discord-bridge/bridge/internal/transport/sftp.go. Changed since:
// a deadline on every operation and on the handshake, Head, and a host-key warning
// on every connect rather than once.

package transport

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTPConfig describes how to reach the remote events file over SFTP. Use this when the
// service runs on separate infra from Factorio (e.g. Pterodactyl's per-server SFTP).
type SFTPConfig struct {
	Host           string // host:port (":22" appended if no port)
	User           string
	KeyPath        string // private key file for key auth
	Password       string // password auth (alternative/additional to KeyPath)
	KnownHostsPath string // known_hosts file; if empty, host key is NOT verified (logs a warning at every connect)
	// RequireHostKey refuses to connect without KnownHostsPath instead of
	// accepting any host key (factorio.sftp.insecure_ignore_host_key: false).
	RequireHostKey bool
}

// opTimeout bounds one SFTP operation, the dial and handshake included. A
// half-open connection otherwise blocks the tailer for as long as the kernel
// takes to notice, with the reader's lock held. A file of a few megabytes
// reads well inside it.
const opTimeout = 30 * time.Second

// errOpTimeout is an operation that ran past opTimeout; the connection was
// closed under it, so the next one dials again.
var errOpTimeout = errors.New("sftp: operation timed out, connection closed")

// sftpReader reads the events file over SFTP, lazily (re)connecting. Any Stat/Read error
// drops the connection so the next poll reconnects.
type sftpReader struct {
	cfg  SFTPConfig
	path string

	mu      sync.Mutex
	ssh     *ssh.Client
	sftp    *sftp.Client
	timeout time.Duration // opTimeout; shorter in tests
}

func newSFTPReader(cfg SFTPConfig, path string) *sftpReader {
	if cfg.Host != "" && !strings.Contains(cfg.Host, ":") {
		cfg.Host += ":22"
	}
	return &sftpReader{cfg: cfg, path: path, timeout: opTimeout}
}

func (s *sftpReader) ensure() error {
	if s.sftp != nil {
		return nil
	}
	auth, err := s.authMethods()
	if err != nil {
		return err
	}
	hostKey, err := s.hostKeyCallback()
	if err != nil {
		return err
	}
	// ssh.Dial's Timeout covers the TCP connect only; a server that accepts
	// and then says nothing would hold the handshake open for good. So the
	// whole handshake runs under a deadline on the socket itself.
	conn, err := net.DialTimeout("tcp", s.cfg.Host, 10*time.Second)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", s.cfg.Host, err)
	}
	_ = conn.SetDeadline(time.Now().Add(s.timeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, s.cfg.Host, &ssh.ClientConfig{
		User:            s.cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh handshake %s: %w", s.cfg.Host, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return fmt.Errorf("sftp client: %w", err)
	}
	_ = conn.SetDeadline(time.Time{}) // from here every operation has its own (withDeadline)
	s.ssh, s.sftp = client, sc
	if s.cfg.KnownHostsPath == "" {
		// Every connect, not once a process: on Pterodactyl the SFTP password
		// is the panel password, and a reconnect is when a man in the middle
		// would be let in.
		log.Printf("transport(sftp): WARNING connected to %s WITHOUT checking its host key: anyone able to intercept this connection gets the SFTP password and the event file. Set factorio.sftp.known_hosts_path to verify it.", s.cfg.Host)
	}
	return nil
}

// withDeadline runs op, and when it has not returned within d calls abort and
// reports errOpTimeout. abort must make op return (closing the connection op
// is blocked on does), so the goroutine op runs on ends soon after rather
// than leaking; its result is dropped into a buffered channel nobody waits on.
func withDeadline(d time.Duration, op func() error, abort func()) error {
	done := make(chan error, 1)
	go func() { done <- op() }()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		abort()
		return errOpTimeout
	}
}

// do runs one operation on the current client under the reader's deadline.
// Any error, the deadline's included, drops the connection so the next call
// dials again. The caller holds s.mu.
func (s *sftpReader) do(op func(c *sftp.Client) error) error {
	if err := s.ensure(); err != nil {
		return err
	}
	c, sshc := s.sftp, s.ssh
	err := withDeadline(s.timeout, func() error { return op(c) }, func() {
		// Closing the transport makes every request waiting on it fail.
		c.Close()
		sshc.Close()
	})
	if err != nil {
		s.reset()
	}
	return err
}

func (s *sftpReader) reset() {
	if s.sftp != nil {
		s.sftp.Close()
		s.sftp = nil
	}
	if s.ssh != nil {
		s.ssh.Close()
		s.ssh = nil
	}
}

func (s *sftpReader) Stat() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var size int64
	err := s.do(func(c *sftp.Client) error {
		fi, err := c.Stat(s.path)
		if err != nil {
			return err
		}
		size = fi.Size()
		return nil
	})
	return size, err
}

func (s *sftpReader) Read(offset int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []byte
	err := s.do(func(c *sftp.Client) error {
		f, err := c.Open(s.path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		data, err = io.ReadAll(f)
		return err
	})
	return data, err
}

func (s *sftpReader) Head(n int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []byte
	err := s.do(func(c *sftp.Client) error {
		f, err := c.Open(s.path)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err = readHead(f, n)
		return err
	})
	return data, err
}

func (s *sftpReader) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
	return nil
}

func (s *sftpReader) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if s.cfg.KeyPath != "" {
		pem, err := os.ReadFile(s.cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", s.cfg.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", s.cfg.KeyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if s.cfg.Password != "" {
		methods = append(methods, ssh.Password(s.cfg.Password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("sftp: no auth configured (set key_path or a password)")
	}
	return methods, nil
}

func (s *sftpReader) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if s.cfg.KnownHostsPath != "" {
		cb, err := knownhosts.New(s.cfg.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("known_hosts %s: %w", s.cfg.KnownHostsPath, err)
		}
		return cb, nil
	}
	if s.cfg.RequireHostKey {
		return nil, errors.New("sftp: factorio.sftp.insecure_ignore_host_key is false but no known_hosts_path is set")
	}
	// Accepted, as before; ensure says so loudly on every connect.
	return ssh.InsecureIgnoreHostKey(), nil
}

// NewSFTP tails a remote file over SFTP by polling.
func NewSFTP(cfg SFTPConfig, path string, interval time.Duration) *Tailer {
	return &Tailer{reader: newSFTPReader(cfg, path), interval: interval}
}
