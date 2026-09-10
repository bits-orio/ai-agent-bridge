// The Source RCON wire format, which is all Factorio's RCON speaks: an int32
// length, an int32 request id, an int32 packet type, the body, then two NUL
// bytes (the body's terminator and an empty second string). Every integer is
// little endian, and the length counts everything after itself.

package rcon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Packet types. 3 opens a session with the password, 2 runs a command (and is
// also what the server sends back to accept or refuse the password), 0 carries
// a command's output.
const (
	typeResponse = int32(0)
	typeExec     = int32(2)
	typeAuth     = int32(3)
)

// authFailedID is the request id a server puts on an auth response to say the
// password was wrong.
const authFailedID = int32(-1)

const (
	headerLen     = 8 // id and type
	terminatorLen = 2 // the body's NUL and the empty string's NUL
)

// packet is one RCON message in either direction.
type packet struct {
	ID   int32
	Type int32
	Body string
}

// writePacket sends one packet in a single Write, so a large body never leaves
// a half-written frame on the wire.
func writePacket(w io.Writer, p packet) error {
	size := headerLen + len(p.Body) + terminatorLen
	buf := make([]byte, 4+size)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(size))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(p.ID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(p.Type))
	copy(buf[12:], p.Body)
	// The last two bytes stay zero: they are the two NUL terminators.
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("rcon: write packet: %w", err)
	}
	return nil
}

// readPacket reads exactly one packet, by its own length prefix. Factorio
// answers a command in one packet however big the reply is: 4 MB came back
// whole and byte-exact on 2.0.77 (TESTING.md check 1.5).
func readPacket(r io.Reader) (packet, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return packet{}, fmt.Errorf("rcon: read reply length: %w", err)
	}
	size := int32(binary.LittleEndian.Uint32(head[:]))
	if size < headerLen+terminatorLen {
		return packet{}, fmt.Errorf("rcon: reply claims %d bytes, under the %d-byte minimum", size, headerLen+terminatorLen)
	}
	if int64(size) > maxResponseLen {
		return packet{}, fmt.Errorf("rcon: reply claims %d bytes, over the %d-byte ceiling", size, maxResponseLen)
	}

	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return packet{}, fmt.Errorf("rcon: read reply body: %w", err)
	}
	return packet{
		ID:   int32(binary.LittleEndian.Uint32(body[0:4])),
		Type: int32(binary.LittleEndian.Uint32(body[4:8])),
		Body: string(bytes.TrimRight(body[headerLen:], "\x00")),
	}, nil
}
