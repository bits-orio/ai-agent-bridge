# AI Agent Bridge: Service

Go binary, one process per Factorio server. It drives the companion mod's `aab-rpc-v1`
protocol over RCON: it picks up the questions players ask, runs an agent whose tools are
bounded reads of live game state, and sends a typed answer back into the game. See
[../CONTEXT.md](../CONTEXT.md) for the domain words and [../PLAN.md](../PLAN.md) for the
phased plan.

The service is the only party that polls. Nothing runs inside the game until a question
arrives.

## Build

Needs Go 1.25+.

```sh
cd service
go vet ./...
go test ./...
go build -o aab ./cmd/aab
```

With no Go toolchain on the machine, run the same gates in a container:

```sh
cd service
docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 golang:1.25-alpine \
  sh -c "gofmt -l . ; go vet ./... && go test ./... && go build -o aab ./cmd/aab"
```

`CGO_ENABLED=0` is deliberate: the history store uses a pure-Go SQLite driver, so the
binary stays static and cross-compiles without a C toolchain.

## Configure

```sh
cp aab.yaml.example aab.yaml   # edit paths and the RCON address
cp ../.env.example .env        # fill in the API key and the RCON password
```

Secrets are read from the env vars named in `aab.yaml` (`rcon.password_env`,
`anthropic.api_key_env`, `control_api.token_env`), never from the YAML itself. A `.env`
file next to `aab.yaml` is loaded at startup, and a real environment variable always
wins over a line in it.

With no `aab.yaml` present (or `AAB_CONFIG=none` set), the service reads its whole
configuration from `AAB_*` environment variables instead:

