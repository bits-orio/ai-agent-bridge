package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func (f *fakeRCON) Execute(cmd string) (string, time.Duration, error) {
	f.calls++
	f.last = cmd
	return f.resp, 0, f.err
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
	// The companion refuses any request whose v is not 1 with bad_version, so every
	// request carries it (companion-mod/scripts/rpc.lua).
	if string(sent["v"]) != `1` {
		t.Errorf(`sent v = %s, want 1`, sent["v"])
	}
}

func TestCallMergesPayloadFieldsWithOp(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[]}`}
	c := New(fake)

	if _, err := c.Call(context.Background(), "poll", pollRequest{After: 17, Limit: 16}); err != nil {
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
	if string(sent["limit"]) != `16` {
		t.Errorf(`sent limit = %s, want 16`, sent["limit"])
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
	fake := &fakeRCON{resp: `{"ok":true,"r":{"protocol":1,"mod_version":"0.1.0","tick":12345,"player_count":2,"pending":3,"ask_command":"ask","last_id":41}}`}
	c := New(fake)

	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := StatusReply{ProtocolVersion: 1, ModVersion: "0.1.0", Tick: 12345, PlayerCount: 2, PendingCount: 3, AskCommand: "ask", LastID: 41}
	if got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

// The map id is optional: an older companion sends none. A number reads as
// its JSON text, the way history reads a session line's, and a value of any
// other type reads as none rather than failing the status read the cursor
// check and history both depend on.
func TestStatusReadsTheMapID(t *testing.T) {
	for field, want := range map[string]MapID{
		``:                                 "",
		`,"map_id":"2894571-0-1804289383"`: "2894571-0-1804289383",
		`,"map_id":12345`:                  "12345",
		`,"map_id":null`:                   "",
		`,"map_id":true`:                   "",
		`,"map_id":{"seed":1}`:             "",
	} {
		fake := &fakeRCON{resp: `{"ok":true,"r":{"tick":7,"last_id":2` + field + `}}`}
		got, err := New(fake).Status(context.Background())
		if err != nil {
			t.Errorf("Status with %q: %v", field, err)
			continue
		}
		if got.MapID != want || got.Tick != 7 || got.LastID != 2 {
			t.Errorf("Status with %q = %+v, want map id %q, tick 7, last id 2", field, got, want)
		}
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
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"id":5,"text":"how much iron?","player_index":3,"player_name":"Bob","force":"player","tick":900}]}`}
	c := New(fake)

	got, err := c.Poll(context.Background(), 4, 16)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	want := PollReply{{ID: 5, Text: "how much iron?", PlayerIndex: &pi, PlayerName: "Bob", Force: "player", Tick: 900}}
	if len(got) != 1 || got[0].ID != want[0].ID || got[0].Text != want[0].Text ||
		got[0].PlayerIndex == nil || *got[0].PlayerIndex != *want[0].PlayerIndex ||
		got[0].PlayerName != want[0].PlayerName ||
		got[0].Force != want[0].Force || got[0].Tick != want[0].Tick {
		t.Errorf("Poll() = %+v, want %+v", got, want)
	}
}

// A question carries the map's voice setting as it stood when it was asked,
// and one without it, from an older companion or an empty setting, reads as
// none.
func TestPollParsesTheQuestionsPersonality(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":[{"id":5,"text":"status","tick":1,"personality":"Talk like a ship's captain"},{"id":6,"text":"status","tick":2}]}`}
	got, err := New(fake).Poll(context.Background(), 4, 16)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(got) != 2 || got[0].Personality != "Talk like a ship's captain" || got[1].Personality != "" {
		t.Errorf("Poll() = %+v, want the first question's personality and none on the second", got)
	}
}

