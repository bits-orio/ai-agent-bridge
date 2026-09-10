// Copied from open-discord-bridge/bridge/internal/transport/sftp.go, unchanged beyond
// this header and the "bridge" -> "service" wording below.

package transport

import (
	"fmt"
	"io"
	"log"
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
	KnownHostsPath string // known_hosts file; if empty, host key is NOT verified (logs a warning)
}

// sftpReader reads the events file over SFTP, lazily (re)connecting. Any Stat/Read error
// drops the connection so the next poll reconnects.
type sftpReader struct {
	cfg  SFTPConfig
	path string

	mu   sync.Mutex
	ssh  *ssh.Client
	sftp *sftp.Client
}

var insecureHostKeyWarn sync.Once

func newSFTPReader(cfg SFTPConfig, path string) *sftpReader {
	if cfg.Host != "" && !strings.Contains(cfg.Host, ":") {
		cfg.Host += ":22"
	}
	return &sftpReader{cfg: cfg, path: path}
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
	client, err := ssh.Dial("tcp", s.cfg.Host, &ssh.ClientConfig{
		User:            s.cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", s.cfg.Host, err)
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return fmt.Errorf("sftp client: %w", err)
	}
	s.ssh, s.sftp = client, sc
	return nil
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
	if err := s.ensure(); err != nil {
		return 0, err
	}
	fi, err := s.sftp.Stat(s.path)
	if err != nil {
		s.reset()
		return 0, err
	}
	return fi.Size(), nil
}

func (s *sftpReader) Read(offset int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return nil, err
	}
	f, err := s.sftp.Open(s.path)
	if err != nil {
		s.reset()
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		s.reset()
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		s.reset()
		return nil, err
	}
	return data, nil
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
	insecureHostKeyWarn.Do(func() {
		log.Printf("transport(sftp): WARNING host key verification disabled (set sftp.known_hosts_path to enable)")
	})
	return ssh.InsecureIgnoreHostKey(), nil
}

// NewSFTP tails a remote file over SFTP by polling.
func NewSFTP(cfg SFTPConfig, path string, interval time.Duration) *Tailer {
	return &Tailer{reader: newSFTPReader(cfg, path), interval: interval}
}
