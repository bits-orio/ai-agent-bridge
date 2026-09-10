// Package agent is the loop: one question in, one artifact out.
//
// The shape of a round is fixed. The model gets the system prompt, the
// conversation so far and every tool the server exposes. It calls tools, all
// of a round's calls run at once and come back in one user message, and it
// ends by calling submit_answer. Anything else that can end a question, a
// model that stops talking, a round cap, a token budget, a quota, ends it
// with an artifact too, so a player always gets an answer.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/catalog"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// defaultForce is the force a question is read against when the companion
// sent no force hint. It is Factorio's own default force name.
const defaultForce = "player"

const (
	quotaNotice   = "You have asked all the questions your hourly allowance covers. Try again a bit later."
	roundsNotice  = "I ran out of rounds before I could answer that. Try asking something narrower."
	tokenNotice   = "That question ran past its token budget before I could answer. Try asking something narrower."
	stalledNotice = "The model stopped without answering. Try asking again."
)

// Question is one thing to answer, as the companion handed it over.
type Question struct {
	ID          int64
	Text        string
	PlayerIndex *int
	Force       string
	Asker       string // human label for the prompt and the log
}

func (q Question) force() string {
	if q.Force == "" {
		return defaultForce
	}
	return q.Force
}

func (q Question) askerLabel() string {
	if q.Asker != "" {
		return q.Asker
	}
	if q.PlayerIndex != nil {
		return fmt.Sprintf("player %d", *q.PlayerIndex)
	}
	return "another mod"
}

// key is what memory and quota are counted against: the player when there is
// one, the force otherwise, so a mod asking on its own behalf shares one
// bucket rather than dodging the quota entirely.
func (q Question) key() string {
	if q.PlayerIndex != nil {
		return fmt.Sprintf("player:%d", *q.PlayerIndex)
	}
	return "force:" + q.force()
}

// Caps are the per-question limits the operator sets.
type Caps struct {
	MaxRounds                 int
	MaxTokensPerQuestion      int
	MemoryTTL                 time.Duration
	QuestionsPerPlayerPerHour int
}

func (c Caps) maxRounds() int {
	if c.MaxRounds <= 0 {
		return 6
	}
	return c.MaxRounds
}

// Result is one answered question, and what it cost.
type Result struct {
	Artifact Artifact
	Rounds   int
	Usage    model.Usage
	CostUSD  float64
}

// Agent answers questions with one model, one set of caps, and the memory and
// quota that go with them. It is safe for concurrent use.
type Agent struct {
	mdl   model.Model
	caps  Caps
	mem   *memory
	quota *quota
	now   func() time.Time
}

func New(m model.Model, caps Caps) *Agent {
	return &Agent{
		mdl:   m,
		caps:  caps,
		mem:   newMemory(caps.MemoryTTL),
		quota: newQuota(caps.QuestionsPerPlayerPerHour),
		now:   time.Now,
	}
}

// Model is the model id answers are being charged against.
func (a *Agent) Model() string { return a.mdl.Name() }

// Answer runs one question to an artifact. An error means the model itself
// failed and the caller decides what to tell the player; every other ending
// comes back as a Result.
func (a *Agent) Answer(ctx context.Context, q Question, ts []tools.Tool) (Result, error) {
	now := a.now()
	if !a.quota.take(q.key(), now) {
		return Result{Artifact: Notice(LevelWarning, quotaNotice)}, nil
	}

	ctx = catalog.WithForce(ctx, q.force())
	byName := index(ts)
	defs := defsFor(ts)
	system := systemPrompt(q)
	msgs := []model.Message{{
		Role:   model.RoleUser,
		Blocks: []model.Block{{Type: model.BlockText, Text: prompt(q, a.mem.recall(q.key(), now))}},
	}}

	var usage model.Usage
	for round := 1; round <= a.caps.maxRounds(); round++ {
		step, err := a.mdl.Step(ctx, system, msgs, defs)
		if err != nil {
			return Result{Rounds: round, Usage: usage, CostUSD: CostUSD(a.mdl.Name(), usage)}, err
		}
		usage.Add(step.Usage)
		msgs = append(msgs, model.Message{Role: model.RoleAssistant, Blocks: step.Blocks})

		calls := model.ToolUses(step.Blocks)
		if len(calls) == 0 {
			return a.finish(q, fromText(step), round, usage), nil
		}

		results, artifact, submitted := a.runCalls(ctx, calls, byName)
		if submitted {
			return a.finish(q, artifact, round, usage), nil
		}
		msgs = append(msgs, model.Message{Role: model.RoleUser, Blocks: results})

		if budget := a.caps.MaxTokensPerQuestion; budget > 0 && usage.Total() >= budget {
			return a.finish(q, Notice(LevelWarning, tokenNotice), round, usage), nil
		}
	}
	return a.finish(q, Notice(LevelWarning, roundsNotice), a.caps.maxRounds(), usage), nil
}

