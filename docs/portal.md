# AI Agent Bridge
> Ask your server a question in chat. An AI reads your live game and answers you in chat.

[![Discord](https://img.shields.io/badge/Discord-join%20the%20server-5865F2?logo=discord&logoColor=white)](https://discord.gg/tWz4FT74pH) [![GitHub](https://img.shields.io/badge/GitHub-source-181717?logo=github&logoColor=white)](https://github.com/bits-orio/ai-agent-bridge)

Type `/ask` and a question, the way you would ask a friend on the server. A few seconds later the answer prints in chat, worked out from your save as it is right now. Things you can ask:

- `/ask where am I making repair packs` and get a map ping you can click.
- `/ask what is our iron plate rate` and get the number, with the icon.
- `/ask how much iron did we make since I died`
- `/ask what is Team Ace researching`
- `/ask which team has launched more rockets`

Ask a follow-up and it remembers what was just said. Anyone on the server can join in. Nothing is scanned or exported ahead of time: the AI looks up only what your question needs, when you ask it.

## Status

First release. Tested on 2.0 dedicated servers, vanilla and with Multi-Team Support, with and without Space Age.

## Quick start

1. Install this mod on the server and on every client.
2. Turn on RCON on the server: a port and a password, in your launch settings or in your hosting panel.
3. Run the agent program beside the server. It is a small download from GitHub, needs to know where the mod's event log is and how to reach RCON, and takes your own model key. The walkthrough: https://github.com/bits-orio/ai-agent-bridge/blob/main/QUICKSTART.md
4. Type `/ask what is my iron plate rate` in chat.

## Features

- Ask in plain words; answers come back in chat with item icons and map pings.
- Follow-ups share a session; `/ask #iron` names one others can join, `/ask new` starts fresh.
- Every player can ask about every team or force on the server.
- On a Multi-Team Support server, team-only chat gets team-only answers, and teams are called by their names.
- Knows what happened: deaths, research, rockets and chat since the save began.
- Other mods can add their own lookups, so the AI learns about them without any change here.
- Your own key, any model: Claude, GPT, DeepSeek, Gemini or anything else OpenRouter lists. Every answer shows what it cost, and there are caps per player, per server and per day.

## How it works

This mod is the game side. It takes the questions, publishes the lookups the AI may run, draws the answers and keeps a small event log. The thinking happens in the agent program you run next to the server; it talks to the mod over RCON and to the model with your key. The protocol between them is open, so anyone can write a different agent, in any language, and the mod will not know the difference.

## Compatibility

Factorio 2.0, Space Age or not. Works on a plain vanilla server. Needs RCON on the server and the agent program running beside it.

## Works with

- [Multi-Team Support](https://mods.factorio.com/mod/multi-team-support): private answers in team chat, teams by name, and each team's own clock so "how am I doing compared to them" compares fairly.
- [Open Discord Bridge](https://mods.factorio.com/mod/open-discord-bridge): both use RCON on the same server, neither needs the other.

Part of the MTS family: [Multi-Team Support](https://mods.factorio.com/mod/multi-team-support), [Open Discord Bridge](https://mods.factorio.com/mod/open-discord-bridge).

## Links

- [Source on GitHub](https://github.com/bits-orio/ai-agent-bridge)
- [Write your own agent](https://github.com/bits-orio/ai-agent-bridge/blob/main/companion-mod/README.md)
- [Report a bug](https://github.com/bits-orio/ai-agent-bridge/issues)
- [Community Discord](https://discord.gg/tWz4FT74pH)

## Development

Developed with AI coding assistants alongside human review and in-game testing. Issues and pull requests are welcome on [GitHub](https://github.com/bits-orio/ai-agent-bridge).

License: MIT
