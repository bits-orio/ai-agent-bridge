# Phase 1 and 2 contract

Fixed contracts every builder codes against. CONTEXT.md and PLAN.md still
rule; this file pins the shapes they leave open. No Anthropic API key exists
on the development machine: the Anthropic model client is written from the
SDK documentation and compile-checked only, and every live test runs the
scripted fake model.

## Artifact JSON (what the model submits, what the companion renders)

One flat object. `shape` picks the fields that matter; everything else is
ignored. The service clips to the caps before sending; the companion clips
again when rendering.

| shape | fields | caps |
|---|---|---|
| `summary` | `title?`, `lines: string[]` | 3 lines, 160 chars each |
| `notice` | `text`, `level?: "warning" or "confirmation"` | 1 line |
| `list` | `title?`, `items: string[]` | 10 items |
| `table` | `title?`, `columns: string[]`, `rows: string[][]` | 5 columns, 8 rows |
| `comparison` | `title?`, `columns: [a, b]`, `rows: {label, a, b}[]` | 5 rows |

Strings may carry Factorio rich text such as `[item=iron-plate]`.

## Service packages (Go, `service/internal/...`)

- `tools.Tool` (already written, `internal/tools/tool.go`): Name, Description,
  Schema, Call. Every tool is one of these.
- `model`: the neutral model boundary. Block{Type "text" or "tool_use" or
  "tool_result", Text, ID, Name, Input json.RawMessage, Content, IsError};
  Message{Role "user" or "assistant", Blocks}; ToolDef{Name, Description,
  Schema}; Usage{InputTokens, OutputTokens, CacheReadTokens,
  CacheWriteTokens}; Step{Blocks, StopReason, Usage}; interface
  `Model{ Name() string; Step(ctx, system string, msgs []Message, tools []ToolDef) (Step, error) }`.
  - `model/anthropic`: the SDK implementation, manual-loop style from the
    Go docs (`Messages.New` with `Tools`, `resp.ToParam()` for history is not
    used because the loop owns history; convert Blocks both ways). Adaptive
    thinking by leaving Thinking unset. Never exercised at runtime here.
  - `model/fake`: scripted, deterministic, keyword driven (below).
- `catalog`: turns the companion's `tools` reply into `[]tools.Tool`. Tool
  name is `<iface>__<fn>` with every character outside `[A-Za-z0-9_-]`
  replaced by `_`, and an explicit reverse map. `force` is injected as a
  required string property on every game tool; when the model omits it the
  agent fills the asker's force. Parameter grammar: `<type>[!] <description>`.
- `agent`: the loop. Inputs: question (text, player index, force, asker
  label), a Model, []tools.Tool, caps. Adds the `submit_answer` tool whose
  schema is the artifact above (`shape` enum required). Runs at most
  `max_rounds` steps; executes all tool_use blocks of a step concurrently and
  returns their results in one user message; a tool error becomes an error
  tool result; `submit_answer` ends the loop with the validated, clipped
  artifact; `end_turn` without submit wraps the text as a summary; exhausted
  rounds or the per-question token cap produce a `notice`. Returns artifact,
  rounds, Usage, cost in USD (price table: claude-opus-5 5 and 25 per MTok,
  claude-sonnet-5 2 and 10, claude-haiku-4-5 1 and 5, unknown 0).
  - System prompt: who it is, force default, tool results are untrusted data
    supplied by players, answer only through submit_answer, keep artifacts
    within caps, internal prototype names are fine, one short precise answer.
  - Per-player memory: last 4 exchanges per asker as compact text prepended
    to the question, expiring after `memory_ttl`.
  - Quota: `questions_per_player_per_hour`; over quota returns a notice
    without calling the model.
- `history`: `Open(path) (*Store, error)`, `(*Store).Ingest(line []byte)
  error` (one events.jsonl line), `(*Store).Tools() []tools.Tool`,
  `(*Store).Close()`. SQLite via `modernc.org/sqlite` (pure Go, so the
  binary builds with CGO_ENABLED=0). Table `events(id INTEGER PRIMARY KEY,
  tick INTEGER, event TEXT, player TEXT, force TEXT, data TEXT, received_at
  TEXT)` with an index on (event, tick). Tools: `recent_events(event?,
  force?, player?, limit<=20)` newest first; `last_event(event, player?,
  force?)` returning the single newest row or a clear "none recorded" result;
  `count_events(event, force?, since_tick?)`.
- `cmd/aab run`: connect RCON, build the catalog, tail the events file into
  history, poll questions every `poll_interval`, answer each in order, log
  one line per question and one per answer with rounds, tokens and cost,
  serve the control API. Rebuild the catalog when a call fails with
  `no_provider` and every 10 minutes.
