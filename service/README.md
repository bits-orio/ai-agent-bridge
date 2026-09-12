# AI Agent Bridge: the service

One Go binary per Factorio server. It is the agent behind the companion
mod: it polls the questions players ask over RCON, runs a model whose tools
are bounded reads of live game state, and sends a typed answer back into
the game. It is one implementation of the mod's protocol, not the only one
possible; the mod's own page documents the protocol for anyone writing
another.

## Run it

The service needs two things from you, and a model key:

1. **Where the mod's event log is.** The companion writes
   `script-output/ai-agent-bridge/events.jsonl` on the server. Give the
   service that path: a local path when it runs on the same machine, or the
   path on an SFTP host when the server is elsewhere.
2. **RCON.** The server's RCON address and password.
3. **A model key.** An [OpenRouter](https://openrouter.ai/keys) key by
   default, which buys every model it lists; or an Anthropic key with
   `model.provider: anthropic`.

Get the binary from the [releases page](https://github.com/bits-orio/ai-agent-bridge/releases),
Linux, Windows and macOS builds are attached to every release, or build it
once with Docker, no Go install needed. Then configure and run:

```sh
git clone https://github.com/bits-orio/ai-agent-bridge
cd ai-agent-bridge
make service                    # -> service/aab, or use the downloaded binary
cd service
cp aab.yaml.example aab.yaml    # the settings
cp ../.env.example .env         # the secrets
./aab -config aab.yaml run
```

### Same machine as the server

`aab.yaml`:

```yaml
factorio:
  rcon:
    address: 127.0.0.1:27015
    password_env: FACTORIO_RCON_PASSWORD
  events_file: ~/factorio/script-output/ai-agent-bridge/events.jsonl
transport: local
```

`.env`:

```sh
FACTORIO_RCON_PASSWORD=<the server's RCON password>
OPENROUTER_API_KEY=<your key>
```

### Server on a host, service anywhere else (SFTP)

`aab.yaml`:

```yaml
factorio:
  rcon:
    address: <server host>:<rcon port>
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /script-output/ai-agent-bridge/events.jsonl   # path on the SFTP host
  sftp:
    host: <sftp host>:<sftp port>
    user: <sftp user>
    password_env: SFTP_PASSWORD        # or key_path: /path/to/private_key
transport: sftp
```

`.env`:

```sh
FACTORIO_RCON_PASSWORD=<the server's RCON password>
SFTP_PASSWORD=<the SFTP password>
OPENROUTER_API_KEY=<your key>
```

### On AleForge

AleForge shows both pieces in the panel. Under **Startup** are the RCON port
and the RCON password you set (RCON is off while the password is blank).
The panel's SFTP details, host, port and username, are on the server's
settings page, and the password is your panel password. The event log sits
under the server's `script-output` folder, which you can see in the File
Manager once the mod has run: `script-output/ai-agent-bridge/events.jsonl`.

So `aab.yaml` is the SFTP block above with the server's address and the
RCON port for `factorio.rcon.address`, the SFTP host, port and username, and
`transport: sftp`; `.env` holds the RCON password, the panel password as
`SFTP_PASSWORD`, and the OpenRouter key. Run the service on any machine that
can reach the server, your own PC included.

Before the first run, and whenever something is off, run the preflight:

```sh
./aab -config aab.yaml check
```

It tries each of the three things on its own line: RCON and whether the
companion answers, the events file over the configured transport, and the
model key together with the OpenRouter balance behind it. An account with
no credit fails that line, since OpenRouter answers every request with HTTP
402 until credits are added, and a balance under a dollar is a warning. A
file that is not there yet is a warning, not a failure, since
the mod writes it on the first event; the line then lists what the folder
holds, which is how a wrong SFTP path shows itself. The service also
refuses to start until both pieces are there and says which is missing,
and on every start it writes `aab.effective.yaml` beside itself, the fully
resolved settings with each secret marked `SET (n chars)` or `MISSING`.

To run against two servers from one machine, give each its own config file
and its own env var names for the secrets, `ALEFORGE_RCON_PASSWORD` and
`ALEFORGE_SFTP_PASSWORD` for instance, in the one `.env`; and give the
second config its own `history.path` and `control_api.addr`.

## The model

One `model` section serves every provider (`aab.yaml.example` documents
each key):

| key | default | meaning |
|---|---|---|
| `model.provider` | `openrouter` | `openrouter`, `anthropic`, or `fake` (a scripted model for tests, no key) |
| `model.id` | `deepseek/deepseek-v4-pro-0813` | any model the provider lists that supports tool calling |
| `model.small` | `deepseek/deepseek-v4.1-flash` | reserved for sub-agents, unused until they land |
| `model.fallbacks` | none | OpenRouter models tried in order when `model.id` fails |
| `model.reasoning` | `low` | `off`, `model`, `low`, `medium`, `high` |
| `model.cache_ttl` | `1h` | `1h` or `5m` for the cached rules and tools on Anthropic routes |
| `model.data_collection` | `deny` | keep prompts off OpenRouter providers that train on them |

A simple question costs a fraction of a cent on the default model. Every
answer logs what it cost, and `/v1/status` on the control API totals it.

## Every setting

With no `aab.yaml` present, or `AAB_CONFIG=none`, the same settings come
from environment variables, which is what hosting panels want:

| Key | Env var | Default |
|---|---|---|
| `factorio.rcon.address` | `AAB_RCON_ADDRESS` | |
| `factorio.events_file` | `AAB_EVENTS_FILE` | |
| `factorio.sftp.host` | `AAB_SFTP_HOST` | |
| `factorio.sftp.user` | `AAB_SFTP_USER` | |
| `factorio.sftp.key_path` | `AAB_SFTP_KEY_PATH` | |
| `factorio.sftp.known_hosts_path` | `AAB_SFTP_KNOWN_HOSTS` | (omit to skip host-key checks, logged) |
| `transport` | `AAB_TRANSPORT` | `local` |
| `poll_interval` | `AAB_POLL_INTERVAL` | `1s` |
| `model.provider` | `AAB_MODEL_PROVIDER` | `openrouter` |
| `model.id` | `AAB_MODEL` | `deepseek/deepseek-v4-pro-0813` |
| `model.small` | `AAB_MODEL_SMALL` | `deepseek/deepseek-v4.1-flash` |
| `model.fallbacks` | `AAB_MODEL_FALLBACKS` (comma-separated) | (none) |
| `model.reasoning` | `AAB_REASONING` | `low` |
| `model.cache_ttl` | `AAB_CACHE_TTL` | `1h` |
| `model.data_collection` | `AAB_DATA_COLLECTION` | `deny` |
| `agent.max_rounds` | `AAB_MAX_ROUNDS` | `6` |
| `agent.max_tokens_per_question` | `AAB_MAX_TOKENS_PER_QUESTION` | `20000` |
| `agent.max_output_tokens` | `AAB_MAX_OUTPUT_TOKENS` | `4096` |
| `agent.max_tool_result_bytes` | `AAB_MAX_TOOL_RESULT_BYTES` | `4096` |
| `agent.max_tool_calls` | `AAB_MAX_TOOL_CALLS` | `30` |
| `agent.session_idle` | `AAB_SESSION_IDLE` | `3m` |
| `agent.named_session_idle` | `AAB_NAMED_SESSION_IDLE` | `30m` |
| `agent.session_max_exchanges` | `AAB_SESSION_MAX_EXCHANGES` | `10` |
| `agent.session_max_bytes` | `AAB_SESSION_MAX_BYTES` | `8000` |
| `agent.questions_per_player_per_hour` | `AAB_QUESTIONS_PER_PLAYER_PER_HOUR` | `20` |
| `agent.questions_per_hour` | `AAB_QUESTIONS_PER_HOUR` | `120` |
| `agent.max_cost_per_day` | `AAB_MAX_COST_PER_DAY` | `5.0` |
| `history.path` | `AAB_HISTORY_PATH` | `history.sqlite` |
| `control_api.addr` | `AAB_CONTROL_ADDR` | `127.0.0.1:8090` |
| `control_api.token_env` | `AAB_CONTROL_TOKEN_ENV` | `AAB_CONTROL_TOKEN` |
| `log_file` | `AAB_LOG_FILE` | `aab.log` next to the events file |

Secrets keep their own names in both modes and never sit in the YAML:
`OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`, `FACTORIO_RCON_PASSWORD`,
`SFTP_PASSWORD`, `AAB_CONTROL_TOKEN`. A `.env` file next to `aab.yaml` is
loaded at startup; a real environment variable wins over a line in it.

## What the log says

```
question 7 from Bob (player 1, force player): what is my iron plate rate
question 7 round 1: stop=tool_use in=4 out=96 reasoning=0 cached=5103/0 thinking=0c text=0c calls=item_rate
question 7 round 2: stop=tool_use in=612 out=88 reasoning=0 cached=5103/0 thinking=0c text=0c calls=submit_answer
answer 7 shape=summary rounds=2 tokens=616/184 cached=10206/0 cost=$0.0031
```

One line when a question is picked up, one per model round, one when the
answer reaches the game with what it cost. `cached=read/write` is the cache
at work; `reasoning`, `thinking` and `text` are output no player sees, the
lines to look at when a question cost more than expected.

- `poll failed: ...` once per failure streak, and `poll recovered` when the
  server is back.
- `tool mts-v1.team_clocks took 310ms, about 190ms of it on the game thread;
  players felt that` when a lookup held the game for longer than a tenth of a
  second. The RCON round trip is taken off first, so a server across the
  internet is not mistaken for a slow tool.
- `answer 7: could not deliver it, trying again next tick` when the answer
  did not land; it is offered again with no second model run, three times.
- `answer 7: the companion refused the artifact, sending a notice instead`
  when the game cannot render that shape.
- `question 7: no catalog available, leaving it pending` while the tool list
  cannot be read; the question waits rather than being answered blind.
- `question 7: the model failed: ... HTTP 402 ...` followed by `OpenRouter
  has no credit left` when the account balance is gone. The asker is told
  the account is out of credit rather than to try again; credits added at
  openrouter.ai take effect on the next question, no restart needed. The
  startup log prints the balance once, and `check` fails on an empty one.
- `question 7: new session global` when a question started a session.
- `tool ai-agent-bridge-tools.find_entities took 230ms on the game thread`
  for any tool call over 100 ms.
- `idle, 12 questions answered` every five minutes with nothing to do.

## Other subcommands

```sh
./aab -config aab.yaml status                  # protocol and mod version, tick, players, pending
./aab -config aab.yaml probe                   # the whole tool catalog, manifests verbatim
./aab -config aab.yaml rpc '{"v":1,"op":"poll"}'   # one raw protocol request
./aab -config aab.yaml poll 0                  # unanswered questions above an id
```

Each exits non-zero on an `{"ok":false}` reply and prints the error code.
None of them needs a model key.

## Control API

`/healthz` is always open. `/v1/status` reports what the service has done
and needs the bearer token when `control_api.token_env` names one that is
set:

```sh
curl -s -H "Authorization: Bearer $AAB_CONTROL_TOKEN" http://127.0.0.1:8090/v1/status
```

```json
{
  "connected": true,
  "mod_version": "1.0.0",
  "questions_answered": 12,
  "tokens_in": 21840,
  "tokens_out": 1130,
  "cost_usd": 0.0375,
  "model": "deepseek/deepseek-v4-pro-0813",
  "uptime": "42m8s"
}
```

## How a question is answered

1. The poll loop picks up a question with its force, its scope and the
   surface the asker is looking at, and reads the force labels.
2. The catalog is rebuilt if it is older than ten minutes or a tool call
   named an unknown provider: the providers list, then one manifest per
   provider.
3. Labels in the question become force names. The session for the
   question's scope and `#name` is opened, or started.
4. The model gets the rules, every tool the server exposes plus
   `submit_answer`, the session so far and the question. It calls tools; a
   round's calls run at once. A tool that fails becomes a failed tool
   result, never a failed question.
5. `submit_answer` ends the loop. The artifact is validated and clipped,
   then sent over RCON for the companion to render, which turns force names
   back into labels and bare names into icons.
6. Anything else that can end a question ends it with an artifact too: a
   round cap, a token budget, a tool-call cap, a quota, a model that stops
   talking. A refusal prints to the asker alone.
7. The rendered answer is what the session keeps for the next question.

## Building and testing

```sh
make service     # build service/aab in Docker
make test        # gofmt, go vet, go test in Docker
make lua-test    # the companion's fake-game suites
make e2e         # a real headless server, the service on the fake model, every scenario
```

`CGO_ENABLED=0` throughout: the history store is pure-Go SQLite, so the
binary is static and cross-compiles without a C toolchain. The Anthropic
and OpenRouter clients are compile-checked and tested against recorded
shapes; the end-to-end harness runs the scripted `fake` model.

## Layout

```
cmd/aab/               main: config, subcommands; run.go wires the service,
                       runner.go its state and loop, poll.go one tick, answer.go
                       one question, catalog.go the tool catalog
internal/config/       YAML and AAB_* env config, env-resolved secrets, validation,
                       the effective-config snapshot
internal/rcon/         the Source RCON protocol, replies of any size, one reconnect
internal/rpc/          the aab-rpc-v1 client: typed ops, the two-step catalog read
internal/transport/    the local and SFTP tailers for events.jsonl
internal/tools/        the one shape every tool takes
internal/catalog/      manifests turned into tools: name mangling, the parameter
                       grammar, the injected force
internal/history/      the SQLite event store and the tools that read it
internal/model/        the neutral model boundary: blocks, messages, usage
internal/model/openrouter/  chat completions over OpenRouter
internal/model/anthropic/   the Anthropic Messages API directly
internal/model/fake/   the scripted model for tests and the harness
internal/agent/        the loop, the grammar, sessions, artifacts, validation,
                       label substitution, quotas, the daily budget, the price table
internal/controlapi/   /healthz and /v1/status
```

## Licence

MIT, see [LICENSE](../LICENSE) at the repository root, which covers the
service, the companion mod and the tooling alike. The Go dependencies are
all permissive: the Anthropic SDK and yaml.v3 under MIT, pkg/sftp and
golang.org/x/crypto under BSD, modernc.org/sqlite under BSD.