// runCalls executes one round's tool calls at once and returns their results
// in the order the model asked for them. A submitted artifact ends the round
// there and then; the results are only needed if it did not.
func (a *Agent) runCalls(ctx context.Context, calls []model.Block, byName map[string]tools.Tool) ([]model.Block, Artifact, bool) {
	results := make([]model.Block, len(calls))
	var (
		mu        sync.Mutex
		submitted *Artifact
		wg        sync.WaitGroup
	)
	for i, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call.Name == SubmitTool {
				artifact, err := parseSubmission(call.Input)
				if err != nil {
					results[i] = failed(call.ID, err)
					return
				}
				mu.Lock()
				if submitted == nil {
					submitted = &artifact
				}
				mu.Unlock()
				results[i] = model.Block{Type: model.BlockToolResult, ID: call.ID, Content: "sent"}
				return
			}
			t, known := byName[call.Name]
			if !known {
				results[i] = failed(call.ID, fmt.Errorf("there is no tool named %q", call.Name))
				return
			}
			out, err := t.Call(ctx, call.Input)
			if err != nil {
				results[i] = failed(call.ID, err)
				return
			}
			results[i] = model.Block{Type: model.BlockToolResult, ID: call.ID, Content: content(out)}
		}()
	}
	wg.Wait()

	if submitted != nil {
		return results, *submitted, true
	}
	return results, Artifact{}, false
}

// finish clips the artifact, remembers the exchange and prices the question.
func (a *Agent) finish(q Question, artifact Artifact, rounds int, usage model.Usage) Result {
	clipped, err := validate(artifact)
	if err != nil {
		clipped = Notice(LevelWarning, stalledNotice)
	}
	a.mem.record(q.key(), q.Text, clipped.Line(), a.now())
	return Result{
		Artifact: clipped,
		Rounds:   rounds,
		Usage:    usage,
		CostUSD:  CostUSD(a.mdl.Name(), usage),
	}
}

// fromText rescues an answer from a turn with no tool calls in it. A model
// that finished talking gets its text wrapped as a summary; one that stopped
// for any other reason gets a notice, because its text is likely a fragment.
func fromText(step model.Step) Artifact {
	text := model.TextOf(step.Blocks)
	if step.StopReason != model.StopEndTurn || strings.TrimSpace(text) == "" {
		return Notice(LevelWarning, stalledNotice)
	}
	lines := strings.Split(text, "\n")
	return Summary(lines...)
}

func parseSubmission(input json.RawMessage) (Artifact, error) {
	var artifact Artifact
	if err := json.Unmarshal(input, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("that is not a valid artifact: %v", err)
	}
	return validate(artifact)
}

func failed(id string, err error) model.Block {
	return model.Block{Type: model.BlockToolResult, ID: id, Content: err.Error(), IsError: true}
}

// content is what the model sees of a tool's return value. An empty return is
// shown as null rather than as nothing at all, which reads as a broken tool.
func content(out json.RawMessage) string {
	if len(out) == 0 {
		return "null"
	}
	return string(out)
}

func index(ts []tools.Tool) map[string]tools.Tool {
	byName := make(map[string]tools.Tool, len(ts))
	for _, t := range ts {
		byName[t.Name] = t
	}
	return byName
}
