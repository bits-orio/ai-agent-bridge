# Phase 3 contract: sessions, chat scope, OpenRouter

Fixed contracts every builder codes against, in the same spirit as
[phase1-2-spec.md](phase1-2-spec.md). CONTEXT.md and PLAN.md still rule;
this file pins what they leave open. Three parts: sessions (how follow-up
questions share context and what that may cost), chat scope (who may hear
an answer, decided by a mod the companion never names) and OpenRouter (the
model backend the service is built around from here on).

Owner decisions this file records, 2026-09-10: every session is shared by
the whole server unless the asker's team is in a private chat mode; a team
that leaves private mode never continues its private session in the open;
spectators use and start global sessions like anyone else; the bot's
answers carry the same channel tag players' own lines carry; models are
chosen through OpenRouter so Claude and cheaper models are one config line
apart.

## 1. Sessions

A session is a short, shared transcript of questions and the answers players
saw. It is the only thing carried from one question to the next.

### What a session holds

- Exchanges, oldest first. One exchange is: who asked (player name, or the
  asker label for a mod), the question text (already capped at 400 bytes),
  and the answer as rendered, that is the lines the companion printed
  joined with newlines and clipped to 400 characters. Never a tool result,
  never a model's reasoning. The renderer is the compaction.
- `started_at`, `last_at`, the scope key and the optional name.

### Cost bound

Each exchange is at most about 100 tokens, so a session adds at most
`session_max_exchanges` times that to a question's input. At the defaults
below that is about 1,000 tokens: $0.002 on Sonnet 5, $0.001 on Haiku 4.5,
a fraction of a cent on DeepSeek. No model-written summaries: they cost
output tokens, the expensive direction, and the rendered answers already
are the summary. The session text sits after the cached rules and tools,
inside the per-round cache breakpoint, so a round two or three of the same
question reads it from the cache.

### Identity

A session key is the scope key (part 2) plus an optional name:

| asked as | key |
|---|---|
| `/ask what is our plate rate` from a global player | `global` |
| `/ask #iron what is our plate rate` from a global player | `global#iron` |
| the same two from a player whose team is in private mode | `team-3` and `team-3#iron` |

Equal keys share a session. A named session is server-wide within its
scope: anyone in global mode may join `global#iron`, a spectator included,
and one player may use `#iron` and `#oil` at the same time. Names are
lowercase `[a-z0-9_-]`, 1 to 16 characters, typed with a leading `#`.

### Lifetime

- A session ends after `session_idle` (default 3m) without a question, or
  `named_session_idle` (default 30m) for a named one, or when it reaches
  `session_max_exchanges` (default 10). The next question with that key
  starts a new session.
- `new` in the grammar below ends the session for that key and starts a
  fresh one.
- Ended sessions are dropped from memory. Nothing about sessions is written
  to disk; the history database is unchanged.

### Grammar

The service parses the question text; the companion sends it verbatim, from
`/ask` and from the chat prefix alike.

```
[new] [#name] question      ask, continuing or starting the session
new [#name]                 start a fresh session, no question: confirmation notice
sessions                    list the sessions in your scope: name, age, exchanges, last asker
```

`sessions` and a bare `new` never call the model and cost nothing.

### What the model sees

The user turn starts with the session, when there is one:

```
Earlier in this session, oldest first:
Alice asked: what forces are there?
Answer: Forces: player (3 online), enemy, neutral
Bob asked: and the other one?
Answer: ...

Question from Bob: ...
```

Ordering: questions with the same session key are answered one at a time,
in id order, so a follow-up always sees the answer it follows. Different
keys run concurrently as they do today.

### Marking a fresh session

The artifact JSON gains an optional `session` object the service fills:
`{name: "iron" | "", fresh: true | false}`. The companion renders a fresh
session as a marker after the tag (part 2): `(new session)` or
`(new session #iron)`. An old companion ignores the field, which the
Phase 1 contract already allows.

### Config (`agent`)

