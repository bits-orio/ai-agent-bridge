// The per-question token budget, measured on what the question itself used.
//
// model.Usage.Budgeted counts every fresh input token in full and a cache
// read at a tenth. The system prompt and the tool list, the fixed prefix,
// are about 12,500 tokens, re-sent every round: read from cache they cost a
// tenth, but on a host with no cache entry (87 of 800 rounds in the ledger,
// and every time OpenRouter moves to a new host) they count in full every
// round. One such question had rounds of 12.9k then 14.2k fresh input, no
// cache read at all, and ended on a 20,000-token budget after two rounds of
// a short question. The budget measured cache luck, not the question.
//
// The scheme. The prefix is measured once, on round 1: every prompt token
// that round sent (fresh input, cache read and cache write) less the user
// turn, estimated at four bytes a token. Round 1's prompt is the prefix and
// the user turn and nothing else, so what is left is the prefix in the
// model's own tokenizer. Then every round that read nothing from cache, the
// round that pays the prefix in full, has the prefix taken back off its
// fresh input and cache write, never below zero. A round that read from
// cache is charged exactly as Budgeted says: its prefix is already the
// cheap tenth. So a cold question is charged its user turn, its output and
// the conversation it grew, as a warm one is, and a question that really
// grows past the budget still stops on it.
//
// A measured prefix of zero, a round 1 smaller than its own user turn as in
// the package's scripted tests, takes nothing off and leaves the budget as
// it was.

package agent

import "github.com/bits-orio/ai-agent-bridge/service/internal/model"

// nearlySpent is the share of the budget past which the model is told one
// turn is left: a round from here can take the rest.
const nearlySpent = 0.7

type tokenMeter struct {
	budget   int // MaxTokensPerQuestion; zero or less is no budget
	userTurn int // round 1's user turn, estimated
	prefix   int // the fixed prefix, measured on round 1
	measured bool
	spent    int // what the budget has been charged so far
}

// newTokenMeter starts a question's meter. userTurn is the estimated token
// count of round 1's user message, the one part of round 1's prompt that is
// not the prefix.
func newTokenMeter(budget, userTurn int) *tokenMeter {
	return &tokenMeter{budget: budget, userTurn: userTurn}
}

// userTurnTokens is the four-bytes-a-token estimate of a user message, the
// same estimator the ledger's briefing_tokens uses.
func userTurnTokens(text string) int { return len(text) / 4 }

// add charges one round.
func (m *tokenMeter) add(u model.Usage) {
	if !m.measured {
		m.measured = true
		m.prefix = max(0, u.InputTokens+u.CacheReadTokens+u.CacheWriteTokens-m.userTurn)
	}
	charge := u.Budgeted()
	if u.CacheReadTokens == 0 {
		charge -= min(m.prefix, u.InputTokens+u.CacheWriteTokens)
	}
	m.spent += max(0, charge)
}

// over reports whether the question has spent its budget.
func (m *tokenMeter) over() bool {
	return m.budget > 0 && m.spent >= m.budget
}

// nearlyOver reports whether one more round may spend the rest.
func (m *tokenMeter) nearlyOver() bool {
	return m.budget > 0 && float64(m.spent) >= nearlySpent*float64(m.budget)
}
