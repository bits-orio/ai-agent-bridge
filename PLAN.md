# AI Agent Bridge (AAB): plan

Status, 2026-11-14: a large batch of fixes and features is built on the
`review-fixes` branch, uncommitted and unreleased, on top of 1.1.1. In the
companion: the `/ask` command is optional (`aab-claim-ask-command`, with
`/aab-ask` always there), `aab-ask-hints` teaches players how to ask, the
diagnostic ops are off unless `aab-selftest-ops` is on, a mod's remote asks
are cleaned and capped at 60 a game minute, the question ring evicts answered
rows first and writes `question_dropped`, asker mode marks a question
`to_asker`, tool replies fit 3,800 bytes and page with `offset`, and the tool
set grew: `machine_status`, `power`, `research_cost`, `mods` and `resources`,
with new sweep metrics. In the service: the agent loop repairs, refuses and
ends answers more carefully and announces its last turn, one question has a
deadline (`agent.question_timeout`), the day's spend survives a restart
(`agent.daily_spend_path`), SFTP host-key checking can be made mandatory,
the history tailer starts at the file's end and two guards keep another
map's lines out of a new epoch, and the ledger, history, log and spend files
are owner-only. A new check, `tests/realsave/`, runs the tools against a real
save. No version is bumped until the owner decides. The changes are in
`companion-mod/changelog.txt` under 1.2.0 and in the READMEs. Three more landed the same day: free commands (`/ask help`, `top` and `card`, no model round), the companion tool `force_card` with the rpc op `say` and its `on_say` event and `aab-allow-say` setting, and service milestone toasts (`toasts.*`).

Status, 2026-09-15: Phases 0 through 4 built and running on a live server.
Phase 4's briefing was measured against the live ledger the day it shipped:
the free tier's zero-lookup rate went from 0 of 7 questions, when nothing
told the model the briefing existed, to 10 of 17 once the prompt did, with
the briefing paying for itself nine times over (`aab stats 2026-09-15`).
Phase 5 is built on `main` and unreleased: the `sweep` tool with a
delegating metric registry, provider-declared metrics that any multi-team mod
opts into from its own manifest, an `enum` type in the param grammar, and
platform ownership in `list_surfaces` and the briefing. The companion stays at
1.0.4 in `info.json` until the owner has tested it; the 1.0.5 changelog entry
is written and waiting. The design and the decisions behind it are in
[docs/design/phase5-sweep.md](docs/design/phase5-sweep.md).

Status, 2026-10-11: built and verified on a live-save copy, uncommitted and
unreleased, on top of 1.0.5: `recipe_needs`, the MTS records tools, team chat
privacy with map epochs, bigger answer caps with refuse-and-rewrite, the icon
repair chain and a server-wide personality. The designs are in
[docs/design/recipe-needs-spec.md](docs/design/recipe-needs-spec.md), which
carries Parts 1 to 4. No version is bumped until the owner asks.

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
  companion decides what is still pending, so a restart resumes instead of
  replaying. The one exception is an outage: after a call gets no reply the
  service starts again from the first question before it answers anything,
  because a restart may have loaded another map (decision 4).
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
- Chat rendering only, with rich text item icons. A popup frame was built in
  Phase 2 and removed in Phase 3: a window opening over the player is an
  interruption, and chat is where the question was asked.

**4. History lives in the service.**
- The companion appends deaths, alerts, chat, joins, research and rockets to
  `events.jsonl`, server-side only, exactly as the Discord bridge does.
- The service tails that file, locally or over SFTP, into SQLite. One file
  keeps every map's rows, and every read covers the current map epoch only: a
  `session` line (and the status `map_id`) names the map, and another map starts
  a new epoch, so a recycled force or channel name never reads an earlier map's
  rows. "Production since I last died" is a tool.
- Every row records the scope it was said in. A private row, a chat line or a
  question said privately, reaches only a question asked privately in the same
  scope key. A question asked in the open reads public rows only. The store
  applies this in SQL on every read, and a row it cannot place reads as private
  to nobody. See docs/design/recipe-needs-spec.md, Part 3.
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

