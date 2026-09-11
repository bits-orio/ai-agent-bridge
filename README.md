# AI Agent Bridge

Ask your running Factorio game a question in chat and get the answer back in
the game, from an AI agent that pulls only what each question needs, when it
needs it. Other mods plug their own tools into it.

Status: design stage. Nothing runnable yet. See [PLAN.md](PLAN.md).

AI Agent Bridge is two halves. The **companion mod** exposes a tiny protocol
over one console command and knows nothing about any other mod. The **service**,
one Go binary per server, drives that protocol over RCON: it picks up questions,
runs an agent whose tools are bounded reads of live game state, and sends a
typed answer back into chat, with the channel tag players' own lines carry. It
is multi-force from the start: every tool takes a force, and any player may ask
about any force. Follow-ups pile onto a shared session, and a model is one
config line: Claude, DeepSeek or anything else OpenRouter lists.

What players can do, what it can cost and the guards on each are in
[SECURITY.md](SECURITY.md).

## How a question travels

1. A player types `/ask what is my iron plate rate on nauvis`.
2. The service polls the question over RCON and hands it to the agent.
3. The agent calls tools, as many rounds as it needs, each one an RCON round
   trip into the companion.
4. The agent fills an answer shape. The companion renders it with item icons
   and shows it to the asker.

## Other mods add tools

A mod exposes a zero-argument function named `agent_tools_v1` on any remote
interface it owns, returning the names and descriptions of its tools. The
companion finds it by scanning the engine's interface list on every turn.
Nothing is registered, nothing is stored, and neither side names the other.
The companion's own engine tools are published the same way.

## Repository map

| Path | What |
|---|---|
| `companion-mod/` | the Factorio mod, Lua only: the rpc command, the probe, the question ring, the renderer, the event file, the frozen `ai-agent-bridge-v1` interface |
| `service/` | the Go service: RCON client, file tailer, config, protocol client, agent loop, artifacts, history, control API |
| `wizard/` | setup CLI that validates RCON and the API key and writes the config |
| `deploy/` | sidecar container, Pterodactyl egg, compose |
| `tools/` | release script and portal metadata as code |
| `docs/` | portal page, ADRs, hosting guides |
| `CONTEXT.md` | the domain language used everywhere in this repository |
| `PLAN.md` | decisions, protocol, phases, open questions |

## Links

- Source and issues: https://github.com/bits-orio/ai-agent-bridge
- Discord: https://discord.gg/tWz4FT74pH
- Sibling: [Open Discord Bridge](https://github.com/bits-orio/open-discord-bridge), whose service structure this repository mirrors

## Development

Developed with AI coding assistants alongside human review and in-game testing.
MIT licence, see [LICENSE](LICENSE).
