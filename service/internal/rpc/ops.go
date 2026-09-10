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
	"log"
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

// Tools calls the tools op ({}) and returns the parsed catalog, one provider at
// a time.
//
// Each entry is decoded on its own so a third mod that writes a plausible but
// wrong manifest costs itself its tools and nobody else's (review-fix contract
// 6). Decoding the whole array in one pass meant a list of tool names where a
// map belonged, a nested params table, or a localised desc took every tool on
// the server down with it, the companion's own included, and the agent then
// answered every question from the prompt alone.
func (c *Client) Tools(ctx context.Context) (ToolsReply, error) {
	raw, err := c.Call(ctx, "tools", nil)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if err := unmarshalList(raw, &entries); err != nil {
		return nil, fmt.Errorf("aab-rpc: tools: bad reply: %w", err)
	}

	out := make(ToolsReply, 0, len(entries))
	for _, entry := range entries {
		var provider Provider
		if err := json.Unmarshal(entry, &provider); err != nil {
			log.Printf("tools: skipping provider %s, its manifest did not decode: %v", ifaceName(entry), err)
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}

// ifaceName digs the iface field out of a provider entry that failed to decode,
// so the log line names the mod whose manifest needs fixing.
func ifaceName(entry json.RawMessage) string {
	var named struct {
		Iface string `json:"iface"`
	}
	if err := json.Unmarshal(entry, &named); err == nil && named.Iface != "" {
		return named.Iface
	}
	return "(unnamed)"
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
