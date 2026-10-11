// Typed helpers for each aab-rpc-v1 op (PLAN.md § The protocol, aab-rpc-v1). Reply field
// names here are the service's own choice as the protocol's reference client (PLAN.md
// decision 7): PLAN.md fixes the op table and the ok/e/m envelope, not the "r" shapes,
// and these must be kept in step with companion-mod/scripts/rpc.lua once it exists.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MaxPollLimit is the largest page of questions the companion serves in one
// poll reply (review-fix contract 3). Asking for more is not an error; the
// companion clamps, and so does Poll, so the request says what will happen.
const MaxPollLimit = 64

// unmarshalList decodes a JSON array reply into out, treating "{}" as an empty
// list. The companion's JSON encoder cannot tell an empty Lua array from an
// empty object and emits {} for both (verified against the live mod, 2026-09-10).
func unmarshalList(raw json.RawMessage, out any) error {
	if b := bytes.TrimSpace(raw); len(b) == 2 && b[0] == '{' && b[1] == '}' {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// StatusReply is the "r" of a status call: protocol version, mod version, tick, player
// count, pending question count, which player command is live (PLAN.md), the highest
// question id issued and the map's id.
// Field names must stay in step with OPS.status in companion-mod/scripts/rpc.lua.
type StatusReply struct {
	ProtocolVersion int    `json:"protocol"`
	ModVersion      string `json:"mod_version"`
	Tick            uint64 `json:"tick"`
	PlayerCount     int    `json:"player_count"`
	PendingCount    int    `json:"pending"`
	AskCommand      string `json:"ask_command"`
	// LastID is the highest question id the companion has issued so far
	// (review-fix contract 4): how many questions a save has seen, and
	// whether new ones are arriving at all. One below what the last look saw
	// means the game went back (cmd/aab, cursor.go).
	LastID int64 `json:"last_id"`
	// MapID names the map the game is on: the id the companion's session
	// line in events.jsonl carries (companion-mod/scripts/map_id.lua). The
	// service records it in history when no session line has said so, and
	// another map is a reason to poll from the start (cmd/aab, cursor.go).
	// An older companion sends none, and neither does a save that has not
	// made its id yet; both read as "".
	MapID MapID `json:"map_id"`
}

// MapID is a map id as the companion sends it. A string is taken as written
// and a number as its JSON text, the way history reads the id a session line
// carries, so the two compare equal. A value of any other type reads as no
// id rather than failing the whole status reply.
type MapID string

// UnmarshalJSON reads a string or a number, and anything else as "".
func (m *MapID) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*m = MapID(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*m = MapID(n.String())
		return nil
	}
	*m = ""
	return nil
}

// Status calls the status op ({}) and returns the parsed reply.
func (c *Client) Status(ctx context.Context) (StatusReply, error) {
	raw, err := c.Call(ctx, "status", nil)
	if err != nil {
		return StatusReply{}, err
	}
	var out StatusReply
	if err := json.Unmarshal(raw, &out); err != nil {
		return StatusReply{}, fmt.Errorf("aab-rpc: status: bad reply: %w", err)
	}
	return out, nil
}

// ForceLabel is one row of the labels op: a force name and the name players
// use for it, supplied by a labels provider (companion README, "Force labels
// by probe"). Forces without a label are not listed.
type ForceLabel struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Labels reads every force label the companion can find right now. An empty
// list is the normal case on a server without a naming mod.
func (c *Client) Labels(ctx context.Context) ([]ForceLabel, error) {
	raw, err := c.Call(ctx, "labels", nil)
	if err != nil {
		return nil, err
	}
	var out []ForceLabel
	if err := unmarshalList(raw, &out); err != nil {
		return nil, fmt.Errorf("aab-rpc: labels: bad reply: %w", err)
	}
	return out, nil
}

// callRequest is the payload of a call op: {i, f, a}, provider interface, function name,
// arguments (PLAN.md).
type callRequest struct {
	I string `json:"i"`
	F string `json:"f"`
	A any    `json:"a,omitempty"`
}

// CallTool invokes one provider function (iface.fn(args)) via the call op and returns its
// plain-data return value, unparsed. The caller knows the tool's own reply shape.
func (c *Client) CallTool(ctx context.Context, iface, fn string, args any) (json.RawMessage, error) {
	out, _, err := c.CallToolTimed(ctx, iface, fn, args)
	return out, err
}

// CallToolTimed is CallTool plus the time the command spent on the socket,
// which is what a slow-tool report wants: the caller's own elapsed time
// includes waiting behind whatever else the round dispatched.
func (c *Client) CallToolTimed(ctx context.Context, iface, fn string, args any) (json.RawMessage, time.Duration, error) {
	return c.CallTimed(ctx, "call", callRequest{I: iface, F: fn, A: args})
}

// Question is one entry in a poll reply: text plus asker, force hint and tick
// (CONTEXT.md § Questions and answers). PlayerIndex is nil when the asker is not a
// player (e.g. a question submitted through the ai-agent-bridge-v1 remote interface with
// no player_index).
type Question struct {
	ID          int64  `json:"id"`
	Text        string `json:"text"`
	PlayerIndex *int   `json:"player_index,omitempty"`
	// PlayerName is the asker's name when the companion could resolve one
	// (review-fix contract 3). History rows are keyed by player name, so without
	// it "when did I last die" cannot be scoped to the person who asked.
	PlayerName string `json:"player_name,omitempty"`
	Force      string `json:"force,omitempty"`
	// ViewingForce is the force the asker was on when they asked, when a
	// home force provider named another one as Force: a member watching
	// from a spectator force. Empty otherwise, and from an older companion.
	ViewingForce string `json:"viewing_force,omitempty"`
	// WatchingForce is the force whose base the asker was looking at, when
	// a provider named one other than Force: the team a spectator watches.
	WatchingForce string `json:"watching_force,omitempty"`
	Tick          uint64 `json:"tick"`
	// Scope and Private come from the companion's chat scope probe (Phase 3):
	// the session pool this question belongs to, and whether its answer stays
	// inside an audience. An older companion sends neither, which reads as
	// global.
	Scope   string `json:"scope,omitempty"`
	Private bool   `json:"private,omitempty"`
	// Surface is where the asker stood when they asked, when they are a
	// player; a where question is about that surface more often than not.
	Surface string `json:"surface,omitempty"`
	// PhysicalSurface is where the asker's character stood when Surface is
	// the one they were looking at in remote view; empty when the same.
	PhysicalSurface string `json:"physical_surface,omitempty"`
	// Personality is the map's aab-personality setting at the moment the
	// question was asked: trimmed, at most 500 bytes, and left out when
	// empty. When it says anything it wins over the operator's
	// agent.personality: off, a preset name, or free text (agent/voice.go).
	// An older companion never sends it, which reads as the config's voice.
	Personality string `json:"personality,omitempty"`
}

// PollReply is the "r" of a poll call: unanswered questions with id greater than
// after, oldest first, at most limit of them (PLAN.md, review-fix contract 3).
type PollReply []Question

// pollRequest is the payload of a poll op: {after, limit}. Limit is what keeps a
// backlog from encoding past the companion's reply cap, which used to wedge the
// service permanently: the whole reply came back as one too_large error, so
// nothing was ever answered and nothing ever shrank the backlog.
type pollRequest struct {
	After int64 `json:"after"`
	Limit int   `json:"limit,omitempty"`
}

// Poll fetches unanswered questions with id greater than after, oldest first, at
// most limit of them. Pass limit 0 to take the companion's own default page size.
func (c *Client) Poll(ctx context.Context, after int64, limit int) (PollReply, error) {
	if limit > MaxPollLimit {
		limit = MaxPollLimit
	}
	if limit < 0 {
		limit = 0
	}
	raw, err := c.Call(ctx, "poll", pollRequest{After: after, Limit: limit})
	if err != nil {
		return nil, err
	}
	var out PollReply
	if err := unmarshalList(raw, &out); err != nil {
		return nil, fmt.Errorf("aab-rpc: poll: bad reply: %w", err)
	}
	return out, nil
}

// answerRequest is the payload of an answer op: {qid, artifact} (PLAN.md). artifact is
// one of the typed answer shapes (summary, comparison, list, table, notice), left as
// `any` here since internal/artifacts (Phase 1+) owns those types, not this package.
type answerRequest struct {
	QID      int64 `json:"qid"`
	Artifact any   `json:"artifact"`
}

// Answer submits the artifact for question qid. This is the one op that writes storage
// (CONTEXT.md invariant 2): the companion renders the artifact to the asker, marks the
// question answered once rendering succeeded, and raises on_answer.
//
// A lost RCON reply costs nothing. The companion keeps offering an unanswered question
// on every poll, so the caller delivers the same artifact again on the next tick without
// paying the model a second time. The one refusal not worth retrying is CodeBadArtifact:
// the artifact itself is wrong (review-fix contract 5).
func (c *Client) Answer(ctx context.Context, qid int64, artifact any) (bool, error) {
	return c.AnswerOn(ctx, 0, qid, artifact)
}

// AnswerOn is Answer held to RCON connection conn: the one the poll that
// offered qid came over (Connection). When that connection has broken, the
// answer is not sent over a new one, and the call fails as Unreachable: a
// server that restarted in between may have loaded a save where qid names
// someone else's question, in someone else's scope. conn 0, or an executor
// that does not number its connections, sends it as Answer does.
func (c *Client) AnswerOn(ctx context.Context, conn uint64, qid int64, artifact any) (bool, error) {
	execute := c.rc.Execute
	if p, ok := c.rc.(pinnedRCON); ok && conn != 0 {
		execute = func(cmd string) (string, time.Duration, error) { return p.ExecuteOn(conn, cmd) }
	}
	raw, _, err := c.call(ctx, "answer", answerRequest{QID: qid, Artifact: artifact}, execute)
	if err != nil {
		return false, err
	}
	var ok bool
	if err := json.Unmarshal(raw, &ok); err != nil {
		return false, fmt.Errorf("aab-rpc: answer: bad reply: %w", err)
	}
	return ok, nil
}
