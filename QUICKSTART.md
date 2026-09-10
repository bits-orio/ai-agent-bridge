# Quickstart

The operator path from a fresh server to answering `/ask` in game. See
[README.md](README.md) for what the two halves do and [PLAN.md](PLAN.md) for
the phases; this page is only the steps.

Status: the `run` subcommand this page describes is Phase 1, built alongside
this page (see PLAN.md). Until it lands, `./aab status`, `./aab probe`,
`./aab rpc` and `./aab poll` work against a server running the companion
mod; there is no agent loop answering questions yet.

## 1. Install the companion mod

Get `ai-agent-bridge` from the in-game mod browser, or from
[the GitHub repo](https://github.com/bits-orio/ai-agent-bridge), on every
server and client that should see its answers. It needs nothing else: on
its own it just holds questions in a ring buffer and waits for the service.

## 2. Enable RCON on your server

The service is the only thing that polls, and it polls over RCON. Add to
your launch command:

```sh
--rcon-port 27015 --rcon-password <a password you pick>
```

Most hosting panels (Pterodactyl and similar) expose these as fields in the
server settings instead of launch flags; use whatever port and password the
panel shows you.

## 3. Get the service

No prebuilt binary yet. Build it with Docker, no Go install needed:

```sh
git clone https://github.com/bits-orio/ai-agent-bridge
cd ai-agent-bridge
make service      # -> service/aab
```

## 4. Configure it

```sh
cd service
cp aab.yaml.example aab.yaml
```

Edit `aab.yaml`: point `factorio.rcon.address` at your server's host and
RCON port, and set `anthropic.model` to the model you want to pay for.

Put your secrets in a `.env` file next to `aab.yaml`, never in the YAML
itself:

```sh
cat > .env <<'EOF'
FACTORIO_RCON_PASSWORD=<the password from step 2>
ANTHROPIC_API_KEY=<your Anthropic API key>
EOF
```

Bring your own key from [console.anthropic.com](https://console.anthropic.com/).
The service brings the model and pays for every question; nothing else in
this repository ever holds a key.

## 5. Run it

```sh
./aab -config aab.yaml run
```

Leave it running alongside your server: a systemd unit, a sidecar
container, or a second terminal all work. It logs one line per question it
answers, including the cost.

## 6. Ask in game

Type in chat:

```
/ask what is my iron plate rate on nauvis
```

The answer prints back a few seconds later. If another mod already owns
`/ask`, the companion falls back to `/aab-ask` and says which one is live
in its `status` reply.

## Troubleshooting

- **Nothing happens after `/ask`.** Check the service log; it should show a
  poll every second or two. If it never sees your question, confirm
  `factorio.rcon.address` and `FACTORIO_RCON_PASSWORD` actually match the
  server.
- **The service refuses to start.** It writes `aab.effective.yaml` next to
  itself on every run: the fully-resolved config with each secret marked
  `SET (n chars)` or `MISSING`. Read that before re-reading the YAML.
- **A question comes back as a short refusal instead of an answer.** Check
  `agent.questions_per_player_per_hour` in `aab.yaml`; the default caps one
  player at 20 questions an hour.

## Links

- Source and issues: https://github.com/bits-orio/ai-agent-bridge
- Discord: https://discord.gg/tWz4FT74pH
- Full protocol and design: [CONTEXT.md](CONTEXT.md), [PLAN.md](PLAN.md),
  [docs/design/phase1-2-spec.md](docs/design/phase1-2-spec.md)
