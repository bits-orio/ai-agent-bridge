# AI Agent Bridge
> Ask your server a question in chat. An AI agent reads the live game and answers you there.

[![Discord](https://img.shields.io/badge/Discord-join%20the%20server-5865F2?logo=discord&logoColor=white)](https://discord.gg/tWz4FT74pH) [![GitHub](https://img.shields.io/badge/GitHub-source-181717?logo=github&logoColor=white)](https://github.com/bits-orio/ai-agent-bridge)

Type `/ask` and a question in chat. The answer comes back in chat a few seconds later, pulled fresh from your running save, with item icons and clickable map pings. Anyone can follow up. It works on any multiplayer server, vanilla or modded, one force or twenty, and other mods can hand it new things to look up.

This mod is the game side of an open protocol, not an AI. It holds questions, publishes lookups, renders answers and writes an event log; an agent program you run beside the server does the thinking, over RCON. The one on GitHub uses your own OpenRouter key and any model you pick. Anyone can write another.

## Status

First release. The protocol is frozen and additive from here. Tested on 2.0 headless servers and with Multi-Team Support.

## Quick start

1. Install this mod on the server and every client.
2. Turn on RCON on the server (a port and a password, in your launch settings or your hosting panel).
3. Run the agent from GitHub, telling it where the mod's event log is and how to reach RCON, with your model key: https://github.com/bits-orio/ai-agent-bridge/blob/main/QUICKSTART.md
4. Type `/ask what is my iron plate rate` in chat.

## Features

- Pulls only what a question needs, when it is asked. No standing export, no background scan.
- Follow-ups share a session; `/ask #iron` names one that others can join, `/ask new` starts over.
- Every lookup takes a force, so any player can ask about any force on the server.
- "Where" questions answer with map pings you can click, ghosts included.
- Answers are small typed shapes rendered by the mod, never raw model text; items show as icons.
- Team chat privacy and team names come from mods that have them, through two small probes.
- Other mods add their own lookups by exposing one function; this mod never needs to know them.
- You bring your own key and pick the model. Every question's cost is visible and capped.

## Compatibility

Factorio 2.0. Works on a plain vanilla server. Needs RCON on the server and an agent running beside it.

## Works with

- [Multi-Team Support](https://mods.factorio.com/mod/multi-team-support): answers stay inside a team's private chat and teams are named the way players name them.
- [Open Discord Bridge](https://mods.factorio.com/mod/open-discord-bridge): shares the same RCON and the same event log layout. Neither depends on the other.

Part of the MTS family: [Multi-Team Support](https://mods.factorio.com/mod/multi-team-support), [Open Discord Bridge](https://mods.factorio.com/mod/open-discord-bridge).

## Links

- [Source on GitHub](https://github.com/bits-orio/ai-agent-bridge)
- [The protocol, for writing your own agent](https://github.com/bits-orio/ai-agent-bridge/blob/main/companion-mod/README.md)
- [Report a bug](https://github.com/bits-orio/ai-agent-bridge/issues)
- [Community Discord](https://discord.gg/tWz4FT74pH)

## Development

Developed with AI coding assistants alongside human review and in-game testing. Issues and pull requests are welcome on [GitHub](https://github.com/bits-orio/ai-agent-bridge).

License: MIT
