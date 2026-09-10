package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rcon"
)

// fakeRCON is a minimal RCON that records the last command sent and returns a canned
// response, so envelope parsing can be tested without a live Factorio server.
type fakeRCON struct {
	resp  string
	err   error
	calls int
	last  string
}

func (f *fakeRCON) Execute(cmd string) (string, error) {
	f.calls++
	f.last = cmd
	return f.resp, f.err
}

func TestCallSendsOpAndParsesOKReply(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{"tick":42}}`}
	c := New(fake)

	raw, err := c.Call(context.Background(), "status", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != `{"tick":42}` {
		t.Errorf("r = %s, want {\"tick\":42}", raw)
	}
	if !strings.HasPrefix(fake.last, "/aab-rpc ") {
		t.Fatalf("command = %q, want /aab-rpc prefix", fake.last)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(fake.last, "/aab-rpc ")), &sent); err != nil {
		t.Fatalf("sent command is not valid JSON: %v", err)
	}
	if string(sent["op"]) != `"status"` {
		t.Errorf(`sent op = %s, want "status"`, sent["op"])
	}
}

func TestCallMergesPayloadFieldsWithOp(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[]}`}
	c := New(fake)

	if _, err := c.Call(context.Background(), "poll", pollRequest{After: 17}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(fake.last, "/aab-rpc ")), &sent); err != nil {
		t.Fatalf("sent command is not valid JSON: %v", err)
	}
	if string(sent["op"]) != `"poll"` {
		t.Errorf(`sent op = %s, want "poll"`, sent["op"])
	}
	if string(sent["after"]) != `17` {
		t.Errorf(`sent after = %s, want 17`, sent["after"])
	}
}

func TestCallErrorReplyReturnsTypedError(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":false,"e":"bad_op","m":"unknown op \"frobnicate\""}`}
	c := New(fake)

	_, err := c.Call(context.Background(), "frobnicate", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error is not *rpc.Error: %v (%T)", err, err)
	}
	if rpcErr.Code != CodeBadOp {
		t.Errorf("Code = %q, want %q", rpcErr.Code, CodeBadOp)
	}
	if rpcErr.Message != `unknown op "frobnicate"` {
		t.Errorf("Message = %q", rpcErr.Message)
	}
}

func TestCallOversizedCommandRefusedBeforeSending(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{}}`}
	c := New(fake)

	huge := strings.Repeat("x", rcon.MaxCommandLen)
	_, err := c.Call(context.Background(), "call", map[string]string{"padding": huge})
	if err == nil {
		t.Fatal("expected the oversized command to be refused")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error is not *rpc.Error: %v (%T)", err, err)
	}
	if rpcErr.Code != CodeTooLarge {
		t.Errorf("Code = %q, want %q", rpcErr.Code, CodeTooLarge)
	}
	if fake.calls != 0 {
		t.Errorf("Execute was called %d times, want 0: oversized command must never reach rcon", fake.calls)
	}
}

func TestCallBadReplyJSON(t *testing.T) {
	fake := &fakeRCON{resp: `not json`}
	c := New(fake)

	if _, err := c.Call(context.Background(), "status", nil); err == nil {
		t.Fatal("expected an error for unparseable reply")
	}
}

func TestCallContextAlreadyCanceled(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{}}`}
	c := New(fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "status", nil); err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
	if fake.calls != 0 {
		t.Errorf("Execute was called %d times, want 0: canceled context must not reach rcon", fake.calls)
	}
}

func TestStatusParsesReply(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{"protocol":1,"mod_version":"0.1.0","tick":12345,"player_count":2,"pending":3,"ask_command":"ask"}}`}
	c := New(fake)

	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := StatusReply{ProtocolVersion: 1, ModVersion: "0.1.0", Tick: 12345, PlayerCount: 2, PendingCount: 3, AskCommand: "ask"}
	if got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

func TestToolsParsesCatalog(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"iface":"ai-agent-bridge-v1","v":1,"tools":{"list_forces":{"desc":"Every force."}}}]}`}
	c := New(fake)

	got, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 1 || got[0].Iface != "ai-agent-bridge-v1" || got[0].Tools["list_forces"].Desc != "Every force." {
		t.Fatalf("Tools() = %+v", got)
	}
}

func TestPollParsesQuestions(t *testing.T) {
	pi := 3
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"id":5,"text":"how much iron?","player_index":3,"force":"player","tick":900}]}`}
	c := New(fake)

	got, err := c.Poll(context.Background(), 4)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	want := PollReply{{ID: 5, Text: "how much iron?", PlayerIndex: &pi, Force: "player", Tick: 900}}
	if len(got) != 1 || got[0].ID != want[0].ID || got[0].Text != want[0].Text ||
		got[0].PlayerIndex == nil || *got[0].PlayerIndex != *want[0].PlayerIndex ||
		got[0].Force != want[0].Force || got[0].Tick != want[0].Tick {
		t.Errorf("Poll() = %+v, want %+v", got, want)
	}
}

func TestAnswerParsesBoolReply(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":true}`}
	c := New(fake)

	ok, err := c.Answer(context.Background(), 5, map[string]string{"kind": "summary"})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !ok {
		t.Error("Answer() = false, want true")
	}

	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(fake.last, "/aab-rpc ")), &sent); err != nil {
		t.Fatalf("sent command is not valid JSON: %v", err)
	}
	if string(sent["qid"]) != "5" {
		t.Errorf("sent qid = %s, want 5", sent["qid"])
	}
}

func TestAnswerNoQuestionError(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":false,"e":"no_question","m":"no question with id 99"}`}
	c := New(fake)

	if _, err := c.Answer(context.Background(), 99, map[string]string{}); err == nil {
		t.Fatal("expected an error")
	} else {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeNoQuestion {
			t.Fatalf("got %v, want *Error with code %q", err, CodeNoQuestion)
		}
	}
}