**8. A briefing rides with every question, and reads are tiered by cost.**
- The briefing is assembled by the service from five serialized `call` trips
  (`list_forces`, `current_research`, `list_surfaces`, `game_time` and
  `list_players`), about 525 ms added to every question, still far cheaper
  than the round it removes. A list that the companion cut is followed page
  by page, up to four pages a section. A dedicated `briefing` op that would
  have made it one trip was planned and never built: neither the companion nor
  the service has it. The briefing is best effort, with its own 2-second
  timeout: on timeout or on error it is omitted, logged once, and the question
  proceeds exactly as it does today.
- Reads backed by engine-maintained counters are cheap enough to always sweep
  every force in one call, never a single force at a time.
- Entity walks stay per force, bounded by a work budget and a maximum radius.
  The reply states what was actually covered and whether the budget ran out.
  A walk with no anchor at all returns a structured refusal instead of
  running unbounded.
- Cheap paths go in front of expensive ones: an item lookup checks the
  logistic networks before it falls back to an anchored walk, and installed
  power capacity ships as a swept nameplate estimate ahead of the exact
  per-network read.
- A round was planned to become one RCON trip: a `calls` op carrying a whole
  round's tool calls in one command. It was planned and never built (ADR
  0011); a round's calls still run as separate `call` trips, at once.
- Voice is one server-wide setting, `personality`, off by default. The map's
  runtime-global setting `aab-personality` rides on every question and wins
  over the operator's `agent.personality`; an admin changes it with
  `/aab-personality`. Its value is `off`, a preset the service owns (`factorio`)
  or one short line of free text, sanitised and always followed by a guard
  sentence so flavour never changes a number, a unit, an item name or a table
  cell. It sits last in the system prompt. A per-player voice and any system
  that rewords an answer after the fact stay cancelled. (This reverses the
  earlier "fixed string, no free text" ruling; docs/design/phase4-spec.md
  section 16 is superseded by `service/internal/agent/voice.go`.)

## The protocol, aab-rpc-v1

One console command, `/aab-rpc <json>`. One JSON object in, one JSON object
out through `rcon.print`. Every reply is `{"ok":true,"r":...}` or
`{"ok":false,"e":"<code>","m":"<detail>"}`.

| op | request | reply `r` | writes storage |
|---|---|---|---|
| `status` | `{}` | protocol version, mod version, tick, player count, pending question count, highest question id issued (`last_id`), and `map_id`, the id the `session` event names (optional: an older companion sends none) | only the map id, once, on a save that has none yet |
| `tools` | `{}` | sorted list of `{iface, v, tools}` per provider, manifests verbatim | no |
| `providers` | `{}` | sorted list of `{iface, v, tools: [names]}`, one small row per provider | no |
| `manifest` | `{i}` | one provider's manifest verbatim | no |
| `call` | `{i, f, a}` | the provider's return value, plain data | no |
| `poll` | `{after?, limit?}` | unanswered questions with id greater than `after` (default 0), oldest first, at most `limit` of them (default 16, max 64); rows carry `scope`, `private`, `to_asker` (only when true), the asker's `surface` (the one they look at) and `physical_surface` when it differs (Phase 3), and `personality`, the map's `aab-personality` setting as it was when the question was asked | no |
| `labels` | `{}` | sorted `{name, label}` rows for every force a labels provider names (Phase 3) | no |
| `answers` | `{after?, limit?}` | answered questions with id greater than `after`, each with the lines the asker saw | no |
| `answer` | `{qid, artifact}` | `true` | yes: validates, renders, then marks answered and raises `on_answer`. An artifact that is malformed, over 16,000 encoded bytes or would print nothing is refused as `bad_artifact`; a failure to print is not |
| `say` | `{artifact}` | `true` | no: validates and prints one unprompted line to everyone (a milestone toast), writes a `say` event, raises `on_say`. `bad_op` when the runtime setting `aab-allow-say` is off; `bad_artifact` for a malformed artifact or one with `to_asker` |