// The page size is clamped rather than refused, so the request always says what
// the companion will actually do.
func TestPollClampsTheLimit(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{}}`}
	c := New(fake)

	if _, err := c.Poll(context.Background(), 0, MaxPollLimit*10); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(fake.last, "/aab-rpc ")), &sent); err != nil {
		t.Fatalf("sent command is not valid JSON: %v", err)
	}
	if got := string(sent["limit"]); got != "64" {
		t.Errorf("sent limit = %s, want it clamped to 64", got)
	}

	// Limit 0 leaves the field out entirely: the companion picks its own page size.
	if _, err := c.Poll(context.Background(), 0, 0); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if strings.Contains(fake.last, "limit") {
		t.Errorf("command = %q, want no limit field", fake.last)
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

// One provider's broken manifest costs that provider its tools, not the whole
// catalog: the companion's own engine tools ride in the same reply, and losing
// them left the agent answering from the prompt alone (review-fix contract 6).
func TestToolsSkipsOnlyTheProviderThatCannotDecode(t *testing.T) {
	for _, tc := range []struct{ name, bad string }{
		{"tools is a list of names", `{"iface":"bad-mod","v":1,"tools":["team_list","team_standings"]}`},
		{"params is a nested table", `{"iface":"bad-mod","v":1,"tools":{"x":{"desc":"x","params":{"milestone":{"type":"string"}}}}}`},
		{"desc is a localised string", `{"iface":"bad-mod","v":1,"tools":{"x":{"desc":["bad-mod.tool-desc"]}}}`},
		{"v is a string", `{"iface":"bad-mod","v":"1","tools":{"x":{"desc":"x"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			good := `{"iface":"ai-agent-bridge-tools","v":1,"tools":{"list_forces":{"desc":"Every force."}}}`
			fake := &fakeRCON{resp: `{"ok":true,"r":[` + tc.bad + `,` + good + `]}`}

			got, err := New(fake).Tools(context.Background())
			if err != nil {
				t.Fatalf("Tools: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("Tools() returned %d provider(s), want 1: %+v", len(got), got)
			}
			if got[0].Iface != "ai-agent-bridge-tools" || got[0].Tools["list_forces"].Desc != "Every force." {
				t.Errorf("kept %+v, want the provider that decodes", got[0])
			}
		})
	}
}

// A reply that is not a list at all is still a failed call: there is nothing to
// salvage per provider.
func TestToolsRejectsAReplyThatIsNotAList(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":"nope"}`}
	if _, err := New(fake).Tools(context.Background()); err == nil {
		t.Fatal("expected a non-list tools reply to be an error")
	}
}

