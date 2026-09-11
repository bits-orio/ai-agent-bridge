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
  and the answer exactly as rendered, every line the companion printed.
  Never cut: a half table is worse than no table, because the model reads
  it as the whole. Never a tool result, never a model's reasoning. The
  renderer is the compaction: an answer is already within the artifact caps.
- `started_at`, `last_at`, the scope key and the optional name.

### Cost bound

A session is bounded by bytes, not by cutting: when adding an exchange
would take the session past `session_max_bytes` (default 8000, about two
thousand tokens), whole exchanges are dropped oldest first until it fits.
`session_max_exchanges` (default 10) bounds the count the same way. An
answer larger than the byte cap on its own is kept alone. So a session adds
at most about two thousand tokens to a question: $0.002 on DeepSeek V4 Pro,
$0.004 on Sonnet 5, and nothing ever reaches the model half-cut. No
model-written summaries: they cost output tokens, the expensive direction,
and the rendered answers already are the summary. The session text sits
after the cached rules and tools, inside the per-round cache breakpoint, so
a round two or three of the same question reads it from the cache.

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
  `named_session_idle` (default 30m) for a named one. The next question with
  that key starts a new session. The count and byte caps never end a
  session; they drop its oldest exchanges whole, so piling on keeps working.
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

Ordering: the service answers questions one at a time in id order, as it
does today, so a follow-up always sees the answer it follows.

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
| `session_max_bytes` | `AAB_SESSION_MAX_BYTES` | `8000` |

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
- Chat only. The popup frame and the `aab-answer-style` setting are
  removed: a window opening over whatever the player was doing is an
  interruption, and chat is where the question was asked. Tables render as
  one line per row with ` | ` between cells, the column names first, as the
  chat renderer already does. Icons are sprites alone, never sprite plus
  label (owner decision, 2026-09-11): the system prompt asks for
  `[img=item.iron-plate]`, `[img=fluid.crude-oil]`, `[img=entity.x]`,
  `[img=technology.x]`, `[img=planet.x]`, `[img=quality.x]`, and the
  service rewrites any `[item=x]`, `[fluid=x]`, `[entity=x]`,
  `[technology=x]`, `[recipe=x]`, `[tile=x]`, `[virtual-signal=x]` or
  `[planet=x]` the model writes anyway into the img form, a quality suffix
  into its own quality icon. `[color=...]` still marks a warning.
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
  provider: openrouter                 # openrouter (default) | anthropic | fake
  id: deepseek/deepseek-v4-pro-0813    # any id OpenRouter lists that supports tools
  small: deepseek/deepseek-v4.1-flash  # reserved for sub-agents (PLAN.md "Later"); unused until then
  fallbacks: []                        # optional: OpenRouter `models` list tried in order
  reasoning: off                       # off (default) | model | low | medium | high
  cache_ttl: 1h                        # 1h | 5m, for the explicit breakpoints on Anthropic routes
  data_collection: deny                # deny (default) | allow: OpenRouter's provider.data_collection
openrouter:
  api_key_env: OPENROUTER_API_KEY
anthropic:
  api_key_env: ANTHROPIC_API_KEY       # only read for provider: anthropic, which uses model.id too
```

One `model` section serves every provider: `reasoning` becomes
`thinking: disabled` / adaptive with effort on the direct Anthropic client
and OpenRouter's `reasoning` object on the router, and `cache_ttl` sets the
same breakpoints on both. The `anthropic.model`, `thinking`, `effort` and
`cache_ttl` keys from the second cost addendum fold into it; the
`agent.max_output_tokens` cap stays where it is. `.env.example` gains
`OPENROUTER_API_KEY=` beside the Anthropic placeholder. The example config
lists a few ids with their prices on the day it was written and says how to
check the live list (`GET /api/v1/models`, keep the ones whose
`supported_parameters` include `tools`).

Defaults, decided 2026-09-10: DeepSeek V4 Pro is the everyday model, the
role Sonnet had, and DeepSeek V4.1 Flash is the small one for sub-agents
when they land, the role Haiku had. Listed that day on OpenRouter at
$1.05 and $3.15 per million tokens in and out for Pro, $0.30 and $1.20 for
Flash, both with tool calling.

`data_collection: deny` is the default: prompts carry players' chat and
names. OpenRouter's provider table marks DeepSeek's own endpoint as
training on prompts and every other host of these two models (Novita,
DeepInfra, Fireworks, Together, Parasail, GMICloud and the rest, eighteen
for Pro, seven for Flash on that day) as not training, so denying keeps
both models available at the same or lower prices and only drops the
first-party route. A model with no compliant host fails with a 404 that
the log shows verbatim.

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
  `{effort}` for low, medium or high. Routes that ignore a field ignore
  it; the trace line shows what came back.
- `max_tokens` from `agent.max_output_tokens`; `models` from `fallbacks`;
  `provider: {data_collection}` from `data_collection`.
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
- Harness: on the fake model, which answers "recall" with how many earlier
  exchanges the prompt carried and the first of them, so a session can be
  read from outside: follow-up, `sessions`, `new`, `#named`, and a private
  scope handed in through the interface.
