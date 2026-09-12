// Package model is the neutral boundary between the agent loop and whatever
// answers a question. The loop owns the conversation and the tool results; a
// Model only turns "here is the history, here are the tools" into one more
// assistant turn (PLAN.md open question 7: a second provider should be an
// addition, not a rewrite).
//
// Nothing here mentions a vendor. internal/model/anthropic speaks to the
// Anthropic API, internal/model/fake is the scripted model the end-to-end
// harness runs against, and both are the same shape to the agent.
package model

import (
	"context"
	"encoding/json"
)

// Block types. A Block is the smallest piece of a turn: some text, a request
// to call a tool, the result of one, or a piece of the model's own reasoning
// that has to be handed back verbatim on the next round.
const (
	BlockText             = "text"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
	// BlockReasoning is one entry of an OpenRouter reasoning_details array,
	// kept as the raw JSON it arrived as and sent back unchanged. Thinking
	// holds the plain reasoning string instead when a route sent only that.
	BlockReasoning = "reasoning"
)

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Stop reasons. A Model reports why its turn ended; the loop only needs to
// tell "it called tools" from "it finished talking" from "it ran out of
// room".
const (
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
)

// Block is one piece of a turn. Which fields matter depends on Type:
//
//   - BlockText: Text.
//   - BlockToolUse: ID, Name, Input (the arguments, as raw JSON).
//   - BlockToolResult: ID (the tool_use it answers), Content, IsError.
//   - BlockThinking: Thinking, Signature.
//   - BlockRedactedThinking: Data.
//   - BlockReasoning: Data (raw JSON) or Thinking (plain text).
//
// The loop reads only text and tool calls, but it keeps every block a turn
// produced and replays them in order: a thinking block is signed, and a model
// that thought before calling a tool expects its own reasoning back on the
// next round.
type Block struct {
	Type      string
	Text      string
	ID        string
	Name      string
	Input     json.RawMessage
	Content   string
	IsError   bool
	Thinking  string
	Signature string
	Data      string
}

// Message is one turn in the conversation.
type Message struct {
	Role   string
	Blocks []Block
}

// ToolDef is one tool as the model sees it: a name, a description to choose
// by, and a JSON Schema for the arguments. internal/tools.Tool is the same
// thing with a Call attached; the loop keeps the two apart so a Model can
// never run a tool itself.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Usage counts the tokens one or more steps cost. The four main counters
// are disjoint, the way the API reports them: a token is fresh input, output,
// a cache read or a cache write, never two of those. CacheWriteHourTokens is
// the part of CacheWriteTokens written with the one-hour lifetime, which is
// priced higher than a five-minute write.
type Usage struct {
	InputTokens          int
	OutputTokens         int
	CacheReadTokens      int
	CacheWriteTokens     int
	CacheWriteHourTokens int
	ReasoningTokens      int     // the part of OutputTokens a route reported as reasoning
	Cost                 float64 // USD the route itself reported, 0 when it reports none
}

// Add folds another step's usage into u.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	u.CacheWriteHourTokens += other.CacheWriteHourTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.Cost += other.Cost
}

// Total is every token the question has spent so far, cached reads at full
// count; the raw number for reporting.
func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// CacheReadWeight is what a cached input token counts for against the
// per-question budget. Every route this service knows bills a cache read at
// a tenth of a fresh token or less, so the budget follows the bill rather
// than the raw count: a five-thousand-token prefix re-read every round
// would otherwise spend a 20,000-token budget in four rounds while costing
// a few hundredths of a cent.
const CacheReadWeight = 0.1

// Budgeted is what the question has spent in fresh-token terms, which is
// what the per-question budget is measured against.
func (u Usage) Budgeted() int {
	return u.InputTokens + u.OutputTokens + u.CacheWriteTokens + int(float64(u.CacheReadTokens)*CacheReadWeight)
}

// Step is one assistant turn: the blocks it produced, why it stopped, and
// what it cost.
type Step struct {
	Blocks     []Block
	StopReason string
	Usage      Usage
	Provider   string // the host that served the step when the route names one; a prompt cache lives on one host
}

// Model produces one assistant turn at a time. Implementations must be safe
// for use from one goroutine at a time; the agent loop never calls Step
// concurrently for the same question.
type Model interface {
	// Name is the model id, used for logging and for the price table.
	Name() string
	// Step sends the system prompt, the whole conversation so far and the
	// available tools, and returns the next assistant turn.
	Step(ctx context.Context, system string, msgs []Message, tools []ToolDef) (Step, error)
}

// TextOf joins every text block in a turn, which is how the loop rescues an
// answer from a model that stopped talking without calling submit_answer.
func TextOf(blocks []Block) string {
	out := ""
	for _, b := range blocks {
		if b.Type != BlockText || b.Text == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += b.Text
	}
	return out
}

// ToolUses returns the tool_use blocks of a turn, in the order the model
// produced them.
func ToolUses(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}
