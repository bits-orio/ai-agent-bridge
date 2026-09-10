// Copied from open-discord-bridge/bridge/internal/transport/local.go, unchanged.

package transport

import (
	"io"
	"os"
	"time"
)

// localReader reads the events file from the local filesystem (same host as Factorio,
// or a shared/bind mount).
type localReader struct {
	path string
}

func (l *localReader) Stat() (int64, error) {
	fi, err := os.Stat(l.path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (l *localReader) Read(offset int64) ([]byte, error) {
	f, err := os.Open(l.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func (l *localReader) Close() error { return nil }

// NewLocal tails a local file by polling.
func NewLocal(path string, interval time.Duration) *Tailer {
	return &Tailer{reader: &localReader{path: path}, interval: interval}
}