The diagnostic ops `ping`, `big`, `write` and `pcall_test` answer `bad_op`
unless the startup setting `aab-selftest-ops` is on. No op named `calls` or
`briefing` exists.

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_result`, `too_large`, `no_question`, `bad_artifact`.
`bad_artifact` is the one refusal a client must not retry: the artifact itself
is wrong, so the question stays pending and re-answerable with a better one.

`call` checks that the manifest lists `f` and that `remote.interfaces[i][f]`
exists before `pcall(remote.call, i, f, a)`. A result above the byte cap is
refused, never truncated. The cap is measured in Phase 0.

Remote interface `ai-agent-bridge-v1`:
- `ask{ text, player_index?, force?, viewing_force?, watching_force?, scope? }`
  returns a question id, or nil for a bad spec or over its cap of 60 a game
  minute for the whole server.
- `get_event_id("on_answer")` returns the session's event id. Fetch it every
  session from a one-shot `on_nth_tick(1)` armed in `on_init` and `on_load`.

Probe: `agent_tools_v1()` returns `{ v = 1, tools = { <fn> = { desc, params? } } }`.
`params` entries read `<type>[!] <description>` with type one of string,
integer, number, boolean, and a trailing `!` meaning required. `force` is
reserved: the service fills it on every game tool, and a provider never
declares it. A history tool takes it as an optional filter.

Player-facing: `/aab-ask <question>`, and `/ask` unless another mod owns it
or `aab-claim-ask-command` is off. A chat prefix is a setting, off by default.

## Artifacts, v1 shapes

- `summary`: up to three lines.
- `comparison`: two named columns, up to five rows.
- `list`: up to ten rows, one line each.
- `table`: up to five columns, up to eight rows.
- `notice`: one line, a warning or confirmation.
- `art` (companion 1.0.9): up to twelve lines of sixty characters, printed as
  written in the monospace font, spaces kept, on lines of their own. The
  service sends it to an older companion as a list.
- Every shape may carry `session: {name, fresh}` (Phase 3), rendered as a
  `(new session)` marker; older companions ignore it.

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

**Phase 3, sessions, scope and OpenRouter. Specified 2026-09-10,
[docs/design/phase3-spec.md](docs/design/phase3-spec.md).** Shared sessions
keyed by chat scope and an optional `#name`, ended by idle time, a cap or
`new`; a `chat_scope_v1` probe any privacy mod may answer, fixed on the
question when it is asked, with the channel tag on the answer line; the
model backend moved to OpenRouter with the reported cost as the cost,
DeepSeek V4 Pro as the default model; answers in chat only, the popup
removed.
Validates: two players pile follow-ups onto one session and a team in
private mode never sees its session continue in the open; the same question
answered on Claude and on DeepSeek with the operator's config line as the
only difference.

**Phase 4, operators.** The wizard, sidecar and egg, compose, the AleForge
guide, the portal page, `1.0`. Validates: an operator with no shell installs
it from the hosting panel.

**Later.** The Discord bridge as a question source and answer sink.
Permissioned acting tools. Sub-agents on a cheaper model.

## Open questions

1. **`/ask` may already be taken by another mod.** Resolved 2026-11-14:
   `/aab-ask` is always registered, `/ask` is tried under `pcall` beside it,
   and the startup setting `aab-claim-ask-command` lets an operator leave
   `/ask` to another mod. `status` says which name is live.
2. **Chat trigger.** Current plan: the command ships first; a chat prefix
   setting arrives in Phase 2, off by default so ordinary chat never reaches
   the model.
