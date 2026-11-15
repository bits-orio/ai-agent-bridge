package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rcon"
)

// say sends {op:"say", artifact} and reads {"r":true} as printed.
func TestSaySendsTheArtifactAndReadsTheReply(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":true}`}
	if err := New(fake).Say(context.Background(), map[string]any{"shape": "summary", "lines": []string{"hi"}}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(fake.last, "/aab-rpc ")), &sent); err != nil {
		t.Fatalf("sent command is not JSON: %v", err)
	}
	if string(sent["op"]) != `"say"` || !strings.Contains(string(sent["artifact"]), `"lines":["hi"]`) {
		t.Errorf("sent %s, want op say with the artifact", fake.last)
	}
	if _, ok := sent["qid"]; ok {
		t.Errorf("say carries no question id, sent %s", fake.last)
	}
}

// An older companion, or the in-game switch off, answers bad_op: the caller
// gets ErrSayUnsupported, so it can stop trying for this connection. Every
// other refusal stays a plain protocol error.
func TestSayMapsBadOpToUnsupported(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":false,"e":"bad_op","m":"unknown op \"say\""}`}
	err := New(fake).Say(context.Background(), map[string]any{"shape": "summary"})
	if !errors.Is(err, ErrSayUnsupported) || !HasCode(err, CodeBadOp) {
		t.Errorf("Say on bad_op = %v, want ErrSayUnsupported that still carries bad_op", err)
	}

	fake.resp = `{"ok":false,"e":"bad_artifact","m":"prints nothing"}`
	err = New(fake).Say(context.Background(), map[string]any{"shape": "summary"})
	if errors.Is(err, ErrSayUnsupported) || !HasCode(err, CodeBadArtifact) {
		t.Errorf("Say on bad_artifact = %v, want a bad_artifact error, not unsupported", err)
	}

	fake.resp = `{"ok":true,"r":false}`
	if err := New(fake).Say(context.Background(), map[string]any{"shape": "summary"}); err == nil {
		t.Error("Say on r=false returned nil, want an error: nothing was confirmed printed")
	}
}

// A toast is held to the connection the tick verified: over a new one the
// server may have restarted onto another map, and the event belongs to the
// old one.
func TestSayOnHoldsTheToastToItsConnection(t *testing.T) {
	p := &pinnedFake{fakeRCON: fakeRCON{resp: `{"ok":true,"r":true}`}, conn: 4}
	c := New(p)
	if err := c.SayOn(context.Background(), 4, map[string]any{"shape": "summary"}); err != nil {
		t.Fatalf("SayOn on the live connection: %v", err)
	}
	err := c.SayOn(context.Background(), 3, map[string]any{"shape": "summary"})
	if !Unreachable(err) || !errors.Is(err, rcon.ErrConnectionChanged) {
		t.Errorf("SayOn on a broken connection = %v, want unreachable, connection changed", err)
	}
	if p.plain != 0 || len(p.on) != 2 {
		t.Errorf("Execute calls %d, ExecuteOn %v: want both through ExecuteOn", p.plain, p.on)
	}
}