func TestHasCode(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":false,"e":"bad_artifact","m":"unknown shape \"graph\""}`}
	_, err := New(fake).Answer(context.Background(), 7, map[string]string{"shape": "graph"})
	if !HasCode(err, CodeBadArtifact) {
		t.Errorf("HasCode(%v, bad_artifact) = false, want true", err)
	}
	if HasCode(err, CodeNoQuestion) {
		t.Error("HasCode matched the wrong code")
	}
	if HasCode(nil, CodeBadArtifact) {
		t.Error("HasCode(nil) = true, want false")
	}
}

func TestEmptyObjectDecodesAsEmptyList(t *testing.T) {
	fake := &fakeRCON{resp: `{"ok":true,"r":{}}`}
	c := New(fake)
	qs, err := c.Poll(context.Background(), 0, 16)
	if err != nil || len(qs) != 0 {
		t.Fatalf("Poll() = %v, %v; want empty, nil", qs, err)
	}
	fake.resp = `{"ok":true,"r":{}}`
	ps, err := c.Tools(context.Background())
	if err != nil || len(ps) != 0 {
		t.Fatalf("Tools() = %v, %v; want empty, nil", ps, err)
	}
}

// Only a call that got no reply at all is Unreachable: a refusal, a reply in
// a shape this client cannot read, or text from a server without the mod all
// came from a server that was up for the whole round trip.
func TestUnreachableIsOnlyACallWithNoReply(t *testing.T) {
	down := errors.New("dial tcp 127.0.0.1:27015: connection refused")
	_, err := New(&fakeRCON{err: down}).Status(context.Background())
	if !Unreachable(err) {
		t.Errorf("Unreachable(%v) = false for a failed round trip", err)
	}
	if !errors.Is(err, down) || err.Error() != "aab-rpc: status: "+down.Error() {
		t.Errorf("err = %q, want the transport error wrapped as before", err)
	}
	for name, resp := range map[string]string{
		"a refusal":                `{"ok":false,"e":"bad_op","m":"unknown op"}`,
		"a reply it cannot read":   `{"ok":true,"r":"nope"}`,
		"a server without the mod": `Unknown command "aab-rpc".`,
	} {
		_, err := New(&fakeRCON{resp: resp}).Status(context.Background())
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if Unreachable(err) {
			t.Errorf("%s: Unreachable(%v) = true, want false: the server replied", name, err)
		}
	}
	if Unreachable(nil) {
		t.Error("Unreachable(nil) = true")
	}
}

// pinnedFake numbers its connections the way rcon.Client does and records
// which way each command went out.
type pinnedFake struct {
	fakeRCON
	conn  uint64
	on    []uint64 // the connection each ExecuteOn call named
	plain int      // Execute calls
}

func (p *pinnedFake) Connection() uint64 { return p.conn }

func (p *pinnedFake) Execute(cmd string) (string, time.Duration, error) {
	p.plain++
	return p.fakeRCON.Execute(cmd)
}

func (p *pinnedFake) ExecuteOn(conn uint64, cmd string) (string, time.Duration, error) {
	p.on = append(p.on, conn)
	if conn != p.conn {
		return "", 0, rcon.ErrConnectionChanged
	}
	return p.fakeRCON.Execute(cmd)
}

// An answer goes over the connection its question was polled on, or not at
// all: after a restart the same qid may be someone else's question. conn 0,
// and an executor that does not number connections, send it as Answer does.
func TestAnswerOnHoldsTheAnswerToItsConnection(t *testing.T) {
	p := &pinnedFake{fakeRCON: fakeRCON{resp: `{"ok":true,"r":true}`}, conn: 2}
	c := New(p)
	if got := c.Connection(); got != 2 {
		t.Fatalf("Connection() = %d, want the executor's 2", got)
	}

	if ok, err := c.AnswerOn(context.Background(), 2, 5, map[string]string{"shape": "summary"}); err != nil || !ok {
		t.Fatalf("AnswerOn on the live connection = %v, %v", ok, err)
	}
	ok, err := c.AnswerOn(context.Background(), 1, 6, map[string]string{"shape": "summary"})
	if ok || !Unreachable(err) || !errors.Is(err, rcon.ErrConnectionChanged) {
		t.Errorf("AnswerOn on a broken connection = %v, %v; want unreachable, connection changed", ok, err)
	}
	if p.plain != 0 || len(p.on) != 2 || p.on[0] != 2 || p.on[1] != 1 {
		t.Errorf("Execute calls %d, ExecuteOn calls %v: want every held answer through ExecuteOn and none around it", p.plain, p.on)
	}

	if _, err := c.AnswerOn(context.Background(), 0, 7, map[string]string{}); err != nil || p.plain != 1 {
		t.Errorf("AnswerOn with no connection named: err %v, Execute calls %d, want it sent as Answer sends", err, p.plain)
	}
	if _, err := c.Answer(context.Background(), 8, map[string]string{}); err != nil || p.plain != 2 {
		t.Errorf("Answer: err %v, Execute calls %d, want it unpinned", err, p.plain)
	}

	plain := &fakeRCON{resp: `{"ok":true,"r":true}`}
	if ok, err := New(plain).AnswerOn(context.Background(), 3, 9, map[string]string{}); err != nil || !ok || plain.calls != 1 {
		t.Errorf("AnswerOn over an executor without connections = %v, %v after %d call(s)", ok, err, plain.calls)
	}
	if got := New(plain).Connection(); got != 0 {
		t.Errorf("Connection() over an executor without connections = %d, want 0", got)
	}
}