| key | env | default |
|---|---|---|
| `session_idle` | `AAB_SESSION_IDLE` | `3m` |
| `named_session_idle` | `AAB_NAMED_SESSION_IDLE` | `30m` |
| `session_max_exchanges` | `AAB_SESSION_MAX_EXCHANGES` | `10` |

`memory_ttl` and the per-player memory it governed are removed; sessions
replace them. `questions_per_player_per_hour` stays per player.

## 2. Chat scope

The companion asks one question when a question is created: who may hear
this? It never asks who is on which team.

### The probe

Any remote interface may expose a zero-side-effect function named
`chat_scope_v1`. The companion scans `remote.interfaces` for it the same
way it scans for `agent_tools_v1`, on every question, storing nothing
(CONTEXT.md invariant 3).

```lua
chat_scope_v1(player_index, text) -> {
  key      = "global",              -- session pool identity; equal keys share sessions
  private  = false,                 -- true: answers and sessions stay inside the audience
  audience = { force = "team-3" },  -- or { players = {1, 5, 9} }; read only when private
  label    = "Team 3",              -- optional, shown by `sessions` and in the log
  tag      = "[color=...][GLOBAL][/color]",  -- optional rich text, printed verbatim
}
```

- `text` is the line as typed: the whole chat line for the chat prefix, the
  parameter for `/ask`. A provider with a "shout" rule applies it here.
- No provider, or a nil return, means `{key = "global", private = false}`.
- Several providers: a private answer beats a global one. Two private
  answers with different keys: the interface whose name sorts first wins
  and the companion logs the conflict once per session.
- Spectators and anyone else without a team get global from the provider,
  so they use and start global sessions like every other player.

### Fixed at ask time

The companion stores `scope`, `private`, `audience` and `tag` on the
question row when the question is created. The answer prints to the stored
audience, however the channel has changed since. A team that flips to
global mid-question still gets that answer privately, and the reverse.

### Printing

- Global: `game.print`, everyone on the server, since a global session is
  shared by everyone. The current asker-only printing stays available as
  the companion setting `aab-answer-audience = asker` for servers that want
  it; `server` is the default.
- Private with `audience.force`: that force's `print`. With
  `audience.players`: each connected one.
- The popup style is unchanged and only ever shows to the asker; the chat
  copy still goes to the audience, so others can follow up.
- The "Got it, thinking about" echo stays asker-only.
- The first answer line is laid out like a player's own chat line, name
  then badge then colon: `[AI Agent Bridge] [TEAM]: Forces: ...`, with the
  tag exactly as the provider gave it and nothing when there is none.
  Continuation lines are indented as today.

### Protocol and interface additions (additive)

- `poll` rows gain `scope` (string) and `private` (boolean). The service
  uses only the key; audiences never leave the companion.
- `ai-agent-bridge-v1.ask(spec)` accepts an optional `scope` table of the
  same shape as the probe's return, for a mod asking on behalf of a
  channel of its own. Without it the question is global.
- Nothing in the `answer` operation changes.

### What a privacy mod implements

One function on its own interface. For a team-chat mod that keeps a
per-team channel and stamps a badge on every line, the function returns the
team's force name as the key when the channel is team-only and `global`
otherwise, the team force as the audience, and the same badge string its
chat lines carry. Sessions then separate by construction: a team leaving
private mode changes its key, so the next question starts a global session
that has never seen the private one, and the private session idles out
untouched.

## 3. OpenRouter

Decision: ADR 0007. The service calls OpenRouter's chat completions API,
`https://openrouter.ai/api/v1/chat/completions`, with an in-house client
(net/http, encoding/json), no SDK. The direct Anthropic client stays in
the tree as a selectable backend and gets no new features; the fake model
stays for the harness.

### Config

```yaml
model:
  provider: openrouter            # openrouter (default) | anthropic | fake
  id: anthropic/claude-sonnet-4.6 # any id OpenRouter lists that supports tools
  fallbacks: []                   # optional: OpenRouter `models` list tried in order
  reasoning: off                  # off (default) | model | effort: low|medium|high
  cache_ttl: 1h                   # 1h | 5m, for the explicit breakpoints on Anthropic routes
  max_output_tokens: 4096
  route:                          # optional, passed through as OpenRouter's `provider` object
    data_collection: deny
openrouter:
  api_key_env: OPENROUTER_API_KEY
```

