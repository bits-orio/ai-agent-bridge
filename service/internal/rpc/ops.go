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
// count, pending question count and which player command is live (PLAN.md).
// Field names must stay in step with OPS.status in companion-mod/scripts/rpc.lua.
type StatusReply struct {
	ProtocolVersion int    `json:"protocol"`
	ModVersion      string `json:"mod_version"`
	Tick            uint64 `json:"tick"`
	PlayerCount     int    `json:"player_count"`
	PendingCount    int    `json:"pending"`
	AskCommand      string `json:"ask_command"`
	// LastID is the highest question id the companion has issued so far
	// (review-fix contract 4). The service polls from 0 and needs no cursor, so
	// this is for the operator and the harness: how many questions a save has
	// seen, and whether new ones are arriving at all.
	LastID int64 `json:"last_id"`
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
	return c.Call(ctx, "call", callRequest{I: iface, F: fn, A: args})
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
	Tick       uint64 `json:"tick"`
	// Scope and Private come from the companion's chat scope probe (Phase 3):
	// the session pool this question belongs to, and whether its answer stays
	// inside an audience. An older companion sends neither, which reads as
	// global.
	Scope   string `json:"scope,omitempty"`
	Private bool   `json:"private,omitempty"`
	// Surface is where the asker stood when they asked, when they are a
	// player; a where question is about that surface more often than not.
	Surface string `json:"surface,omitempty"`
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
	raw, err := c.Call(ctx, "answer", answerRequest{QID: qid, Artifact: artifact})
	if err != nil {
		return false, err
	}
	var ok bool
	if err := json.Unmarshal(raw, &ok); err != nil {
		return false, fmt.Errorf("aab-rpc: answer: bad reply: %w", err)
	}
	return ok, nil
}
