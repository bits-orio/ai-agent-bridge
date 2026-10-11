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
| `agent.max_round_tool_result_bytes` | `AAB_MAX_ROUND_TOOL_RESULT_BYTES` | `24000` |
| `agent.max_tool_calls` | `AAB_MAX_TOOL_CALLS` | `30` |
| `agent.session_idle` | `AAB_SESSION_IDLE` | `3m` |
| `agent.named_session_idle` | `AAB_NAMED_SESSION_IDLE` | `30m` |
| `agent.clarify_idle` | `AAB_CLARIFY_IDLE` | `10m` |
| `agent.personality` | `AAB_PERSONALITY` | `off`; or `factorio`, or your own words (see Voice) |
| `agent.session_max_exchanges` | `AAB_SESSION_MAX_EXCHANGES` | `10` |
| `agent.session_max_bytes` | `AAB_SESSION_MAX_BYTES` | `8000` |
| `agent.questions_per_player_per_hour` | `AAB_QUESTIONS_PER_PLAYER_PER_HOUR` | `20` |
| `agent.questions_per_hour` | `AAB_QUESTIONS_PER_HOUR` | `120` |
| `agent.max_cost_per_day` | `AAB_MAX_COST_PER_DAY` | `5.0` |
| `history.path` | `AAB_HISTORY_PATH` | `history.sqlite` |
| `control_api.addr` | `AAB_CONTROL_ADDR` | `127.0.0.1:8090` |
| `control_api.token_env` | `AAB_CONTROL_TOKEN_ENV` | `AAB_CONTROL_TOKEN` |
| `ledger.enabled` | `AAB_LEDGER_ENABLED` | `true` |
| `ledger.dir` | `AAB_LEDGER_DIR` | the directory holding the config file |
| `briefing.enabled` | `AAB_BRIEFING_ENABLED` | `true` |
| `log_file` | `AAB_LOG_FILE` | `aab.log` next to the events file |

Secrets keep their own names in both modes and never sit in the YAML:
`OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`, `FACTORIO_RCON_PASSWORD`,
`SFTP_PASSWORD`, `AAB_CONTROL_TOKEN`. A `.env` file next to `aab.yaml` is
loaded at startup; a real environment variable wins over a line in it.

### Voice

`agent.personality` sets the voice answers are written in. `off` is plain,
and so are `false`, `no`, `none`, `disabled`, `0` and `n`, in any case.
`factorio` is a fixed preset: a dry engineer on the factory floor, on the
summary line only. Anything else is your own words, for instance
`"Talk like a ship's captain: brief and salty."` They are kept to one line of
at most 500 characters, any rich-text tag is taken out, and they are always
followed by the sentence the preset ends with: "Flavour never changes a
number, a unit, an item name or a table cell." A voice changes how an
answer sounds, never what it says.

An admin can set the same thing in game: Bot personality
(`aab-personality`) in Mod settings on the Map tab, or `/aab-personality`.
A value set there wins over the config, so you can change the voice without
a restart; `off` there turns it off, and blank leaves it to the config. The
companion holds that setting to 500 bytes, so the service never cuts what it
sends. The ledger records which voice each question ran in: `off`, the
preset's name, or `custom`. The log says it too: once at startup, and once
each time the in-game setting starts or stops overriding the config. The
voice is the last part of the system prompt and one value for the server at
a time, so changing it costs one cache write, not one per question.

### History

The service keeps the event log in `history.path`, one SQLite file, and the
history tools read it. One file outlives the map it started on, so every row
records its map epoch, and every read covers the current map only: the
epoch of the newest row. An earlier map's rows, chat included, are never
read again, even when a force or a player has the same name. When the
companion names its map in a `session` line at the start of every game
session, another map starts a new epoch, and the same map keeps its epoch
even after a save loaded far back. Without that line, the tick decides:
a drop into a new map's first five minutes, or a drop of more than thirty
minutes, is a new map; a smaller drop is an autosave loaded after a crash,
the same map.

Two maps made from one fixed seed carry the same id, so the same id is not
always the same map. Two rules tell them apart. A drop into the map's first
five minutes is a new map. And a status counting fewer questions issued than
the epoch has logged is a new map, as is a `session` line once the companion
writes that count into it: the count never goes back while one map goes on.
The second rule cannot tell a same-seed map from a save loaded from before
some of the logged questions, so it treats that save as a new map too, and
the rows before it are no longer read: it errs on the private side. One case
still gets through: a same-seed map the service first reaches after its
fifth minute, by when it has issued as many questions as the map before it
logged, none at all when nobody asked one there.

