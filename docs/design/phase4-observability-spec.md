# Phase 4 contract: observability

Fixed contract every builder codes against, in the same spirit as
[phase1-2-spec.md](phase1-2-spec.md) and [phase3-spec.md](phase3-spec.md).
CONTEXT.md and PLAN.md still rule; this file pins what they leave open: the
ledger the service writes, the two JSON objects it is built from, the
`aab stats` reports read back from it, and what the ledger is never allowed
to do to an answer. It is a standalone file: [phase4-spec.md](phase4-spec.md)
covers the briefing, the cost tiers, the bounded walks and the wire format,
and the two link to each other.

The owner's stance driving this file: a service cannot improve what it does
not measure, structured logging sounds expensive and is not, about a
kilobyte a question, and the payoff is long term. The first `aab stats` run
is not the point. A year of it is.

Owner decisions this file records, 2026-09-12: the service writes a JSONL
ledger, one object per round and one object per question; `aab stats`
reads the ledger and reports on it; the two reports that matter most are
tool repeat count within a single round, which names the next plural tool
to build, and cache hit ratio per question, the regression alarm that would
have caught the 2026-09-12 asker-line prompt regression the day it landed;
the active `personality` voice (Decision 12) is logged on the per-question
record so an experiment is measured rather than judged by ear; and the
ledger is built to never slow an answer, never fail a question, never carry
a secret, and never grow past what the operator chose.

## 1. What already exists

Some of this is already tracked, just not in a form a second program can
query. `model.Usage` (`service/internal/model/model.go` lines 94-102)
already carries `InputTokens`, `OutputTokens`, `CacheReadTokens`,
`CacheWriteTokens`, `CacheWriteHourTokens` and `ReasoningTokens`, plus
`Cost`. `model.Step` (lines 137-142) carries `Provider` alongside `Blocks`,
`StopReason` and `Usage`.

Two log lines already exist, at two different grains. `traceRound`
(`service/internal/agent/trace.go`, header comment lines 1-3, function at
line 13) already writes one line per model round, when the optional
`Trace func(format string, args ...any)` hook (`service/internal/agent/agent.go`
line 203) is set; it is wired once, at `a.traceRound(q, round, step)`
(line 267), and the line it writes already carries the question id, round
number, stop reason, every token count but the hour-cache-write count,
provider, and the names of the tools called that round. Every answer also
produces one aggregate line, at
`service/cmd/aab/answer.go` lines 118-120:

```
answer %d shape=%s rounds=%d tokens=%d/%d cached=%d/%d cost=$%.4f
```

Both lines are opt-in text for an operator tailing a terminal, not
always-on structured data a second program can query. The per-round line
names which tools were called but not how big a result came back, how long
the call took, or whether it errored. Neither line is written per question
in a form `aab stats` can read back. This design keeps both lines exactly
as they are and adds the ledger beside them: the aggregate and per-round
lines stay for the operator watching live, and the ledger is for a program
reading back later. Removing either line is out of scope here.

The one structured, queryable store the service already keeps, the SQLite
history at `service/internal/history/history.go` (schema at lines 23-31,
columns `id, tick, event, player, force, data, received_at` at lines
24-30), holds engine events: deaths, joins, research, rockets, chat. It
says nothing about a model round, a tool call, or a dollar spent. The
ledger is the same idea, aimed at the agent loop instead of the game.

## 2. The ledger

Two JSON objects, both written by the service, both keyed by `question_id`
so a round can be joined back to the question it belongs to.

### Per round

```json
{
  "question_id": 1042,
  "round": 2,
  "model": "deepseek/deepseek-v4-pro-0813",
  "provider": "DeepInfra",
  "ms_first_byte": 640,
  "ms_total": 4120,
  "input_tokens": 3810,
  "output_tokens": 214,
  "cache_read_tokens": 3400,
  "cache_write_tokens": 0,
  "cache_write_hour_tokens": 0,
  "reasoning_tokens": 0,
  "cost": 0.00071,
  "stop_reason": "tool_use",
  "tool_calls": [
    {
      "name": "mts-v1__team_clocks",
      "args": "{\"all\":true}",
      "result_bytes": 812,
      "ms": 210,
      "ms_above_floor": 105,
      "ok": true,
      "error": null
    },
    {
      "name": "ai-agent-bridge-tools__find_item",
      "args": "{\"item\":\"iron-plate\"}",
      "result_bytes": 340,
      "ms": 118,
      "ms_above_floor": 13,
      "ok": true,
      "error": null,
      "path": "logistic"
    }
  ]
}
```

