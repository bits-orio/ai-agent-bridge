// Copied from open-discord-bridge/bridge/internal/transport/transport_test.go, unchanged.

package transport

import (
	"reflect"
	"testing"
)

func collect(t *testing.T, chunks ...string) [][]byte {
	t.Helper()
	var got [][]byte
	var buf []byte
	for _, c := range chunks {
		buf = emitLines(append(buf, c...), func(line []byte) {
			got = append(got, append([]byte(nil), line...))
		})
	}
	return got
}

func TestEmitLinesSplitsAndBuffers(t *testing.T) {
	// A line split across two reads should emit once, fully assembled.
	got := collect(t, `{"a":1}`+"\n"+`{"b":`, `2}`+"\n")
	want := [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmitLinesSkipsBlank(t *testing.T) {
	got := collect(t, "\n\n"+`{"x":1}`+"\n")
	if len(got) != 1 || string(got[0]) != `{"x":1}` {
		t.Fatalf("got %q", got)
	}
}
