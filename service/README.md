# AI Agent Bridge: Service

Go binary, one process per Factorio server. Drives the companion mod's `aab-rpc-v1`
protocol over RCON. See [../CONTEXT.md](../CONTEXT.md) and [../PLAN.md](../PLAN.md) for
the domain words and the phased plan. This is the Phase 0 skeleton: transport and a
protocol client, no agent loop yet.

## Build

Needs Go 1.25+.

```sh
cd service
go vet ./...
go test ./...
go build -o aab ./cmd/aab
```

## Configure

```sh
cp aab.yaml.example aab.yaml   # edit paths and the RCON address
```

Secrets are read from the env vars named in `aab.yaml` (`rcon.password_env`,
`anthropic.api_key_env`), never put them in the YAML. A `.env` file next to `aab.yaml`
is auto-loaded (real environment variables always win); see `.env.example` conventions in
[open-discord-bridge](https://github.com/bits-orio/open-discord-bridge/blob/main/bridge/.env.example)
for the shape.

With no `aab.yaml` present (or `AAB_CONFIG=none` set), the service reads its whole
configuration from `AAB_*` environment variables instead. See
`internal/config/config.go` for the full list. Every load, in either mode, writes
`aab.effective.yaml` to the working directory: the fully-resolved config, secrets
redacted to `SET (n chars)` / `MISSING`, for inspecting exactly what the service resolved
without exposing anything. It is output only, the service never reads it back.

## Run

```sh
./aab -config aab.yaml status
./aab -config aab.yaml probe
./aab -config aab.yaml rpc '{"op":"poll","after":0}'
./aab -config aab.yaml poll 0
```

## Subcommands

- **`status`**: sends the `status` op, prints the reply: protocol version, mod version,
  tick, player count, pending question count.
- **`probe`**: sends the `tools` op, prints the full catalog every provider on the server
  exposes (the companion's own engine tools plus anything another mod adds via
  `agent_tools_v1`).
- **`rpc <json>`**: sends one raw `aab-rpc-v1` request and prints the reply. `<json>` is a
  JSON object carrying its own `"op"` field, e.g.
  `aab rpc '{"op":"call","i":"ai-agent-bridge-v1","f":"list_forces","a":{}}'`. This is the
  escape hatch for exercising an op the typed client helpers (`internal/rpc/ops.go`)
  don't cover yet, and for the Phase 0 dev-rig checks in `../PLAN.md`.
- **`poll [after]`**: sends the `poll` op with the given cursor (default `0`, meaning
  everything still pending) and prints the questions.

Every subcommand exits non-zero and prints the error on an `{"ok":false,...}` reply,
including the protocol's error code (`bad_op`, `no_provider`, `too_large`, and so on).
See `../PLAN.md` § The protocol, aab-rpc-v1, for the full table.

## Layout

```
cmd/aab/            main: config load + subcommand dispatch; dotenv.go and logfile.go
                     copied from open-discord-bridge/bridge/cmd/bridge
internal/config/     YAML config + env-resolved secrets + validation + effective snapshot
internal/transport/  copied from open-discord-bridge/bridge/internal/transport. Local- and
                     SFTP-polling tailer for the companion's events.jsonl (Phase 2+)
internal/rcon/       copied from open-discord-bridge/bridge/internal/rcon. Reconnecting
                     Factorio RCON client
internal/rpc/        the aab-rpc-v1 client: envelope parsing, typed op helpers
                     (status/tools/call/poll/answer), the oversized-command guard
```