| field | holds |
|---|---|
| `question_id` | which question this round belongs to |
| `round` | 1-based round number, the same counter as the agent loop's `for round := 1; round <= a.caps.maxRounds(); round++` (`service/internal/agent/agent.go` line 258) |
| `model` | the model id this round ran against |
| `provider` | `model.Step.Provider`: which upstream OpenRouter routed the round to. A different word for a different thing than CONTEXT.md's Provider (a mod exposing tools) |
| `ms_first_byte` | wall clock from request start to the first streamed byte |
| `ms_total` | wall clock for the whole round: the model call plus every tool call it made |
| `input_tokens` | `Usage.InputTokens` |
| `output_tokens` | `Usage.OutputTokens` |
| `cache_read_tokens` | `Usage.CacheReadTokens`: input served from the prompt cache, billed at the cached-read rate |
| `cache_write_tokens` | `Usage.CacheWriteTokens` |
| `cache_write_hour_tokens` | `Usage.CacheWriteHourTokens`, the hour-TTL cache write |
| `reasoning_tokens` | `Usage.ReasoningTokens` |
| `cost` | `Usage.Cost`, USD |
| `stop_reason` | `model.Step.StopReason` |
| `tool_calls` | one entry per call this round dispatched, from `runCalls`/`runReads` (`service/internal/agent/agent.go` lines 305, 372) |

Each `tool_calls` entry:

| field | holds |
|---|---|
| `name` | the catalog name, `iface__fn` the way `ToolName` builds it (`service/internal/catalog/catalog.go` line 111, `sep` at line 28) |
| `args` | the call's arguments, clipped the same way a tool result is clipped, never the full text of an oversized argument |
| `result_bytes` | the size, in bytes, of the result this call returned. A sweep carries this inside the envelope [phase4-spec.md](phase4-spec.md)'s wire contract specifies; the service's own list tools (`recent_chat`, `catch_up`, `recent_events`, `last_event`, `count_events`) sweep no axis and return the same header-plus-tab-separated-rows shape with no sweep envelope, so their `result_bytes` counts a header line and rows, nothing else. Measured ahead of the service's own `max_tool_result_bytes` clip: 4096 in Group A, unchanged from today's default, and 8000 in Group B, raised in the same release as `CAPS.call`'s rise to 16384 (`service/internal/config/config.go` line 38) |
| `ms` | wall clock for this one call |
| `ms_above_floor` | `ms` minus `rpc.Client.Floor()` (`service/internal/rpc/rpc.go` line 86, backed by the 32-sample ring in `service/internal/rpc/latency.go` lines 15-40): the part of the call that is not baseline RCON, so a genuinely slow tool stands out from a merely slow connection |
| `ok` | true when the call returned a result |
| `error` | the error string when `ok` is false, `null` otherwise |
| `path` | present only when `name` is a `find_item` call, omitted for every other tool: which rung answered it, `logistic` (a logistic network already held the item, no walk needed), `walk` (the bounded entity walk under `work_budget` found it), or `refused` (no anchor and no logistic hit, so the structured refusal fired). This is what tests the prediction that the three-rung logistic path already answers most where-is-it questions ([phase4-spec.md](phase4-spec.md)), rather than leaving it assumed |

### Per question

```json
{
  "question_id": 1042,
  "asker": "player7",
  "force": "team-3",
  "surface": "nauvis",
  "session_key": "team-3#iron",
  "session_fresh": false,
  "text": "what is each team researching",
  "rounds": 2,
  "lookups": 3,
  "zero_lookup": false,
  "shape": "table",
  "cost": 0.00142,
  "ms_model": 5230,
  "ms_rcon": 1140,
  "briefing": "on",
  "briefing_bytes": 612,
  "briefing_tokens": 480,
  "briefing_ms": 525,
  "asked_back": false,
  "awaiting_reply_resolved": false,
  "refused": false,
  "refused_reason": null,
  "voice": "off"
}
```

