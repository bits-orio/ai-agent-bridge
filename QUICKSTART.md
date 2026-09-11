# Quickstart

From a server with nothing to a question answered in chat. Six steps; the
two that matter are where the event log is and how to reach RCON.

## 1. Install the companion mod

`ai-agent-bridge` from the in-game mod browser or the
[mod portal](https://mods.factorio.com/mod/ai-agent-bridge), on the server
and on every client. It needs nothing else.

## 2. Turn on RCON

The service talks to the mod over RCON. On a command line:

```sh
--rcon-port 27015 --rcon-password <a password you pick>
```

Hosting panels show the port and the password as fields in the server's
startup settings; AleForge does under **Startup**. RCON is off while the
password is blank.

## 3. Build the service

No prebuilt binary yet. Docker builds it, no Go install needed:

```sh
git clone https://github.com/bits-orio/ai-agent-bridge
cd ai-agent-bridge
make service      # -> service/aab
cd service
```

## 4. Tell it the two things it needs

```sh
cp aab.yaml.example aab.yaml
cp ../.env.example .env
```

In `aab.yaml`, fill in `factorio.rcon.address` and `factorio.events_file`.

- **Service on the same machine as the server:** the address is
  `127.0.0.1:<rcon port>` and the events file is the server's
  `script-output/ai-agent-bridge/events.jsonl`; leave `transport: local`.
- **Server on a host:** the address is `<host>:<rcon port>`, the events
  file is the same path on the host's SFTP, and `factorio.sftp` gets the
  SFTP host, port and user, with `transport: sftp`. On AleForge the SFTP
  details are on the server's settings page and the password is your panel
  password. [service/README.md](service/README.md) has both blocks to copy.

In `.env`, fill in `FACTORIO_RCON_PASSWORD`, `SFTP_PASSWORD` when SFTP is
in use, and `OPENROUTER_API_KEY` from [openrouter.ai](https://openrouter.ai/keys).
The default model is DeepSeek V4 Pro; `model.id` in `aab.yaml` picks any
other model OpenRouter lists that supports tool calling, and the example
file names a few with their prices.

## 5. Run it

```sh
./aab -config aab.yaml run
```

It refuses to start until the two things are there and says which is
missing. Leave it running: a second terminal, a systemd unit or a container
all work, on any machine that can reach the server.

## 6. Ask in game

```
/ask what is my iron plate rate
```

The question is echoed to everyone, the answer prints a few seconds later,
and anyone can follow up. `/ask new` starts over, `/ask #iron ...` names a
session, `/ask sessions` lists them.

## When it does not work

- **Nothing happens after `/ask`.** The service logs one line the moment it
  picks a question up: `question 3 from Alice (player 1, force player): ...`.
  Without it, `poll failed: ...` in the log means it cannot reach RCON, so
  the address or the password is wrong. `question 3: no catalog available`
  means it has the question and is waiting to read the tool list; the line
  above it says why.
- **The service refuses to start.** Read `aab.effective.yaml` next to it:
  every setting as resolved, each secret marked `SET (n chars)` or `MISSING`.
- **The question is refused with a short notice.** The asker hit a cap: five
  seconds between asks, 20 questions an hour per player, 120 an hour for the
  server, five dollars a day. [SECURITY.md](SECURITY.md) lists them all and
  where each is set.
- **Answers name the wrong surface, or ask which one.** The service tells
  the model which surface the asker is looking at and searches there; name
  the surface or the team in the question for anything else.

## Links

- Source and issues: https://github.com/bits-orio/ai-agent-bridge
- Discord: https://discord.gg/tWz4FT74pH
- The protocol, for writing another agent:
  [companion-mod/README.md](companion-mod/README.md)
- The design: [CONTEXT.md](CONTEXT.md), [PLAN.md](PLAN.md),
  [docs/design/](docs/design/)
