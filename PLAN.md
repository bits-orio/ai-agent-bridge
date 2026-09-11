# AI Agent Bridge (AAB): plan

Status: Phases 0, 1 and 2 built and passing the rig harness on 2.0.77 with the
scripted model, 2026-09-10. No live model call has been made yet (no API key on
the development machine). The design brief is under `docs/design/`.

AAB is two halves. A Factorio companion mod exposes a tiny protocol over a
console command. A Go service, one per server, drives that protocol over RCON:
it picks up questions players ask in chat, runs an AI agent whose tools are
bounded reads of live game state, and sends a typed answer back into the game.
Other mods add tools by exposing one function. The companion knows nothing
about them, nothing about the service, and nothing about any team mod.

## Core architectural decisions

Each decision has a fuller record in `docs/adr/`.

**1. Pull, over RCON. Nothing runs in the game until a question arrives.**
- The companion registers no periodic handler. Every read happens inside the
  console command the service invokes, on an already-slow RCON round trip.
- The service is the only party that polls: the questions still unanswered, a
  page at a time, at a short interval, against a pure-read operation. The
  companion decides what is still pending, so the service keeps no cursor and a
  restart resumes instead of replaying.
- Works against a hosted server with no shell on the box, because RCON is the
  only path in and out that every host exposes.
- Removes the whole `on_load` and registry problem: nothing is discovered or
  stored at load time.

**2. Three seams, each the smallest thing that works.**
- Tools by probe: a provider puts a zero-argument `agent_tools_v1` function on
  any remote interface it owns. The companion scans `remote.interfaces` for it
  on every turn. No registration, no storage, no dependency either way.
- Questions by a frozen interface: `ai-agent-bridge-v1` with `ask` and
  `get_event_id`. The Discord bridge, or any mod, calls in.
- Answers by event: `on_answer`, raised for any subscriber. The companion
  never learns who is listening.

**3. Typed artifacts. The model fills a shape; the companion renders it.**
- Shapes: summary, comparison, list, table, notice. Each has hard size caps.
- Formatting leaves the model entirely. Output shrinks, every answer looks the
  same, and player-typed text inside a result cannot change the layout.
- Chat rendering first, a popup frame second. Rich text with item icons.

**4. History lives in the service.**
- The companion appends deaths, alerts, chat, joins, research and rockets to
  `events.jsonl`, server-side only, exactly as the Discord bridge does.
- The service tails that file, locally or over SFTP, into SQLite and keeps the
  whole history of a save. "Production since I last died" is a tool.
- Production history itself comes from the engine's flow statistics, read live.

**5. The operator brings the key and picks the model.**
- One Anthropic API key, one model choice, one binary. Cost per session is
  reported on the control API, never hidden.
- The top model calls tools directly by default. Sub-agents on a cheaper model
  are an option the planner takes for multi-part questions, not the default.

**6. Go for the service, reusing the Discord bridge.**
- The file tailer with local and SFTP transports, the reconnecting RCON
  client, the config loader with its effective-config snapshot, the `.env` and
  log helpers, and the whole release pipeline move over unchanged.
- The official Anthropic Go SDK carries a tool runner for the agent loop.
- One binary per platform keeps the bridge's deployment menu: bare metal,
  sidecar container, standalone container, compose.

**7. The protocol is the product.**
- `aab-rpc-v1` is documented in the companion's README and frozen the way
  `open-discord-bridge-v1` and `mts-v1` are. Additive changes are safe;
  breaking changes ship under a new name beside the old one.
- The Go service is the reference client. A Python script, an MCP server or a
  hosted service can drive the same companion without touching it.

## The protocol, aab-rpc-v1

One console command, `/aab-rpc <json>`. One JSON object in, one JSON object
out through `rcon.print`. Every reply is `{"ok":true,"r":...}` or
`{"ok":false,"e":"<code>","m":"<detail>"}`.