`anthropic.*` keeps working for `provider: anthropic`. `.env.example` gains
`OPENROUTER_API_KEY=` beside the Anthropic placeholder. The example config
lists a few ids with their prices on the day it was written and says how to
check the live list (`GET /api/v1/models`, keep the ones whose
`supported_parameters` include `tools`).

### Request

- Headers: `Authorization: Bearer`, `HTTP-Referer` pointing at the GitHub
  repository, `X-OpenRouter-Title: AI Agent Bridge`.
- `messages`: the system prompt as one text part carrying
  `cache_control: {type: "ephemeral", ttl}`; the user turn as text parts,
  the last one carrying a five-minute `cache_control`; assistant turns with
  `tool_calls` (`arguments` is a JSON string) and, when the route returned
  them, `reasoning_details` passed back verbatim and in order; one
  `role: tool` message per result with its `tool_call_id`, in the order the
  calls were made.
- `tools`: `{type: "function", function: {name, description, parameters}}`
  from the same tool definitions as today; `tool_choice: "auto"`;
  `parallel_tool_calls: true`.
- `reasoning`: `{enabled: false}` for `off`; omitted for `model`;
  `{effort}` when an effort is set. Routes that ignore a field ignore it;
  the trace line shows what came back.
- `max_tokens` from `max_output_tokens`; `models` from `fallbacks`;
  `provider` from `route`.
- Caching on DeepSeek, OpenAI, Gemini and the other automatic routes needs
  nothing from the service; the explicit breakpoints are for Anthropic
  routes and are harmless elsewhere.

### Response

- `choices[0].message`: `content` (text), `tool_calls`, `reasoning` or
  `reasoning_details`. Reasoning entries become `model.BlockReasoning`
  blocks holding the entry's raw JSON, kept in the conversation and sent
  back unchanged; the agent loop never reads them.
- `finish_reason`: `tool_calls` and `stop` map onto the loop's existing
  stop reasons; `length` maps onto max tokens.
- `usage`: `prompt_tokens`, `completion_tokens`,
  `prompt_tokens_details.cached_tokens` and `cache_write_tokens`,
  `completion_tokens_details.reasoning_tokens`, and `cost`. `cost` is
  authoritative: `Result.CostUSD` takes it when present, the price table is
  used only for the direct Anthropic backend. `model.Usage` gains
  `ReasoningTokens`; the answer and trace log lines print it.
- Errors: a non-2xx status with OpenRouter's error JSON becomes the Step
  error; one retry after a second on 429 and 5xx, then the asker keeps
  their quota slot as today.

### Tests

- Unit: an `httptest` server that checks the request JSON shape (parts,
  breakpoints, tools, reasoning field per setting, headers) and replies
  with recorded response shapes: a tool call round, a text round, a
  reasoning_details round, a `length` finish, a 429 then success, an error
  body.
- Harness: unchanged, on the fake model.
- Live: the owner's OpenRouter key, one question on a Claude route and one
  on a DeepSeek route, recorded in TESTING.md with the `cost` OpenRouter
  reported.

## Build order

1. Sessions in the service, with `sessions` and `new`, unit tests for the
   grammar, the idle cut, the cap, ordering per key; the per-player memory
   removed.
2. Chat scope in the companion: probe, question-row fields, audience
   printing, tag layout, the `aab-answer-audience` setting; fake-game
   checks with a test scope provider beside the test tool provider.
3. Poll row and interface additions; the `session` artifact field and its
   marker.
4. OpenRouter client, config, docs; the Anthropic client left as is.
5. Harness scenarios: idle cut, named session shared by two askers, a
   private question printed to the force only (needs the test scope
   provider), the fresh-session marker.
6. README, QUICKSTART and CONTEXT.md words: Session, Scope, Scope provider,
   Audience, Tag.
