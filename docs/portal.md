# AI Agent Bridge
> Ask your server a question in chat. An AI agent reads the live game state and answers you.

[![Discord](https://img.shields.io/badge/Discord-join%20the%20server-5865F2?logo=discord&logoColor=white)](https://discord.gg/tWz4FT74pH) [![GitHub](https://img.shields.io/badge/GitHub-source-181717?logo=github&logoColor=white)](https://github.com/bits-orio/ai-agent-bridge)

You type a question in chat and get an answer back in the game, pulled fresh from your running save. Nothing is exported or scanned ahead of time: the agent reads only what your question needs, the moment you ask it. It works on any multiplayer server, answers about any force, and other mods can hand it new things to look up.

## Status

Design stage. Nothing here is installable or playable yet. The protocol and the phase plan are written and public on GitHub; the companion mod and the service that drives it are still being built. Watch the Discord or the GitHub repo for the first runnable release.

## Quick start

Once the first release ships, running it looks like this:

1. Install the companion mod on your Factorio server.
2. Run the AI Agent Bridge service next to the server (one small program, you keep your own API key).
3. Point the service at your server over RCON.
4. Type `/ask` followed by your question in game chat.
5. Read the answer in chat or in a popup, with item icons rendered inline.

## Features

- Pulls only what a question needs, when it's asked. No standing export, no background scan.
- Every tool takes a force, so any player can ask about any force on the server, not just their own.
- Answers come back as small typed shapes, a summary, a comparison, a list, a table, rendered by the mod, never raw model text.
- Other mods add their own tools by exposing one function. The companion never needs to know they exist.
- You bring your own Anthropic API key and pick the model. Cost per question is visible, never hidden.
- History of what happened on your save (deaths, research, rockets) is kept by the service, so "since I last died" is a real question you can ask.

## Compatibility

Needs Factorio 2.0 or later and the AI Agent Bridge service running beside your server with your own Anthropic API key. Works on a plain vanilla server: it has no mod-specific code of its own.

Multi-force from the start. Every tool takes a force as a parameter, so it works the same whether your server runs one force or twenty.

## Works with

Runs well alongside [Open Discord Bridge](https://mods.factorio.com/mod/open-discord-bridge), which already needs RCON on the same server. Neither one depends on the other.

## Links

- [Source on GitHub](https://github.com/bits-orio/ai-agent-bridge)
- [Report a bug](https://github.com/bits-orio/ai-agent-bridge/issues)
- [Community Discord](https://discord.gg/tWz4FT74pH)

## Development

Developed with AI coding assistants alongside human review and in-game testing. Issues and pull requests are welcome on [GitHub](https://github.com/bits-orio/ai-agent-bridge).

License: MIT