| field | holds |
|---|---|
| `question_id` | the id the companion's question ring issued |
| `asker` | player name, or the asker label for a mod (CONTEXT.md's Asker) |
| `force` | the asker's force hint |
| `surface` | the asker's surface at ask time |
| `session_key` | `sessionKey(scope, name)` (`service/internal/agent/session.go` line 87): `scope` alone, or `scope#name` |
| `session_fresh` | the `fresh` bool `sessions.open` returns (`service/internal/agent/session.go` line 104): true when this question started a new session rather than continuing one |
| `text` | the question text, already capped at 400 bytes by the companion (`companion-mod/scripts/questions.lua` line 38) before it reaches the service |
| `rounds` | how many rounds the question took, bound by `max_rounds` (default 6, `service/internal/config/config.go` line 35) |
| `lookups` | total tool calls across every round, bound by `max_tool_calls` (default 30, line 46) |
| `zero_lookup` | true when `lookups` is zero: the question was answered from the briefing alone, with no tool call at all. This is the free tier's own signature |
| `shape` | the artifact shape delivered: `summary`, `notice`, `list`, `table` or `comparison`, the five [phase1-2-spec.md](phase1-2-spec.md) defines (`service/internal/agent/artifact.go` lines 19-23) |
| `cost` | the question's total cost, every round's `cost` summed |
| `ms_model` | wall clock inside model calls: every round's `ms_total` minus that round's own tool time |
| `ms_rcon` | wall clock spent on RCON: every `tool_calls[].ms` across every round, summed, plus `briefing_ms` as a separate summand, so the briefing's own cost stays visible rather than buried inside a number that reads as pure tool time |
| `briefing` | whether the briefing rode with this question: `on` when it was assembled and reached the user turn, `off` when the operator's `briefing` config key had it turned off for this question, `failed` when the config key was on but the attempt itself timed out, errored, or hit a companion lacking the op, so the service answered without it |
| `briefing_bytes` | meaningful only when `briefing` is `on`, zero when it is `off` or `failed`: the size of the reply the briefing was assembled from, several `call` replies summed until the companion ships a dedicated `briefing` op, one reply after ([phase4-spec.md](phase4-spec.md) has the wire contract for both) |
| `briefing_tokens` | meaningful only when `briefing` is `on`, zero when it is `off` or `failed`: the briefing's size once it sits in the user turn, from the byte-per-four estimator, `floor(briefing_bytes / 4)`. It is an estimate, not a figure the model API reports back on its own; the estimator is what the ledger has, and consistency across every question matters more than precision on any one of them |
| `briefing_ms` | meaningful only when `briefing` is `on`, zero when it is `off` or `failed`: wall clock spent fetching the briefing, before round 1, five serialized `call` trips today at about 525 ms at the 105 ms RCON floor, or the one `briefing` trip once it ships |
| `asked_back` | true when the answer delivered for this question was the ask-back `notice` at level `confirmation` (Decision 7), not a real answer. This is the field a tier 2 tool's structured refusal actually sets: the refusal itself is a normal tool result the round continues past, invisible to the ledger on its own, and it shows up here only through what the model does with it |
| `awaiting_reply_resolved` | true when this question arrived while its session's `awaiting_reply` flag was still set: the player's reply landed inside the session's `clarify_idle` window rather than after it expired. Set on the resolving question's own record, never rewritten onto the ask-back's, since a ledger entry is never rewritten once written |
| `refused` | true when the service refused the question outright, before or instead of asking the model at all: an hourly quota, the server-wide hourly quota, the daily cost budget, or an empty question (`refusal(...)`, `service/internal/agent/agent.go` lines 232, 236 and 241, and `service/internal/agent/commands.go` line 30). A tier 2 tool's own refusal does not set this field; see `asked_back` |
| `refused_reason` | the notice text delivered when `refused` is true (`quotaNotice`, `serverQuotaNotice`, `budgetNotice`, or the empty-question notice), `null` otherwise |
| `voice` | the `personality` setting active for this question (Decision 12): `off`, or the flavour value |

## 3. Where it lives, its rotation, and its size

One JSONL file per UTC calendar day, `ledger-YYYY-MM-DD.jsonl`, appended by
the service process only. That is the same append-only discipline the
companion already keeps for its own `events.jsonl` and the service already
keeps for the SQLite events table (CONTEXT.md's Event file entry;
`service/internal/history/history.go` lines 23-31): nothing here is ever
rewritten, only appended to. A file per day needs no rotation logic inside
a running process. A day boundary opens the next file; an operator who
wants disk back deletes a file that is old enough, which the service never
does on its own.

Size: about a kilobyte per question, one question object plus one round
object per round plus however many tool call entries those rounds carried,
clipped arguments and result byte counts (never raw results) keeping any
one entry bounded. At the server-wide cap of 120 questions an hour
(`defaultServerQuestionsHour`, `service/internal/config/config.go` line
44), the worst case is a server asking questions nonstop for 24 hours:
roughly 3MB a day.

It holds player names and question text by design (Decision 10 and R13
already put both in the model's hands), so it is not something to
publish. It stays on the operator's disk, next to the SQLite history file,
and `*.jsonl` joins `*.log` in `.gitignore` for the same reason
`/service/*.log` and `/service/history.sqlite*` are already there.

## 4. `aab stats`

A subcommand that reads the ledger and reports on it. Nothing here calls
the model or RCON; it is pure arithmetic over JSONL already on disk.

| metric | computed from | decision it drives |
|---|---|---|
| cache hit ratio per question | `cache_read_tokens` over `input_tokens + cache_read_tokens`, summed across a question's rounds | whether the system prompt prefix (Decision 1) is still byte stable; a drop is the alarm |
| rounds histogram | count of questions at each `rounds` value | whether `max_rounds` (6) is still the right ceiling, and whether the briefing is cutting rounds the way Decision 1 intends |
| tool frequency | count of each tool `name` across every round's `tool_calls` | which tools are hot enough to matter for cost, which providers pull their weight |
| tool repeat count within a single round | count of a repeated tool `name` inside one round's `tool_calls` array | names the next plural tool to build (Decision 3), from evidence instead of a guess |
| refusal rate and reason | `refused`/`refused_reason` across questions, grouped by reason | which cap is binding too often: an hourly quota, the server quota, or the daily budget |
| model ms against RCON ms | sum of `ms_model` versus sum of `ms_rcon` | settles whether round trips or RCON dominate wall clock, a prediction today, not a measurement |
| cost per question, per player, per day | `cost`, grouped by question, by `asker`, by calendar day | tuning `max_cost_per_day` (default $5.00, `service/internal/config/config.go` line 45), catching one expensive player or one expensive question pattern early |
| zero-lookup share of questions | count of `zero_lookup` true, over questions where `briefing` is `on` | the free tier's own KPI (Decision 13): how much of the server's traffic the briefing is already answering for free, measured only where the briefing actually ran |
| briefing cost against rounds saved | over questions where `briefing` is `on`: `briefing_tokens` (the byte-per-four estimate, not a reported figure) summed at the cached-read rate, against the count of `zero_lookup` questions among them, each one a round (or several) a lookup would otherwise have cost | the only way Decision 1's own cost argument, that a briefing on every question is cheaper than the round it exists to prevent, can be checked instead of assumed |
| ask-back rate and its resolution rate | `asked_back` share of questions, and among those, the share with a later `awaiting_reply_resolved` row in the same `session_key` | whether the anchor design (Decision 7) is cutting ask-backs at all, and whether a fired one gets answered before the session idles out |

Two of these are load-bearing, not merely interesting. Tool repeat count
within a round turns "the model called `current_research` once per force,
21 times" from an anecdote into a standing report: whichever tool tops it
is the next one to make plural, built from what questions actually asked
rather than a guess at what they might ask. Cache hit ratio per question is
the regression alarm: the system prompt prefix has to stay byte stable for
the cache to hold, and a ratio that drops on a release is the same signal
that would have caught the 2026-09-12 asker-line regression the day it
shipped, not weeks of players saying answers felt slower.

A third pair is what makes the free tier and the briefing itself
falsifiable rather than merely asserted. Both rows report only over
questions where `briefing` is `on`, since a question the briefing never
reached, whether the operator switched it off or the attempt itself
failed, says nothing about how well the briefing works. Zero-lookup share
is the free tier's own KPI within that set: the fraction of those
questions the briefing already answered before a single tool call.
Briefing cost against rounds saved is the one number that can overturn the
briefing's founding argument, that paying for a snapshot on every question
is cheaper than the round it exists to prevent: if the briefing's summed
cost ever outgrows what the rounds it avoided would have cost, the
argument no longer holds, and the ledger is what would show it. Ask-back
rate and its resolution rate is the anchor design's own report card: a low
rate says anchors are doing their job; a high rate paired with a low
resolution rate says the ask-back reaches the player but the reply is
arriving after the session has already idled out.

## 5. What the ledger must never do

It writes after an answer is already decided, never before, and never on
the path that decides it. A slow write, or a full disk, delays nothing a
player sees and fails no question: the write is best effort, its own error
goes to the service's own log, never to the asker, and the answer already
rendered stands either way.

It carries what Decision 10 and R13 already put in the model's hands: chat
text and player names. It must never carry what the model and the tools
never see: the OpenRouter key, the RCON password, or any other operator
secret from config. Clipped arguments are clipped for size, not for
redaction; a tool contract that could ever put a secret into an argument is
the bug to fix, not a field to strip out after the fact here.

And it must never grow past what the operator chose. One file a day,
arguments and results already bounded by the same caps that bound them
everywhere else in the service, disk given back only when the operator
deletes a file old enough to spare. No self-inflicted unbounded log, and
nothing that quietly outgrows the kilobyte-a-question estimate this file
gives.