3. **RCON size caps.** Measured 2026-09-10 on 2.0.77: replies up to 4 MB
   arrive complete in one packet and commands up to 1 MB are accepted, no
   truncation either way. The companion's cap is therefore
   a token-budget choice. Resolved: tool results are capped at 8000 bytes, the
   legacy tools op and one manifest at 32 KB each, other reads at 64 KB, refused
   above, never truncated. The tools op shared the 8000 cap until 1.0.4, by which
   point the companion's own catalog had reached 8087 bytes and no longer fit. Since
   2026-11-14 the companion's own list tools also fit 3,800 bytes, under the
   service's 4,096-byte cut, and page with `offset` and `next_offset`.
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
   Extended 2026-09-12: chat and the briefing now enter the prompt on
   purpose, through the briefing block and the `recent_chat` tool. Both are
   fenced as quoted data under one untrusted-input line covering player
   names, force names, chat lines and map marker text. Marker text rides as
   a label only; the coordinates beside it are the machine-readable part,
   never an instruction. Specified in docs/design/phase4-spec.md.
7. **Model providers.** Resolved 2026-09-10 the other way round: models go
   through OpenRouter (ADR 0007), which is what makes Claude and DeepSeek a
   config line apart. The direct Anthropic client stays selectable and
   frozen. The `model.Model` boundary the agent package was built on is what
   made this an addition.
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
   (docs/design/phase1-2-spec.md, "Cost contract"). Second measurement
   showed the rest: the cold cache write and adaptive thinking, so thinking
   is now off by default, effort is configurable, and the rules-and-tools
   cache entry lives an hour. Floor for a warm simple question: about
   $0.003 on Sonnet 5, $0.0015 on Haiku 4.5.
10. **Whether model time or RCON time dominates a multi-round question.**
    Current plan: this is a prediction, not a measurement. It rests on the
    105 ms RCON floor and the round count, because the service has no
    per-round timing data yet. The observability ledger
    (docs/design/phase4-observability-spec.md) is built to settle it, and
    which side wins changes the priority order of the Phase 4 work.
11. **MTS needs a records tool for tech records and milestones.** Built
    2026-10-11, unreleased (docs/design/recipe-needs-spec.md, Part 2): `records`,
    `record_chase` and `player_teams` on the `mts-v1` manifest. The plan was: a cross-repo follow-up, not an AAB change. MTS milestone and record
    announcements go through a per-player broadcast helper that calls
    `player.print`, not `game.print`, so `on_console_chat` never fires and
    `recent_chat` cannot see them and never could. The fix is an `mts-v1`
    `records` tool exposing what `tech_records.lua` and `records.lua` already
    keep in storage, so the companion reads the source of truth instead of
    parsing chat.
12. **A rate over time is not answerable today.** Current plan: the engine's
    flow statistics are the only history of production and they are read
    live, so a question about whether a rate is rising or falling has no
    source. The event log already carries timestamped research, rockets,
    deaths, joins and chat, so trend questions about those are answerable
    service side. No periodic sample is planned to close the production-rate
    gap; this stands as a limit, not a roadmap item.
13. **`rcon.Client.Execute` takes no context.** Current plan: leave it, and
    bound callers instead. A caller that gives up on a call cannot cancel it,
    so the call keeps running and keeps holding the single RCON mutex behind
    the client's own 10 second dial and 30 second io timeouts. The briefing
    (docs/design/phase4-spec.md section 3) therefore never abandons a trip in
    flight; its budget only stops the next one starting. Giving `Execute` a
    context would tighten every call path rather than one, and is worth doing
    when something needs it more than this did.
14. **Nothing in the companion declares `better` yet.** The sweep reply may
    carry `better`, `higher` or `lower`, and the service reads it to rank a
    metric where less is better, but no companion metric sets it. Current
    plan: add it to the first metric that needs it.
15. **The walk tools do not report their coverage.** ADR 0009 promised
    `covered`, `examined`, `budget` and `exhausted` on every entity walk and a
    structured refusal with `accepts`. The tools bound themselves (2,000
    entities for `find_entities`, which says `truncated`) but do not send those
    fields. Current plan: leave it until a question is answered wrongly for
    the want of one.
16. **A same-seed map first reached after its fifth minute can still read the
    map before it.** The map-epoch guards close the other routes (see
    `service/README.md`, History). Current plan: accept it; the companion
    cannot tell two maps of one seed and tick apart.