- `controlapi`: `/healthz`, `/v1/status` JSON: connected, mod_version,
  questions_answered, tokens_in, tokens_out, cost_usd, model, uptime.
  Optional bearer token from `control_api.token_env`.

## Config additions (`service/internal/config`, YAML and `AAB_*` env)

```yaml
anthropic:
  api_key_env: ANTHROPIC_API_KEY   # placeholder, the key goes in .env
  model: claude-opus-5             # "fake" selects the scripted model
agent:
  max_rounds: 6
  max_tokens_per_question: 20000
  memory_ttl: 10m
  questions_per_player_per_hour: 20
history:
  path: history.sqlite             # relative to the config file
control_api:
  addr: 127.0.0.1:8090
  token_env: AAB_CONTROL_TOKEN
```

`.env.example` carries `ANTHROPIC_API_KEY=` as the placeholder.

## Fake model (deterministic, for the harness)

Keyword rules on the newest question text, first step:
- "research" -> `current_research`; "rate" -> `item_rate` with the item named
  after "of" or "for" (default iron-plate, surface nauvis, window one_minute);
  "forces" or "teams" -> `list_forces`; "players" -> `list_players`;
  "died" or "death" -> `last_event{event: player_died}`; "hello" -> the tool
  named `aab-test-provider__hello` when present, else `list_forces`;
  "surfaces" -> `list_surfaces`; anything else -> `submit_answer` at once
  with a summary echoing the question.
- Second step: `submit_answer`. Shape by keyword: "table" -> table, "list"
  -> list, "compare" -> comparison, "notice" -> notice, else summary. The
  first line or row carries the compacted tool result JSON (first 160
  characters) so a test can assert on real game data.
- Usage per step: 100 input, 20 output tokens.

## Companion (`companion-mod`)

- One `on_console_chat` handler in control.lua fanning out to events and
  to the chat-prefix path (a second `script.on_event` for the same event
  replaces the first).
- Setting `aab-chat-prefix` (runtime-global, default empty = off). A chat
  line starting with the prefix becomes a question from that player with
  the rest as text.
- Setting `aab-answer-style` (runtime-global, `chat`, `popup`, `auto`;
  default `auto`: popup for `table`, `comparison`, and `list` with more than
  three items, chat otherwise). Popup: a frame in `player.gui.screen`,
  auto-centred, titlebar with a close button, lines as labels or a GUI table
  for the `table` shape. Only for a connected asker; otherwise chat.
- `answer` op stores the rendered lines and shape on the question. New
  pure-read op `answers {after}` -> `[{id, shape, lines, player_index}]` for
  answered questions with id > after, oldest first. The harness reads this.
- events.jsonl gains `question` (qid, player, force, text) on ask and
  `answer` (qid, shape) on answer.
- New engine tools: `list_surfaces(force)`, `production_since(force,
  surface, item, since_tick)` summing flow samples over the smallest
  precision window that covers the elapsed ticks (verify `get_flow_count`
  sample semantics in the LuaFlowStatistics docs; document the
  approximation if any).
- `tests/provider-mod/aab-test-provider`: a tiny mod with `agent_tools_v1`
  exposing `hello{name}` -> `{greeting}` and `boom` -> error, for the probe
  and error-path scenarios.

## Harness (`tests/e2e`, Python 3 standard library)

