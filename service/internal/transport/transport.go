// Copied from open-discord-bridge/bridge/internal/transport/transport.go, unchanged
// beyond this header and the "bridge" -> "service" wording below.

// Package transport reads the companion mod's JSONL events file and delivers complete
// lines to the service. The read mechanism is pluggable (Local file, SFTP); the polling,
// offset tracking, truncation detection, and line buffering are shared.
package transport

import (
	"bytes"
	"context"
	"log"
	"time"
)

// reader abstracts "how to read the events file". Implementations handle their own
// connection lifecycle (e.g. SFTP reconnect) and should return an error, never panic,
// when the source is temporarily unavailable.
type reader interface {
	// Stat returns the current file size, used to detect growth and truncation.
	Stat() (int64, error)
	// Read returns the bytes from offset to the current end of file.
	Read(offset int64) ([]byte, error)
	// Close releases any underlying connection.
	Close() error
}

// Tailer polls a reader and emits each complete JSONL line via onLine. It starts at the
// current end of file (so a restart doesn't replay history) and resets to offset 0 when
// the file shrinks (the mod truncates once per game session).
type Tailer struct {
	reader   reader
	interval time.Duration
}

func (t *Tailer) Run(ctx context.Context, onLine func([]byte)) {
	defer t.reader.Close()

	var offset int64
	if sz, err := t.reader.Stat(); err == nil {
		offset = sz
	}

	var buf []byte
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sz, err := t.reader.Stat()
			if err != nil {
				log.Printf("transport: stat error (will retry): %v", err)
				continue
			}
			if sz < offset {
				offset = 0 // truncation: new session
				buf = buf[:0]
			}
			if sz == offset {
				continue
			}
			data, err := t.reader.Read(offset)
			if err != nil {
				log.Printf("transport: read error: %v", err)
				continue
			}
			offset += int64(len(data))
			buf = emitLines(append(buf, data...), onLine)
		}
	}
}

// emitLines emits each complete (newline-terminated) line in buf and returns the
// remaining partial line.
func emitLines(buf []byte, onLine func([]byte)) []byte {
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(buf[:i])
		rest := buf[i+1:]
		if len(line) > 0 {
			cp := make([]byte, len(line))
			copy(cp, line)
			onLine(cp)
		}
		buf = append(buf[:0], rest...)
	}
	return buf
}