| Key | Env var | Default |
|---|---|---|
| `factorio.rcon.address` | `AAB_RCON_ADDRESS` | |
| `factorio.events_file` | `AAB_EVENTS_FILE` | |
| `transport` | `AAB_TRANSPORT` | `local` |
| `poll_interval` | `AAB_POLL_INTERVAL` | `1s` |
| `anthropic.model` | `AAB_MODEL` | `claude-sonnet-5` |
| `anthropic.thinking` | `AAB_THINKING` | `off` |
| `anthropic.effort` | `AAB_EFFORT` | (unset, the model's default) |
| `anthropic.cache_ttl` | `AAB_CACHE_TTL` | `1h` |
| `agent.max_rounds` | `AAB_MAX_ROUNDS` | `6` |
| `agent.max_tokens_per_question` | `AAB_MAX_TOKENS_PER_QUESTION` | `20000` |
| `agent.max_output_tokens` | `AAB_MAX_OUTPUT_TOKENS` | `4096` |
| `agent.max_tool_result_bytes` | `AAB_MAX_TOOL_RESULT_BYTES` | `4096` |
| `agent.memory_ttl` | `AAB_MEMORY_TTL` | `10m` |
| `agent.questions_per_player_per_hour` | `AAB_QUESTIONS_PER_PLAYER_PER_HOUR` | `20` |
| `history.path` | `AAB_HISTORY_PATH` | `history.sqlite` |
| `control_api.addr` | `AAB_CONTROL_ADDR` | `127.0.0.1:8090` |
| `control_api.token_env` | `AAB_CONTROL_TOKEN_ENV` | `AAB_CONTROL_TOKEN` |
| `log_file` | `AAB_LOG_FILE` | `aab.log` next to the events file |

Secrets keep their own names in both modes: `ANTHROPIC_API_KEY`,
`FACTORIO_RCON_PASSWORD`, `AAB_CONTROL_TOKEN`, `SFTP_PASSWORD`.

Every load, in either mode, writes `aab.effective.yaml` to the working directory: the
fully-resolved config with secrets reduced to `SET (n chars)` or `MISSING`, so you can
see exactly what the service resolved without exposing anything. It is output only, the
service never reads it back.

## Run

```sh
./aab -config aab.yaml run
```

That is the one you leave running. It connects over RCON, builds the tool catalog, tails
the events file into the history database, polls for questions every `poll_interval`,
answers each in order, and serves the control API. Every question logs two lines, one
when it is picked up and one when the answer reaches the game:

```
question 7 from Bob (player 1, force player): what is my iron plate rate
answer 7 shape=summary rounds=2 tokens=340/96 cached=12600/0 cost=$0.0038
```

A few other lines are worth knowing:

- `poll failed: ...` once per failure streak, not once per poll. A server that is down
  costs one line, and `poll recovered` says when it came back.
- `answer 7: could not deliver it, trying again next tick: ...` when the answer itself
  did not land. The artifact is already paid for, so it is offered again with no second
  model run. After three failures the service says it is giving up on that question.
- `answer 7: the companion refused the artifact, sending a notice instead: ...` when the
  game cannot render that shape. The asker is told so once, and the question is done.
- `question 7: no catalog available, leaving it pending ...` when the service cannot read
  the server's tools. The question waits for the next tick rather than being answered out
  of the system prompt with no game data in it.
- `poll reply too large at 16 questions, trying 8` when a page of long questions outgrows
  the companion's reply cap. The page halves until a reply fits and doubles back toward
  sixteen after every poll that lands.
- `idle, 12 questions answered` every five minutes with nothing to do, so an idle
  service is tellable from a wedged one.

No cursor is written to disk. In memory the service tracks the highest question id it has
finished with, delivered or given up on, and polls from there, so a question it abandoned
cannot sit at the head of every later page. The companion serves the oldest unanswered
questions above that id, a page at a time, so restarting the service resumes from zero
rather than re-answering everything the companion's ring still holds.

The other four subcommands drive the protocol by hand, which is what the Phase 0 checks
in [../TESTING.md](../TESTING.md) use:

```sh
./aab -config aab.yaml status
./aab -config aab.yaml probe
./aab -config aab.yaml rpc '{"op":"poll","after":0}'
./aab -config aab.yaml poll 0
```

- **`status`**: protocol version, mod version, tick, player count, pending questions, and
  the highest question id the save has issued.
- **`probe`**: the full catalog every provider on the server exposes, manifests verbatim,
  read the way the service reads it: the providers list, then one manifest per provider.
  A provider whose manifest does not arrive or does not decode is skipped with a line
  naming it; the rest of the catalog still comes through.
- **`rpc <json>`**: one raw `aab-rpc-v1` request, a JSON object carrying its own `"op"`.
- **`poll [after]`**: unanswered questions with an id above `after`, oldest first.

Each exits non-zero on an `{"ok":false,...}` reply and prints the protocol's error code.

## Without an API key

Set `anthropic.model` to `fake` and the service runs a scripted model instead: it picks
one tool from keywords in the question, then submits an artifact carrying that tool's
result. It calls no API, needs no key, and answers the same way every time, which is what
the end-to-end harness runs against a real Factorio server.

## Control API

`/healthz` is always open, for a container health check.

`/v1/status` reports what the service has done, and needs the bearer token when
`control_api.token_env` names one that is set:

```sh
curl -s -H "Authorization: Bearer $AAB_CONTROL_TOKEN" http://127.0.0.1:8090/v1/status
```

```json
{
  "connected": true,
  "mod_version": "0.2.0",
  "questions_answered": 12,
  "tokens_in": 21840,
  "tokens_out": 1130,
  "cost_usd": 0.1375,
  "model": "claude-sonnet-5",
  "uptime": "42m8s"
}
```

The cost is the operator's own money, so it is reported rather than hidden (ADR 0005).
Prices come from a small table in `internal/agent/cost.go`; a model the table does not
know prices at zero rather than at a guess. The `cached=read/write` pair in the answer
line is where most of a question's input goes: the rules and the tool definitions are
sent with a cache breakpoint, so after the first question in five minutes they are read
back at a tenth of the input price, and a second breakpoint on the newest user block
does the same for earlier rounds of the same question. Two caps keep the rest small:
`max_output_tokens` bounds one turn's output, thinking included, and
`max_tool_result_bytes` cuts a long tool result before the model reads it, with a note
telling it to ask for fewer rows. Thinking is off unless `anthropic.thinking` turns it on:
a Claude 5 model thinks by default, and on a one-tool question that thinking was most of
the output tokens. The run log also prints one line per model round:

```
question 3 round 1: stop=tool_use in=4 out=96 cached=5103/0 thinking=0c text=0c calls=top_produced
question 3 round 2: stop=tool_use in=612 out=88 cached=5103/0 thinking=0c text=0c calls=submit_answer
```

`thinking` and `text` are characters the model produced that no player sees; `calls`
is what it asked for. A round whose `out` is large with no calls to show for it is the
line to look at when a question cost more than expected.

## The RCON client

`internal/rcon` speaks the Source RCON protocol itself: an int32 length, an int32 request
id, an int32 type, the body, two NUL bytes. The library it replaced refused any command
over 1000 bytes and any reply over 4 KB, both its own client-side constants. Factorio
accepted a 1,000,042-byte command on 2.0.77 and returned 4 MB replies whole
([../TESTING.md](../TESTING.md) check 1.5), and the 1000-byte ceiling had made every
table and list answer undeliverable.

`MaxCommandLen` is now 262144: a budget the service picks rather than a limit the game
imposes. An empty or over-long command is refused before anything is dialled, so a bad
command never tears down a healthy connection, and a genuine connection error reconnects
once.

## How a question is answered

1. The poll loop picks up a question with its force hint and the asker's name.
2. The catalog is rebuilt if it is older than ten minutes, or if the last tool call
   reported an unknown provider: the providers list, then one manifest per provider, so
   one mod's oversized manifest costs only its own tools. Nothing about it is stored
   (CONTEXT.md invariant 3). With no catalog at all the question waits instead, since an
   answer with no game tools behind it is a confident guess.
3. The agent sends the system prompt, the asker's last few exchanges and the question,
   with every tool the server exposes plus `submit_answer`.
4. The model calls tools. A round's calls all run at once and come back in one message.
   A tool that fails becomes a failed tool result, never a failed question.
5. `submit_answer` ends the loop. The artifact is validated and clipped, then sent back
   over RCON for the companion to render.
6. Anything else that can end a question, a round cap, a token budget, a quota, a model
   that stops talking, ends it with an artifact too. A player always gets an answer.
7. The companion renders the artifact and only then marks the question answered, so a
   question the service could not deliver comes back on the next poll and is delivered
   again from the artifact already in hand.

## Layout

```
cmd/aab/             main: config load and subcommand dispatch; run.go wires the
                      service up, runner.go holds its state and loop, poll.go one tick,
                      answer.go one question, catalog.go the tool catalog; dotenv.go and
                      logfile.go copied from open-discord-bridge
internal/config/      YAML config, AAB_* env config, env-resolved secrets, validation,
                      the effective-config snapshot
internal/rcon/        the Source RCON protocol, written out: auth, exec, one
                      length-prefixed reply of any size, one reconnect
internal/rpc/         the aab-rpc-v1 client: envelope parsing, typed op helpers
                      (status/providers/manifest/tools/call/poll/answer), the two-step
                      catalog read, per-provider manifest decoding, the
                      oversized-command guard
internal/transport/   copied from open-discord-bridge. Local and SFTP polling tailer for
                      the companion's events.jsonl
internal/tools/       the one shape every tool takes: name, description, schema, call
internal/catalog/     the companion's tools reply turned into tools: name mangling,
                      the parameter grammar, the injected force argument
internal/history/     the SQLite event store and the tools that read it
internal/model/       the neutral model boundary: blocks, messages, usage, one interface
internal/model/anthropic/  that interface on the official Anthropic Go SDK
internal/model/fake/  that interface, scripted, for tests and the harness
internal/agent/       the loop, the artifact shapes, validation and clipping, per-player
                      memory and quota, the price table
internal/controlapi/  /healthz and /v1/status
```
