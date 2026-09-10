// Package rpc is the reference client for the companion mod's aab-rpc-v1 protocol
// (PLAN.md): one console command, "/aab-rpc <json>", one JSON object in, one JSON object
// out. Every reply is {"ok":true,"r":...} or {"ok":false,"e":"<code>","m":"<detail>"}.
//
// Nothing here is Factorio-specific beyond the command name: Client talks to anything
// that implements the RCON interface below, so tests use a fake instead of a live server.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/bits-orio/ai-agent-bridge/service/internal/rcon"
)

// command is the console command every aab-rpc-v1 request is sent through (PLAN.md § The
// protocol, aab-rpc-v1).
const command = "aab-rpc"

// protocolVersion is the "v" field every request must carry. The companion refuses a
// request whose v is anything but 1 with bad_version
// (companion-mod/scripts/rpc.lua), so it is not optional.
const protocolVersion = 1

// Error codes defined by aab-rpc-v1 (PLAN.md). CodeTooLarge doubles as the client-side
// code returned when Call refuses to even send an oversized request. The companion could
// never receive it whole, so it is the same failure the protocol names for an oversized
// reply.
const (
	CodeBadJSON       = "bad_json"
	CodeBadVersion    = "bad_version"
	CodeBadOp         = "bad_op"
	CodeNoProvider    = "no_provider"
	CodeNoTool        = "no_tool"
	CodeProviderError = "provider_error"
	CodeBadResult     = "bad_result"
	CodeTooLarge      = "too_large"
	CodeNoQuestion    = "no_question"
	// CodeBadArtifact is the companion refusing an answer it cannot render:
	// an unknown shape, or a field of the wrong type (review-fix contract 5).
	// Sending the same artifact again would fail the same way, so a caller
	// must not retry it.
	CodeBadArtifact = "bad_artifact"
)

// RCON is the minimal executor Client needs: one blocking command/response round trip.
// *rcon.Client satisfies it; tests use a fake.
type RCON interface {
	Execute(cmd string) (string, error)
}

// Error is a typed {"ok":false,...} reply, carrying the protocol's error code so callers
// can switch on it (e.g. CodeNoQuestion to mean "nothing pending", not a real failure).
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "aab-rpc: " + e.Code
	}
	return fmt.Sprintf("aab-rpc: %s: %s", e.Code, e.Message)
}

// HasCode reports whether err is a protocol error carrying code, so callers can
// tell one refusal from another without unwrapping by hand.
func HasCode(err error, code string) bool {
	var protocol *Error
	return errors.As(err, &protocol) && protocol.Code == code
}

// Client sends aab-rpc-v1 requests over an RCON connection.
type Client struct {
	rc RCON
}

func New(rc RCON) *Client {
	return &Client{rc: rc}
}

// envelope is the wire shape of every aab-rpc-v1 reply.
type envelope struct {
	OK bool            `json:"ok"`
	R  json.RawMessage `json:"r"`
	E  string          `json:"e"`
	M  string          `json:"m"`
}

// Call sends one aab-rpc-v1 request (op plus payload's fields merged into one JSON
// object) and returns the raw "r" payload of a successful reply. A {"ok":false,...}
// reply comes back as *Error. payload may be nil (an empty request) or anything that
// marshals to a JSON object.
func (c *Client) Call(ctx context.Context, op string, payload any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd, err := buildCommand(op, payload)
	if err != nil {
		return nil, fmt.Errorf("aab-rpc: encode %s request: %w", op, err)
	}
	if len(cmd) > rcon.MaxCommandLen {
		return nil, &Error{
			Code:    CodeTooLarge,
			Message: fmt.Sprintf("%s request is %d bytes, over the %d-byte rcon command limit; refused before sending", op, len(cmd), rcon.MaxCommandLen),
		}
	}

	resp, err := c.rc.Execute(cmd)
	if err != nil {
		return nil, fmt.Errorf("aab-rpc: %s: %w", op, err)
	}

	var env envelope
	if err := json.Unmarshal([]byte(resp), &env); err != nil {
		return nil, fmt.Errorf("aab-rpc: %s: bad reply JSON: %w", op, err)
	}
	if !env.OK {
		return nil, &Error{Code: env.E, Message: env.M}
	}
	return env.R, nil
}

// buildCommand renders "/aab-rpc <json>" for op and payload.
func buildCommand(op string, payload any) (string, error) {
	body, err := mergeOp(op, payload)
	if err != nil {
		return "", err
	}
	return "/" + command + " " + string(body), nil
}

// mergeOp folds op and the protocol version into payload's top-level JSON object as "op"
// and "v", the two fields every aab-rpc-v1 request must carry. payload must marshal to a
// JSON object (or be nil).
func mergeOp(op string, payload any) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, fmt.Errorf("payload must encode to a JSON object: %w", err)
			}
		}
	}
	opJSON, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	fields["op"] = opJSON
	fields["v"] = json.RawMessage(strconv.Itoa(protocolVersion))
	return json.Marshal(fields)
}