- Live: the owner's OpenRouter key, one question on a Claude route and one
  on a DeepSeek route, recorded in TESTING.md with the `cost` OpenRouter
  reported.

## After the first MTS session (2026-09-11)

Three things the first team-vs-team session showed, all built:

- **The question echo goes to the audience.** `/ask` is a command, not a
  chat line, so nobody but the asker saw the question; a shared session then
  opened with an answer to a question the others never read. The companion
  now prints `[AI Agent Bridge] [TAG] Alice asked: ...` to the same audience
  the answer will reach, through the one `scripts/audience.lua` both use. A
  chat-prefix question is its own echo and gets none. The asker-only "Got
  it" line is gone.
- **Bare names become sprites at render time.** The prompt asks for
  `[img=item.iron-ore]` everywhere, columns included, and the companion
  turns any bare hyphenated prototype name the model writes anyway into its
  sprite (`scripts/sprites.lua`: item, then fluid, technology, entity; text
  inside any tag untouched; single words left as prose).
- **Force labels.** A fifth seam, `force_labels_v1`, zero-argument, on any
  interface, returning `{ [force_name] = label }`. The companion strips rich
  text and serves the merged map as the `labels` op. The service reads the
  op once per question and substitutes labels with force names in the
  question text, whole words, case-insensitive, longest label first, and
  the bare X of a "Team X" label when X is three characters or longer; the
  renderer substitutes force names back into labels in every answer line
  outside a tag. Only force names carrying a digit, hyphen or underscore
  take part on either side, so `player` is never swapped. No prompt
  paragraph, no tokens (owner decision, 2026-09-11). MTS answers the probe
  from its claimed team list.
- **Clickable tags on request.** The forced rewrite of `[item=x]` into
  `[img=item.x]` is gone. The prompt keeps sprites as the default and adds
  the exception: when the player asks about the thing itself (what an item
  is, what a recipe needs, a technology to look at), the model writes the
  clickable `[item=x]`, `[recipe=x]`, `[technology=x]`, `[fluid=x]` or
  `[entity=x]`, which opens it in game.
- **Where questions.** Two tools, `find_entities` (one force, one surface,
  by name, type or the recipe a crafting machine is set to; the engine pass
  capped at 2,000 entities, 5 positions returned by default, 10 at most,
  each with a ready `[gps=x,y,surface]`) and `locate_player`. The prompt
  says to write positions as gps tags, and, because both walk one surface,
  to ask the player which surface in a notice when the question does not
  say and the force stands on more than one; the session carries the
  follow-up.

## Build order

1. Sessions in the service, with `sessions` and `new`, unit tests for the
   grammar, the idle cut, the cap, ordering per key; the per-player memory
   removed.
2. Chat scope in the companion: probe, question-row fields, audience
   printing, tag layout, the `aab-answer-audience` setting, the popup and
   its setting removed; fake-game checks with a test scope provider beside
   the test tool provider.
3. Poll row and interface additions; the `session` artifact field and its
   marker.
4. OpenRouter client, config, docs; the Anthropic client left as is.
5. Harness scenarios: idle cut, named session shared by two askers, a
   private question printed to the force only (needs the test scope
   provider), the fresh-session marker.
6. README, QUICKSTART and CONTEXT.md words: Session, Scope, Scope provider,
   Audience, Tag.