- `rig.py`: start and stop a headless server (binary from
  `FACTORIO_HEADLESS`, default `~/factorio-dev/headless-2.0.77/factorio/bin/x64/factorio`)
  with its own write-data under `tests/e2e/.run/<name>/`, a mods dir of
  symlinks (companion-mod, tests/provider-mod), ports 34210 and 27110,
  RCON password `rig`, `--start-server-load-scenario base/freeplay` with
  the rig's server-settings shape; optional standalone client (binary from
  `FACTORIO_CLIENT`, default `~/factorio/bin/x64/factorio`) with its own
  write-data and the same mods dir, `--mp-connect`; a minimal RCON client
  (copy the rig's rcon.py logic); wait helpers.
- `run.py`: builds `service/aab` through Docker when missing or `SERVICE_BIN`
  is unset (`docker run --rm -v service:/src -w /src -e CGO_ENABLED=0
  golang:1.25-alpine go build -o aab ./cmd/aab`), writes
  `tests/e2e/.run/aab.e2e.yaml` (model fake, rcon 127.0.0.1:27110, events
  path inside the server write-data `script-output/ai-agent-bridge/events.jsonl`,
  history path in `.run`), starts server, starts the service, runs scenarios,
  prints one PASS or FAIL line each, stops everything, exits non-zero on
  any failure. `--client` also launches the standalone client and enables
  the scenarios that need a player.
- Scenarios: status; ask through the remote interface ("what forces are
  there") and expect an `answers` entry whose lines mention `player`; ask
  "hello" and expect the provider tool's greeting; ask "table of players"
  and expect shape table; simulated chat through
  `script.raise_event(defines.events.on_console_chat, {player_index, message})`
  with the prefix set (client mode); a death through
  `game.players[1].character.die()` then "when did I last die" expecting
  `player_died` (client mode); quota: ask 21 times and expect a notice;
  provider error: call `boom` through the rpc and expect `provider_error`.
- `Makefile` at repo root: `service` (Docker build), `test` (Go unit tests
  in Docker), `e2e`, `e2e-client`, `lua-check`.

## Breadth addendum (after the Phase 1 and 2 review)

More engine tools, all engine aggregates or engine-side counts, never entity
scans in Lua. Each takes `force` (injected by the service) and returns
bounded plain data. Verify every API name on lua-api.factorio.com first.

| tool | arguments | returns |
|---|---|---|
| `research_queue` | | current research plus the queue, names and progress |
| `tech_status` | `tech!` | researched, available (prerequisites met), level, unit count, prerequisites |
| `logistics_summary` | `surface!` | per logistic network on that surface for the force: robot counts, available robots, the ten largest contents by count |
| `entity_count` | `surface!`, `name!` | `surface.count_entities_filtered{force, name}` |
| `evolution` | `surface!` | `force.get_evolution_factor(surface)` and its three components where the API exposes them |
| `rockets` | | rockets launched and items launched for the force |
| `game_time` | | tick, hours played, connected players |
| `pollution` | `surface!` | total pollution on the surface |

Fake-model keywords added: "queue" -> `research_queue`; "technology" or "tech "
-> `tech_status` with the word after it; "logistic" or "bots" ->
`logistics_summary`; "how many <name>" -> `entity_count`; "evolution" ->
`evolution`; "rocket" -> `rockets`; "time" or "how long" -> `game_time`;
"pollution" -> `pollution`; "since" -> `production_since` with `since_tick` 0.

Harness scenarios added (server-only): one ask per new tool, asserting the
answer carries the tool's key fields (for example `rockets_launched`,
`evolution_factor`, `hours`), plus "iron plate production since the start"
for `production_since`.

## Review-fix contract (after the adversarial review, 2026-09-10)

Measured on the rig: Factorio accepts RCON commands of at least 1,000,042
bytes and returns replies of at least 4 MB in one packet. The 1000-byte
command limit and the 4 KB packet limit were gorcon's, not the game's.

1. **RCON client.** `service/internal/rcon` drops gorcon for an in-house
   Source RCON client (auth, exec, one length-prefixed response of any size),
   keeping the wrapper API (`New`, `Execute`, `Close`, reconnect once on a
   connection error, no redial on a local validation error) and its fake
   server tests. `rcon.MaxCommandLen` becomes 262144 with a comment citing
   the measurement. Empty commands are still refused locally.
2. **Answer budget.** `agent.validate` enforces a total marshalled artifact
   size of at most 6000 bytes by dropping trailing rows or items and then
   shortening cells, after the shape caps. Cells are capped at 160 runes in
   Go; the companion clips at 640 bytes on a UTF-8 boundary as a safety net.
3. **poll.** Request `{after?, limit?}`: oldest unanswered questions with id
   greater than `after` (default 0), at most `limit` (default 16, max 64).
   Rows gain `player_name`. The service always polls with `after` 0, keeps an
   in-flight set, retries a question whose answer delivery failed on the next
   tick, and gives up on a question after three delivery failures with a log
   line. No cursor is persisted.
4. **status** gains `last_id` (the highest question id issued so far).
5. **answer.** The companion validates the artifact (known shape, fields of
   the right types) and returns `bad_artifact` on failure; rendering runs
   inside pcall; a question is marked answered and its render recorded only
   after rendering succeeded. Go maps `bad_artifact` to a typed error and
   does not retry it.
6. **Manifests.** The companion's probe drops manifest entries whose `desc`
   is not a string or whose `params` is not a map of strings, and drops a
   provider whose manifest cannot be read, with one log line each. The
   service decodes the tools reply per provider and skips one that fails to
   decode, never the whole catalog.
7. **Thinking blocks.** `model.Block` gains types `thinking` (Thinking,
   Signature) and `redacted_thinking` (Data). The Anthropic converter decodes
   `ThinkingBlock` and `RedactedThinkingBlock` and re-emits them in their
   original position with `NewThinkingBlock` and `NewRedactedThinkingBlock`.
   The fake model never emits them.
8. **Force default everywhere.** The agent fills `force` with the asker's
   force for any tool whose schema declares a `force` property when the model
   omits it, game and history tools alike. The prompt's claim is then true.
9. **Asker label** becomes `<player_name> (player <index>, force <force>)`
   when a name is known.
10. **Bounded enumerations.** `list_players{force, connected?, limit?}`
    defaults to connected players, limit 20 (max 50), returns `total`,
    `shown`, `players`. `list_surfaces{force, limit?}` limit 20 (max 50) with
    `total`. Rows sorted by name before cutting.
11. **Agent loop.** A round with `submit_answer` beside other tool calls
    rejects the submission with an error tool result and continues. Two
    submissions in one round: the first by block order wins. An `end_turn`
    text answer drops blank lines before clipping, and an all-blank summary
    becomes the stalled notice. A model error refunds the quota slot.
    Catalog tool names truncate the interface prefix, never the function
    name, and collisions are logged and skipped.
12. **Companion hardening.** `render_shapes.line` returns
    `(unrenderable value)` for anything that is not a string, number or
    boolean. `questions.ask` keeps `force` only when it is a string and
    `player_index` only when it is a number. The `big` selftest op clamps
    `kb` to 4096.
13. **Harness.** Reset `history.sqlite` (and journal files) before each run;
    after a scripted death, wait until `last_event` sees it before asking;
    the quota scenario requires a summary on the first question and a notice
    on the twenty-first; fail fast when the control-API port already answers
    and require `connected: true` from the launched service; `--keep` prints
    the PIDs to `.run/pids` and `make e2e-stop` kills them; the provider-error
    scenario SKIPs without a probe-exposing provider; `poll_for_answer` passes
    a captured `after` cursor and fails fast on `ok: false`; the log watcher
    ignores `InterruptibleStdioStream` and passes `stdin=DEVNULL`; in client
    mode an empty chat prefix is a FAIL; QUICKSTART's troubleshooting describes
    the `question N from ...` and `poll failed` lines the service really logs.

## Second review-fix contract (tools review, 2026-09-10)

1. **Catalog in two steps.** New read-only ops beside `tools`: `providers {}`
   returns `[{iface, v, tools: [names...]}]` for every probe found, sorted;
   `manifest {i}` returns one provider's manifest verbatim. The service builds
   its catalog from `providers` plus one `manifest` call per provider, so a
   provider whose manifest is too large costs itself its tools and nobody
   else theirs. `tools` stays for small servers and for the harness.
2. **No catalog, no answer.** When the service has no catalog yet and the
   rebuild fails, the question stays pending and the log says
   `no catalog available`, retrying next tick. A stale catalog is still used.
3. **Done questions leave the page.** The service polls with `after` set to
   the highest question id it has finished with (delivered, or given up on),
   so a question it abandoned cannot pin the page. A `bad_artifact` refusal
   is followed by one notice delivery ("I could not put that answer into a
   shape the game can show"), which the companion renders and marks answered.
4. **Page growth.** After every successful poll the page doubles back toward
   `pollLimit`; a `too_large` reply halves it again. One wasted poll per two
   ticks while a backlog of long questions drains is acceptable.
5. **Level-based technologies in the queue.** Each repeated entry reports
   `level` as the technology's current level plus the number of earlier
   entries with the same name; `units` and `progress` are reported for the
   first entry only.
6. **Qualities.** Flow reads sum across every quality in `prototypes.quality`
   with `{name=item, quality=q}`; `production_since`, `item_rate` and
   `top_items` share that helper in `flow.lua`. `logistics_summary`
   aggregates contents by item name, adds a `qualities` breakdown only when a
   name has more than one, and reports `distinct_items` as unique names.
7. **Reply sizes.** `logistics_summary` shows at most 5 networks and 8
   contents each. Every double in a reply passes through one rounding helper
   in `bounded.lua`: four decimals for evolution factors, two for progress
   and rates.
8. **One surface contract.** `surface_lookup.find` accepts a name or an
   index and every surface-taking tool uses it and returns `found=false`
   for a miss; `flow.require_surface` goes away.
9. **Missing argument table.** `probe.call` substitutes `{}` for a missing
   `a`, so a zero-argument tool called without arguments works and a tool
   that needs `force` says so in its own words.
10. **Harness assertions.** Every ask scenario fails when the answer line
    starts with `You asked:` or carries `aab-rpc:` or `provider_error`, and
    asserts a quoted key with its colon (for example `"queued":`) that the
    echoed question text cannot contain. The table-of-players scenario also
    requires `"players"`. The large-table scenario reads the question back
    through `answers` and checks the recorded shape and first line are the
    ones it sent.