The companion's status names its map too, and the service reads it at
startup, every 30 polls, and first thing whenever the companion answers
again after it could not be reached. A `session` line can go unseen: the
server changed maps while the service was down, so the line sits before the
end of the file the service starts reading at, or `aab-events-enabled` is
off and the file is never written. Either way, when the status shows a new
map by the same rules, the service records a `session` row of its own, with
`"source":"status"`, and reads move to the new map's epoch from then on.
When the real `session` line turns up after it, it joins that epoch. A
status is judged against history as it stood before the status call went
out, so a line the service stored while the call was out never makes the
status look like a drop.

A file from an older service is migrated on the first start, in place: every
row gets its epoch and who may read it. The 21,554 rows of a
live server's month took about 0.4 seconds. A chat line logged before lines
carried a scope stays private to its force, unless its map only ever had
one force and no private row, where everyone heard it and it becomes
public. An answer row is as private as its question in the same epoch, and
unreadable when that question is not there. No tool reply carries a row id
or a question id: both count up across every channel, so their gaps would
count another channel's private rows.

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

- `question 7 round 1: submit_answer refused (over_cap): Not sent, ... Line
  2 is 410 characters, the cap is 320: ...` when the model's answer went
  back to it, one line per refused answer with the reason it read: an
  answer over a cap (`over_cap`), one that is not an answer shape
  (`invalid`), one sent beside lookups (`beside_reads`), a second one in
  the same round (`second_submit`), or one in a round over the lookup cap
  (`lookup_cap`). The round's ledger record carries the same list as
  `submit_refusals`.
- `question 7: the answer was over the caps and went out cut: ...` when the
  model had been sent back twice already, or no round was left to rewrite
  it in. The question's ledger record says `clipped: true`.
- `question 7: no rewrite came back, sending the answer refused for its
  length instead of ...` when the question ended, or the model wrote prose,
  before a rewrite arrived; `question 7 round 3: the model failed before a
  rewrite came back` when the model failed instead.

- `poll failed: ...` once per failure streak, and `poll recovered` when a
  streak of refused polls ends.
- `poll: the companion answers again after an outage (map
  2894571-0-1804289383, tick 600, 2 question(s) asked): a restart may have
  loaded another map or an older save, so polling starts from the first
  question again, with no answer held and no session kept` the first time
  the companion answers after an outage: a poll, a status read or a
  delivery that got no reply at all, or a reply that came over a new RCON
  connection. A restart is the only way a headless server loads a save, so
  the service treats every outage as one: it forgets the answers it held
  and every session, and answers whatever is still waiting from the first
  question. A network blip costs the same, a question answered twice and a
  follow-up that starts fresh, and so does a save that holds the game for
  longer than the 30-second RCON timeout.
- `answer 7: dropped unsent, no reply or a new connection since the poll:
  after a restart this id may name another question` when a delivery got no
  reply, or the RCON connection the question was polled on broke before the
  answer was ready. An answer is only ever sent over the connection its
  question was polled on.
- `poll: the game went back (tick 600, last question id 2, poll cursor 41): a
  new map or an older save, polling from the start` when the companion's
  tick or its question ids went back, or its map id changed, read from its
  status every 30 polls with no outage in between. A map change adds `, map
  2894571-0-1804289383, was 2894571-0-77` inside the brackets. The service
  forgets the answers it held and every session, and answers the new map's
  questions from the first.
- `history: a new map from the companion's status (map 2894571-0-1804289383,
  tick 600), ahead of any session line: history reads start again from
  here` when the status named a new map before any `session` line did, at
  startup or at a later look. History reads stop reaching the earlier map
  from that moment.
- `voice: custom, from the map's personality setting, which overrides
  agent.personality` and `voice: off, from agent.personality: the map's
  personality setting is blank` when the in-game setting takes over or lets
  go; `voice: off, from agent.personality` at startup.
- `tool mts-v1.team_clocks took 310ms, about 190ms of it on the game thread;
  players felt that` when a lookup held the game for longer than a tenth of a
  second. The RCON round trip is taken off first, so a server across the
  internet is not mistaken for a slow tool.
- `answer 7: could not deliver it, trying again next tick` when the companion
  replied but the answer did not land; it is offered again with no second
  model run, three times.
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
- `briefing: question 7 got no usable game_time reply within 2s, briefing
  skipped` when the per-question snapshot could not be assembled in time or
  the companion refused it; the question still answers, just without one.
