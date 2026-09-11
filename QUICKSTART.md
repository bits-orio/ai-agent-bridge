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
RCON port, and pick `model.id`, any model OpenRouter lists that supports
tool calling. The default, DeepSeek V4 Pro, answers a simple question for
a fraction of a cent; `anthropic/claude-opus-5` costs a few cents. The
example file lists a few with their prices.

Put your secrets in a `.env` file next to `aab.yaml`, never in the YAML
itself:

```sh
cat > .env <<'EOF'
FACTORIO_RCON_PASSWORD=<the password from step 2>
OPENROUTER_API_KEY=<your OpenRouter API key>
EOF
```

Bring your own key from [openrouter.ai](https://openrouter.ai/keys). To talk
to the Anthropic API directly instead, set `model.provider: anthropic`, put
an Anthropic model id in `model.id` and `ANTHROPIC_API_KEY` in `.env`.
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

- **Nothing happens after `/ask`.** The service does not log every poll
  (that would be a line every second or two for nothing); it logs one line
  the moment it actually picks a question up:

  ```
  question 3 from Alice (player 1, force player): what forces are there?
  answer 3 shape=summary rounds=1 tokens=100/20 cost=$0.0004
  ```

  With nothing to do it says so every five minutes, `idle, 7 questions
  answered`, so a quiet log still tells you the service is alive.

  If you never see a `question N from ...` line for what you typed, the
  service is either not reaching the server or holding your question back,
  and two other lines tell you which. `run: poll failed: <error>` once (not
  on every retry) means it cannot reach the server at all, so confirm
  `factorio.rcon.address` and `FACTORIO_RCON_PASSWORD` actually match it.
  `question N: no catalog available, leaving it pending and trying again
  next tick` means it has your question and is waiting to read the tool list
  off the companion; the `catalog:` line just above it names the reason, and
  the question runs by itself once the list arrives.

  If the `question` line shows up but the `answer` line, or the reply in
  game, doesn't, look for `answer N: could not deliver it, trying again next
  tick: <error>` right after it. `answer N: the companion refused the
  artifact, sending a notice instead` means the answer itself was a shape
  the game cannot render, so the asker gets a short notice in its place.
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
