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
