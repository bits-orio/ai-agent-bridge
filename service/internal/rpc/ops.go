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

// ToolManifest is one function entry in a provider's agent_tools_v1 manifest: a
// description and, for tools that take arguments, a params grammar
// ("<type>[!] <description>" per param, PLAN.md § Probe).
type ToolManifest struct {
	Desc   string            `json:"desc"`
	Params map[string]string `json:"params,omitempty"`
}

// Provider is one entry in the tools catalog: one provider's interface name, probe
// version, and manifest, verbatim (PLAN.md says "manifests verbatim").
type Provider struct {
	Iface string                  `json:"iface"`
	V     int                     `json:"v"`
	Tools map[string]ToolManifest `json:"tools"`
}

// ToolsReply is the "r" of a tools call: the sorted list of providers (PLAN.md).
type ToolsReply []Provider

// Tools calls the tools op ({}) and returns the parsed catalog.
func (c *Client) Tools(ctx context.Context) (ToolsReply, error) {
	raw, err := c.Call(ctx, "tools", nil)
	if err != nil {
		return nil, err
	}
	var out ToolsReply
	if err := unmarshalList(raw, &out); err != nil {
		return nil, fmt.Errorf("aab-rpc: tools: bad reply: %w", err)
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
	Force       string `json:"force,omitempty"`
	Tick        uint64 `json:"tick"`
}

// PollReply is the "r" of a poll call: questions with id greater than after, oldest
// first (PLAN.md).
type PollReply []Question

// pollRequest is the payload of a poll op: {after}, the cursor (PLAN.md).
type pollRequest struct {
	After int64 `json:"after"`
}

// Poll fetches every question with id greater than after, oldest first. Pass 0 on first
// use to fetch everything still pending.
func (c *Client) Poll(ctx context.Context, after int64) (PollReply, error) {
	raw, err := c.Call(ctx, "poll", pollRequest{After: after})
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
// (CONTEXT.md invariant 2): the companion marks the question answered, renders the
// artifact to the asker, and raises on_answer. A lost RCON reply costs nothing: the
// caller re-polls the same cursor and never resubmits an answer.
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
