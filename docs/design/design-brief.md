# The successor: design direction (captured 2026-09-10)

## Principles

1. Just in time. Nothing is exported continuously. Data is pulled only when a question needs it, and only the minimum for that question.
2. The game is the front end. Questions are asked in-game (chat or a command); answers come back to chat or a small GUI popup.
3. Every piece of game information is a tool. List forces, players of a force, production of one item for one force over one window: each is a structured, bounded read the mod implements and the backend exposes as a Claude tool.
4. Answers are artifacts. The agent picks an answer shape (summary, comparison, list, table) and fills it; the mod renders it. Shapes are text-only and deliberately small.
5. The operator chooses the model; cost is their dial. A tool call needs no model, so the top model calls tools directly by default. Haiku sub-agents are an option the planner takes for multi-part questions or large results, not the default path.
6. History lives in the service, not the game. The mod appends events to a file; the service tails it into SQLite and answers history questions from there.

## The agent loop

Not a fixed number of hops. The agent takes as many tool rounds as the question needs, independent calls in parallel within a round, under a per-question cap on rounds and tokens.

1. Player asks in-game. The mod captures the question with player, force and surface attached.
2. Backend (Python, on the operator's machine) receives it and hands it to the agent, which decides which tools it needs.
3. Tool calls go back into the game over RCON, in parallel where independent, and return small JSON results. Repeat until the agent has what it needs.
4. The agent composes an artifact; the backend sends it back over RCON and the mod renders it to chat or a popup for that player.

## What flips from flma, what carries over

| flma | Successor |
|---|---|
| Push: files rewritten every 300 ticks | Pull: nothing runs until a question arrives |
| Files under script-output, local disk | RCON in and out, works against a hosted server |
| Terminal CLI, agent outside the game | Chat and GUI inside the game |
| Read everything, agent filters | Read only what the question needs, per tool |
| One force | Force is a parameter on every tool; any multi-force game is first-class |
| Free-text CLI output | Typed answer artifacts rendered by the mod |
| Python-only consumer | Backend still Python; the player needs only the mod |

Carries over: cost discipline as a written rule, per-force engine aggregates, freshness stamped on results, the dev rig with RCON, tag-driven release.

## Draft tool catalogue (each returns bounded JSON)

- Discovery: list_forces, list_players(force), list_surfaces(force), server_info
- Research: current_research(force), research_queue(force), tech_status(force, tech)
- Production: item_rate(force, surface, item, window), top_items(force, surface, window, n), fluid_rate(...)
- Logistics: network_summary(force, surface), item_in_networks(force, surface, item)
- Buildings: count_by_name(force, surface, name), find_entities(force, surface, name, limit)
- Prototypes: search_prototypes(query), recipe(name), what_makes(item)
- Power: power_summary(force, surface)
- Later, permissioned: acting tools (queue research, ping map, place ghosts)

## Answer artifacts (draft shapes)

- summary: at most three lines
- comparison: two named columns, up to five rows
- list: up to ten rows, one line each
- table: up to five columns, up to eight rows
- notice: a one-line warning or confirmation
All rendered by the mod with Factorio rich text (item icons inline), in chat or a popup frame.

## Extension interface

How other mods add tools, submit questions and receive answers while the companion knows nothing about them. Three designs were drafted from different angles and scored by three judges (Factorio lifecycle, mod-agnosticism, backend fit). The shape below won under every lens. It rests on one engine fact: remote.interfaces is already a readable registry of every interface and function name, so the companion never needs a registry of its own.

Seams
1. Tool supply (pull). A provider adds a zero-argument function named agent_tools_v1 to any remote interface it owns. It returns a table whose keys are function names on that same interface, each with a description and parameters. The companion scans for that function name on every agent turn, inside the RCON command handler. Nothing is stored or registered. A removed provider simply vanishes. Precedent: FactorioMilestones scans remote.interfaces for milestones_presets the same way.
2. Question intake (push). The companion owns one frozen interface, ai-companion-v1, with ask and get_event_id. A Discord bridge calls ask with text and an optional player and force hint, gets a question id back, and keeps the id to message mapping itself. Guarded by `if remote.interfaces["ai-companion-v1"]`, the same convention integrators use toward the Discord bridge.
3. Answer delivery (broadcast). The companion prints the answer to the asker and raises a custom event carrying id, question, answer and player. Subscribers fetch the event id each session from a one-shot on_nth_tick(1) armed in both on_init and on_load.

Transport: one console command, `/ai-rpc <json>`, four operations: tools, call, poll (after a cursor), answer. Every reply is one rcon.print of one JSON object. Oversized results are refused, never truncated. Only answer writes anything, so a lost response costs nothing: the backend re-polls the same cursor.

What a provider writes, in full (no change to its own frozen API, no dependency in either direction):

```lua
remote.add_interface("teams-agent", {
  agent_tools_v1 = function() return { tools = {
    team_list = { desc = "Every claimed team: force name, display name, member count, pause state." },
    team_standings = {
      desc   = "Race leaderboard for one milestone. Call with no arguments to list the milestone keys.",
      params = { milestone = "string! a key from the empty-call reply",
                 rank_by   = "string 'elapsed' (default) or 'online_elapsed'" },
    },
  } } end,
  team_list      = function(a) ... return rows end,
  team_standings = function(a) ... return rows end,
})
```

Parameters use a one-line grammar, `<type>[!] <description>`, four scalar types, trailing `!` for required. Tools return plain data only: a Lua object leaks through remote.call intact and cannot be serialised.

- The backend builds Claude tool definitions from the catalog, names each tool by interface and function, keeps an explicit reverse map for routing, and injects `force` as a reserved parameter on every tool.
- The companion publishes its own engine tools through the same probe on a reserved namespace, so the seam cannot break without breaking the companion.
- Latency, size and cost are measured by the backend per call, not declared by providers. Lua has no wall clock.
- Version lives in the probe name. A breaking manifest change ships as agent_tools_v2, scanned alongside v1.
- Team names are the only thing a team-mod needs to own. Spawn positions, players and surfaces are engine data any tool can read directly.

Verify in-game before designing around it, one test each:
- Does rcon.print inside a command handler return to the RCON client, and where does a response above 16 KB truncate? No size limit is documented.
- Is player_index nil for RCON-invoked commands? Documented only for the server console.
- Does a storage write or raise_event inside an RCON command replicate identically to every peer? Two clients, one command, compare.
- Does pcall(remote.call, ...) catch an error thrown inside the provider's function?
- Does a command handler block the simulation for every player while it runs? If so, tools need a hard per-call budget in Lua.
- Does local-rcon-socket enable single-player use?

Security
- Prompt injection arrives through tool results, not only the question. Team display names, player names and milestone keys are player-typed. Label provider text as untrusted in the system prompt.
- The companion cannot stop a provider from listing a state-changing function. The contract forbids it in words; the backend can keep a read-only allow-list per server.

## Pointers for brainstorming further

Transport and trigger
- How does the backend learn a question was asked? The mod cannot open a socket. Options: RCON polling of a drain function, tailing a file the mod writes, or reading the server console log. RCON polling is the only path that works against a hosted server.
- Trigger: a custom command (/ask ...) gives the mod the player and the text without broadcasting; a chat prefix is friendlier. Support both?
- Acknowledge immediately from the mod ("working on it"), at zero token cost, then deliver the answer.

Tools
- Every tool declares cost class (engine aggregate vs entity scan), scope (force, surface) and a hard cap on output size.
- Engine flow statistics keep production history for free, up to 1000 hours. Event history (deaths, alerts, chat, joins) comes from an append-only event file the mod writes on each event, tailed locally or over SFTP by the service exactly as the Discord bridge tails its events file, and stored in SQLite. The service keeps the whole history of the save, so "production since I last died" is a tool, not a guess.
- Cache tool results for a few seconds so five players asking the same thing costs one read.
- Localised names: internal names are enough for Claude; a search tool covers modded prototypes.

Artifacts
- The agent emits structured data, the mod renders it. Formatting leaves the LLM, output shrinks, every answer looks the same.
- A popup can carry buttons: "ask a follow-up", "show as table", "pin". Chat cannot.
- Rich text lets the answer show [item=iron-plate] icons; no other agent mod does this.

Multi-force
- Any player may ask about any force. No scoping, no permissions. The asker's own force is only the default when a question names none.
- Comparison and leaderboard artifacts across forces become possible only because force is a parameter on every tool.
- Other mods that own multi-force concepts (team names, spawns, standings) supply them through the extension interface, so the companion stays generic.

Cost and orchestration
- Operator-chosen model on top. Haiku sub-agents only when the planner decides a sub-question needs several calls plus summarising.
- Per-player quota, per-server budget, cooldown, and a "cost this session" readout for the operator.
- Short per-player conversation window with a TTL so "and copper?" works.

Platform and product
- The Discord bridge already needs RCON to the same server. One backend, one tool layer, two front ends: in-game chat and Discord.
- Where the backend runs: the operator's PC, the server host, or a hosted service. Pick the first for v1.
- Latency budget: poll interval + planning + parallel tool calls + composing. Target under ten seconds with an immediate ack.
- Name and title must carry the searchable words (AI, agent, chat, assistant). Decide before the repo exists.
- Tell jhjaggars before shipping; record the reply.

# Project outline

The repository (github.com/bits-orio/ai-agent-bridge, name chosen 2026-09-10: AI Agent Bridge, AAB) mirrors Open Discord Bridge: a Lua companion mod, a Go service that runs one process per Factorio server, a setup wizard, a fixed menu of deployment shapes, and portal metadata kept as code. Half of the service is copied from the bridge unchanged.

## Layout

```
companion-mod/            the Factorio mod, Lua only
  control.lua               wiring: commands, events, remote interface
  scripts/rpc.lua           the /aab-rpc console command: ops tools, call, poll, answer
  scripts/probe.lua         scans remote.interfaces for agent_tools_v1, builds the catalog
  scripts/tools/*.lua       engine tools, one file per area: forces, players, research, production, logistics, buildings, power
  scripts/questions.lua     /ask command and chat prefix, bounded question ring in storage
  scripts/render.lua        artifact JSON to chat lines or a popup frame, rich text with item icons
  scripts/events.lua        append-only events.jsonl: deaths, alerts, chat, joins, research, rockets
  scripts/remote.lua        the frozen ai-companion-v1 interface: ask, get_event_id
  info.json, changelog.txt, README.md (integrator API), link-mod.sh, thumbnail.png
service/                  Go, single binary, one per server (bridge/ in ODB)
  cmd/aab/main.go           wiring, pollers, signal handling
  internal/config           yaml or env mode, effective snapshot, unknown-key warnings   [copied from ODB]
  internal/transport        tailer, local, sftp                                          [copied from ODB]
  internal/rcon             reconnecting RCON client                                     [copied from ODB]
  internal/rpc              client for the mod's rpc command: tools, call, poll, answer
  internal/catalog          manifest to Claude tool definitions, reverse map, force injection
  internal/agent            the loop: Anthropic Go SDK tool runner, round and token caps, per-player window
  internal/artifacts        typed answer shapes, validation, size caps
  internal/history          events.jsonl to SQLite, history tools (since last death, per force)
  internal/controlapi       /v1/status, /v1/config, /v1/cost                            [pattern from ODB]
wizard/                   validates RCON and the API key, picks model and transport, renders config and .env
deploy/                   Dockerfile.sidecar, pterodactyl-egg.json, docker-compose.yml
tools/                    portal_meta.json, portal_lint.py, portal_check.py, sync_portal_details.sh, upload_mod_portal.sh, release.sh   [copied from ODB]
docs/                     portal.md, aleforge/SETUP.md, adr/
.github/workflows/        ci.yml (go vet, test, build, luac -p, changelog format), release.yml, mod-portal-upload.yml, portal-details.yml
README.md, QUICKSTART.md, DEPLOYMENT.md, TESTING.md, PLAN.md, ALEFORGE_CONFIG.md, CONTEXT.md
```

## Decisions to record first (PLAN.md opens with these, as the bridge's plan does)

1. Pull, over RCON. Nothing runs in the game until a question arrives. The service is the only party that polls.
2. Three seams. Tools by probe (agent_tools_v1), questions by a frozen interface, answers by event.
3. Typed artifacts. The model emits structured data, the mod renders it. Chat first, popup second.
4. History in the service. Event file tailed into SQLite. Engine flow statistics for production history.
5. Operator brings the key and picks the model. Cost is reported per session, never hidden.
6. Go for the service. The tailer, SFTP, RCON client, config loader and release plumbing move over from the bridge unchanged. The official Anthropic Go SDK carries a tool runner for the agent loop. One binary per platform keeps the same deployment menu as the bridge.

## Phases

- Phase 0, checks. The six engine tests on the dev rig, each a one-line command. Validates: the transport assumptions the whole design rests on.
- Phase 1, the loop. /ask in game, one Go binary, six engine tools, one summary artifact printed to chat. Validates: a question round trip under ten seconds on a real server.
- Phase 2, depth. The popup renderer and the other artifact shapes, the provider probe with one example provider, the event file and SQLite history tools, per-player follow-ups.
- Phase 3, operators. Wizard, sidecar and egg, AleForge guide, portal page, 1.0 release.
- Later. The Discord bridge as a question source, permissioned acting tools.

## Name candidates (all internal names free on the portal, 2026-09-10)

| Title | Internal name | Why |
|---|---|---|
| AI Factory Advisor (AFA), my pick | ai-factory-advisor | AI first, factory says what it is about, advisor says it answers and does not act |
| Open Chat Advisor (OCA) | open-chat-advisor | The Open Discord Bridge shape, chat in the second slot |
| AI Server Advisor (ASA) | ai-server-advisor | Judge's top score; server is a house keyword but hides single-player use |
| Factory Chat Advisor (FCA) | factory-chat-advisor | Reads most naturally as a product; neither search word is first |
| AI Factory Oracle (AFO) | ai-factory-oracle | Oracle is the plainest "ask it, it answers" word; faint brand echo |
| Open Factory Assistant (OFA) | open-factory-assistant | Family shape; assistant is searched but sits third |