| op | request | reply `r` | writes storage |
|---|---|---|---|
| `status` | `{}` | protocol version, mod version, tick, player count, pending question count, highest question id issued (`last_id`) | no |
| `tools` | `{}` | sorted list of `{iface, v, tools}` per provider, manifests verbatim | no |
| `providers` | `{}` | sorted list of `{iface, v, tools: [names]}`, one small row per provider | no |
| `manifest` | `{i}` | one provider's manifest verbatim | no |
| `call` | `{i, f, a}` | the provider's return value, plain data | no |
| `poll` | `{after?, limit?}` | unanswered questions with id greater than `after` (default 0), oldest first, at most `limit` of them (default 16, max 64) | no |
| `answers` | `{after?, limit?}` | answered questions with id greater than `after`, each with the lines the asker saw | no |
| `answer` | `{qid, artifact}` | `true` | yes: validates, renders, then marks answered and raises `on_answer` |

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_result`, `too_large`, `no_question`, `bad_artifact`.
`bad_artifact` is the one refusal a client must not retry: the artifact itself
is wrong, so the question stays pending and re-answerable with a better one.

`call` checks that the manifest lists `f` and that `remote.interfaces[i][f]`
exists before `pcall(remote.call, i, f, a)`. A result above the byte cap is
refused, never truncated. The cap is measured in Phase 0.

Remote interface `ai-agent-bridge-v1`:
- `ask{ text, player_index?, force? }` returns a question id.
- `get_event_id("on_answer")` returns the session's event id. Fetch it every
  session from a one-shot `on_nth_tick(1)` armed in `on_init` and `on_load`.

Probe: `agent_tools_v1()` returns `{ v = 1, tools = { <fn> = { desc, params? } } }`.
`params` entries read `<type>[!] <description>` with type one of string,
integer, number, boolean, and a trailing `!` meaning required. `force` is
reserved: the service injects it on every tool, a provider never declares it.

Player-facing: `/ask <question>`. A chat prefix is a setting, off by default.

## Artifacts, v1 shapes

- `summary`: up to three lines.
- `comparison`: two named columns, up to five rows.
- `list`: up to ten rows, one line each.
- `table`: up to five columns, up to eight rows.
- `notice`: one line, a warning or confirmation.

## Phases

**Phase 0, checks. Done 2026-09-10, five of six recorded in TESTING.md.** A companion skeleton whose rpc command carries test
operations, and a Go binary that can send one command and print the reply.
Six one-line tests on the dev rig, each recorded in `TESTING.md` with its
result: `rcon.print` returns to the caller; `player_index` is nil over RCON;
a storage write and `raise_event` inside an RCON command replicate to a
second client; `pcall(remote.call, ...)` catches a provider error; the reply
size at which RCON truncates; whether `local-rcon-socket` enables single-player
use. Validates: every transport assumption the design rests on.

**Phase 1, the loop. Built 2026-09-10, verified with the fake model.** `/ask` in game, the poll and answer operations, five
engine tools (forces, players, research, production rate, top items), the
agent loop on the Anthropic Go SDK, one `summary` artifact printed to chat.
Validates: a real question answered in game in under ten seconds on a
multiplayer server, with the cost of that question visible to the operator.

**Phase 2, depth. Built and reviewed 2026-09-10; thirteen engine tools; the popup still needs human eyes.** The popup renderer and the remaining shapes. The probe
with one example provider in a separate test mod. The event file, the SQLite
history and the history tools. Per-player follow-up context with a TTL.
Round and token caps, per-player quota. Validates: a third mod adds a tool
without touching this repository, and "since I last died" answers correctly.

**Phase 3, operators.** The wizard, sidecar and egg, compose, the AleForge
guide, the portal page, `1.0`. Validates: an operator with no shell installs
it from the hosting panel.

**Later.** The Discord bridge as a question source and answer sink.
Permissioned acting tools. Sub-agents on a cheaper model.

## Open questions

1. **`/ask` may already be taken by another mod.** Current plan: register
   `/ask` under `pcall`, fall back to `/aab-ask`, and say which one is live in
   `status`.
2. **Chat trigger.** Current plan: the command ships first; a chat prefix
   setting arrives in Phase 2, off by default so ordinary chat never reaches
   the model.
3. **RCON size caps.** Measured 2026-09-10 on 2.0.77: replies up to 4 MB
   arrive complete in one packet and commands up to 1 MB are accepted, no
   truncation either way. The companion's cap is therefore
   a token-budget choice. Resolved: tool results and the legacy tools op are capped at 8000 bytes,
   one manifest at 32 KB, other reads at 64 KB, refused above, never truncated.
4. **Command handlers may block the simulation for every player while they
   run.** Current plan: measure in Phase 0; if true, each tool gets a hard
   time budget in Lua as well as a byte cap.
5. **Single-player.** `local-rcon-socket` is not a launch flag (checked against
   `factorio --help` on 2.0.72 and 2.1.17). Another portal mod's setup notes
   describe it as an entry in the client's settings under "The rest". Current
   plan: look for that entry in Phase 0, and separately test whether a graphical
   client launch accepts `--rcon-port`. Multiplayer servers are the first audience.
6. **Prompt injection through tool results.** Team names, player names and
   chat are player-typed and flow into the model as data. Current plan: the
   system prompt labels every tool result as untrusted, and artifacts are
   structured so injected text cannot change layout or trigger actions.
7. **Model providers.** Current plan: the Anthropic SDK only through `1.0`,
   behind a small interface in the agent package so a second provider is an
   addition rather than a rewrite.
8. **Runtime-global settings cannot be written from RCON.** `settings.global`
   writes from a console command are refused by the engine (measured 2026-09-10).
   Current plan: the harness seeds `mod-settings.dat` before the map exists;
   operators change settings in the map settings dialog as usual.
9. **Cost per question.** Measured 2026-09-10 on the first live run: four to
   thirteen cents each on claude-opus-5, almost all of it a 6,500-token fixed
   prompt re-sent uncached every round. Resolved in the service: cache
   breakpoints on the system prompt and the newest user block, cache tokens
   priced, claude-sonnet-5 as the default, one-line tool descriptions, a cap
   on output tokens per turn and a cut on long tool results
   (docs/design/phase1-2-spec.md, "Cost contract"). Still to measure: the
   same questions after the change.
