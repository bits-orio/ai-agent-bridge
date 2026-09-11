# AI Agent Bridge

Ask your running Factorio game a question in chat and read the answer in
chat, from an AI agent that looks up only what the question needs, the
moment it is asked. Anyone can follow up. Other mods plug their own lookups
in. Works on any server, vanilla or modded, one force or twenty.

Two halves, joined by one protocol:

- **The companion mod** is the game side. It holds the questions, publishes
  the lookups an agent may run, renders answers with icons and map pings,
  keeps a session going, and writes an event log. It is not tied to any
  agent or model: anything that speaks its protocol over RCON can drive it.
  [companion-mod/README.md](companion-mod/README.md) is the protocol.
- **The service** is the agent this repository ships. One Go binary that
  polls the questions, runs a model with the server's tools, and answers.
  It needs two things from you, where the event log is and how to reach
  RCON, plus a model key, and any model OpenRouter lists is one config line.
  [service/README.md](service/README.md) is how to run it.

[QUICKSTART.md](QUICKSTART.md) goes from nothing to an answered question in
six steps. [SECURITY.md](SECURITY.md) says what players can do, what it can
cost, and the guards on each.

## What it does

- `/ask` a question; the answer comes back in chat a few seconds later,
  with item icons, clickable tags when you ask about a thing itself, and
  `[gps]` pings for "where" questions.
- Follow-ups share a session; `#named` sessions let others join yours.
- Any player may ask about any force. A team in private chat gets private
  answers, and forces are named the way players name them, when a mod such
  as Multi-Team Support supplies both.
- History: deaths, research, rockets and chat are kept by the service, so
  "how much iron since I last died" is a real question.
- Cost is visible per question and capped per player, per server and per
  day. Bring your own key.

## Repository map

| Path | What |
|---|---|
| `companion-mod/` | the Factorio mod, Lua only |
| `service/` | the Go service |
| `tests/` | the fake-game Lua suites, the end-to-end harness, a test provider mod |
| `tools/` | release script and portal metadata as code |
| `docs/` | portal page, ADRs, design specs |
| `CONTEXT.md` | the words used everywhere in this repository |
| `PLAN.md` | decisions, protocol, phases, open questions |
| `TESTING.md` | what was measured on a real server, and when |

`wizard/` and `deploy/` are reserved for the operator tooling of the next
phase and are empty today.

## Links

- Source and issues: https://github.com/bits-orio/ai-agent-bridge
- Discord: https://discord.gg/tWz4FT74pH
- Sibling: [Open Discord Bridge](https://github.com/bits-orio/open-discord-bridge), whose service structure this repository mirrors

## Development

Developed with AI coding assistants alongside human review and in-game testing.
MIT licence, see [LICENSE](LICENSE).
