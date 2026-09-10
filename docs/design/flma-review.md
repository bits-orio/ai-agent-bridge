# flma (Factorio Live Agent), reviewed 2026-09-10

Author Jesse Jaggars (jhjaggars). MIT. Clone at ~/src/flma, read-only reference.
Portal: 28 downloads, Utilities, no tags, no homepage, 6 releases since 2026-07-11, latest 0.3.11 (Factorio 2.0 only).
One line: a control-stage mod writes live game state to disk as JSON, and a Python CLI or MCP server reads it for an AI agent.

## How it works

- Producer: one 2,409-line control.lua. Every peer (server and each client) writes its own copy under script-output/flma/<save_id>/. Nothing is filtered per player.
- Master switch is a runtime-global setting. Off means no handlers registered at all, not an early return.
- Every 300 ticks (configurable): production.json (lifetime totals plus 1m/10m/1h/10h flow rates), logistics.json, research.json, inventories.json (opt-in), building-contents.json (named machines only).
- On research events: tech.json with the full prerequisite graph per force.
- On init, mod-config change, recipe-affecting research, or translation completion: recipes.json, an ~11 MB RecipeExporter-compatible dump for the player force, written in one tick.
- Buildings (opt-in): a type blocklist defines "building". One chunk-sliced baseline scan, then an event-driven index updated on build and mine. Live events batch into buildings.ndjson once per interval; the log compacts at 20k lines.
- Localised names come from an on_string_translated pass that drains on its own cadence.
- Consumers: a Python CLI (planner) with a vendored recipe-calculation engine whose recipes.db is built from the mod's own export, so planning matches the running modpack. An MCP server (Streamable HTTP, bearer token) wraps the same handlers, stamps every result with a freshness envelope, and serves Prometheus metrics. Claude Code skills teach the command surface.
- Contract: SCHEMA.md is the only interface between the Lua and Python halves.
- Tooling: dev/ runs a headless server plus a client on an isolated profile with RCON. Release is tag-driven: CI (ruff, mypy, pytest, changelog format, luac -p), zip, GitHub release, portal upload.

## Novel ideas worth keeping

1. Files instead of RCON. A joining client gets data too, no port opens, no hosting needed.
2. Cost as a written design rule: no on_tick, engine aggregates so cost is O(item types), buildings never rescanned.
3. Per-save namespacing with a current-save.json pointer, so several saves on one machine never mix.
4. Recipe DB derived from the live save, with a "modpack alignment" check in status.
5. Freshness envelope instead of errors when the game is not running.
6. Time-sliced baseline scan by map chunk, so a megabase costs more ticks, never a bigger tick.

## Where a successor can improve

- Read-only. There is no path from the agent back into the game: no commands, no ghosts, no chat. Competitors do the acting half over RCON, none do both halves well.
- Single-force. recipes.json is the player force only and the CLI assumes one team. Multi-force servers (MTS) are untouched.
- Local-only. Data lands on the disk of the machine running Factorio. A hosted server (AleForge) has no way to get it out without a shell there.
- Snapshot-only. Nothing event-shaped reaches the consumer: no alerts, deaths, chat, train or power events.
- Coverage gaps: power, trains, alerts, pollution and evolution, enemies, circuits, platforms.
- Whole-file rewrites each cycle, torn writes pushed onto the consumer, 11 MB written in a single tick.
- No in-game surface. Players enable it through map settings and never see the agent inside the game.
- Heavy consumer stack: uv, Python, a DB build step, a clone for the skills.
- No Lua tests. The mod is checked by luac -p and hand verification only.
- Portal: no tags, no homepage, a 460-character summary that never says AI, LLM, MCP or Claude, a title that differs between portal and info.json, sub-1.0 version.

## Portal landscape, same day

| Mod | Downloads | What it does |
|---|---|---|
| ai-companion | 450 | 51 RCON commands, agent acts in-game |
| ai-player-v3 | 159 | autonomous skills driven by an LLM router over RCON |
| ExportFactoriopediaForLLM | 85 | static Factoriopedia text dump |
| planning-agent | 59 | in-game chat with Claude, places ghosts over RCON |
| claude-companion | 49 | server helpers for an agentic player, 2.1 |
| fdash-exporter | 45 | broad live stats export for a dashboard, per-tick budget |
| flma | 28 | this mod |
| world-mode-bridge | 25 | MCP bridge, state out and commands in via RCON |
| factorio-stats-mcp | 12 | stats.json on autosave |

Nobody combines event-driven observation, an action path, multi-force awareness and a hosted-server story. That is the open space.