- `tool ai-agent-bridge-tools.find_entities took 230ms on the game thread`
  for any tool call over 100 ms.
- `idle, 12 questions answered` every five minutes with nothing to do.

## Other subcommands

```sh
./aab -config aab.yaml status                  # protocol and mod version, tick, players, pending
./aab -config aab.yaml probe                   # the whole tool catalog, manifests verbatim
./aab -config aab.yaml rpc '{"v":1,"op":"poll"}'   # one raw protocol request
./aab -config aab.yaml poll 0                  # unanswered questions above an id
./aab stats                                    # report on the observability ledger
```

Each of the first four exits non-zero on an `{"ok":false}` reply and prints
the error code. None of them needs a model key.

`stats` is pure arithmetic over the ledger files already on disk: it needs
no RCON password, no model key, and no reachable Factorio server, so it
runs before any of that is checked. `./aab stats 2026-09-12` reports one
day, `./aab stats 2026-09-01 2026-09-12` an inclusive range, no args every
ledger file found. It reads `ledger.dir` out of `-config` the same way
every other subcommand does; pass `-ledger-dir` instead to point it
straight at a directory without touching a config file at all.

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
   surface the asker is looking at, and reads the force labels. Every 30
   polls it reads the companion's status; when the tick or the question ids
   went back, or the map id changed, a new map or an older save, it polls
   from the first question again. When a call got no reply, or a reply came
   over a new RCON connection, the next tick reads the status first and
   starts again before answering anything: no held answer, no session, the
   cursor back at the first question.
2. The catalog is rebuilt if it is older than ten minutes or a tool call
   named an unknown provider: the providers list, then one manifest per
   provider.
3. Labels in the question become force names. The session for the
   question's scope and `#name` is opened, or started.
4. Unless `briefing.enabled` is off, a compact snapshot of game state (who's
   online, what each force is researching, the last few chat lines) is read
   before the model is asked anything, on its own two-second budget
   (docs/design/phase4-spec.md section 3). It rides in the user turn beside
   the question, so a good share of questions ("who's online", "what time is
   it") never need a tool call at all. A slow or failed attempt is best
   effort: the question answers exactly as it would without one. Chat, here
   and in every history tool, is only what the question's scope may read: a
   private channel's lines reach a question asked in that same channel and
   no other ([SECURITY.md](https://github.com/bits-orio/ai-agent-bridge/blob/main/SECURITY.md), Chat privacy).
   Every chat line carries its speaker's force and says whether it was said
   in a private channel, so a force's own chat reads as its own.
5. The model gets the rules, every tool the server exposes plus
   `submit_answer`, the session so far, the briefing and the question. It
   calls tools; a round's calls run at once. A tool that fails becomes a
   failed tool result, never a failed question. A call to a name that is no
   tool runs the one tool it can only mean: a prefix invented, doubled,
   dropped or wrong, or a name within two typed characters of exactly one
   tool. The ledger keeps the name asked for as `corrected_from`. A round whose results
   together would pass `max_round_tool_result_bytes` refuses whichever
   result would push it over, not the round: the results that already fit
   are kept exactly as their tools returned them.
6. `submit_answer` ends the loop. The artifact is checked against the
   caps: a summary of 3 lines, a list of 10 items, a table of 5 columns by
   8 rows, a comparison of 5 rows, 320 characters a cell and 12,000 bytes
   in all. An answer over a cap goes back to the model, naming what is
   over, at most twice and only while it has a round left to rewrite it
   in. After that it is cut with an ellipsis: between words when the
   nearest break keeps at least half the cap, at the character otherwise,
   so one long word cannot take a whole line with it. If the question
   ends, or the model writes prose or fails, before a rewrite comes back,
   the refused answer goes out cut rather than a notice. The artifact is
   then sent
   over RCON for the companion to render, which turns force names
   back into labels and bare names into icons. An answer that is itself a
   clarifying notice (asking which surface, which force) marks the session
   awaiting the player's reply: its idle window widens to `clarify_idle`
   until the next question lands in it, so the follow-up is not left
   talking to a session that already closed.
7. Anything else that can end a question ends it with an artifact too: a
   round cap, a token budget, a tool-call cap, a quota, a model that stops
   talking or fails. A refusal prints to the asker alone. A model that
   fails with no answer in hand gives the asker's hourly slot back, but the
   rounds it already ran still count toward `max_cost_per_day`.
8. The rendered answer is what the session keeps for the next question.

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
internal/chatscope/    a question's scope, carried to every history read
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
