# Safety and security

What a player can do, what the model can do, what it can cost, and the
guards on each. Reviewed 2026-09-11 against the whole tree and the chat
privacy section again on 2026-10-11; every guard here has a test.

## Who can do what

| actor | can | cannot |
|---|---|---|
| A player in game | `/ask` (or `/aab-ask`), the chat prefix, `/ask new`, `/ask #name`, `/ask sessions`, `/ask help`, `/ask top`, `/ask card` | run `/aab-rpc` (refused with one private line; only RCON and the server console reach it), see another team's private question or private chat through an answer (see Chat privacy), forge an answer in the bot's name. An admin alone can change the bot's voice with `/aab-personality`; any player can read it |
| Another mod on the server | ask through `ai-agent-bridge-v1.ask`, expose tools, a chat scope, force labels | anything a mod could not already do: a mod is server code |
| The service, over RCON | the `aab-rpc` protocol: poll, catalog, tool calls, answers, and `say`, one unprompted line to everyone while `aab-allow-say` is on | run the diagnostic ops `ping`, `big`, `write` and `pcall_test` on a server where `aab-selftest-ops` is off, the default; anything else; the RCON password itself is the server's root, so keep the port on localhost or behind a firewall, as Factorio's own docs say |
| The model | call the tools in the catalog, with arguments it chooses, and submit one answer | invent a tool, call a function a provider did not list, write anything: every tool is a bounded read |

Player-typed text (questions, names, chat) reaches the model as data. The
system prompt says so, and the worst a prompt injection can do is spend the
asker's own quota on odd tool calls.

## Spam and cost, the blanket guards

| guard | where | default | what it stops |
|---|---|---|---|
| ask cooldown per player | companion, `aab-ask-cooldown-seconds` | 5 s | a held-down key: refused privately before the echo, the ring and the service |
| asks per minute, whole server | companion, `aab-asks-per-minute` | 30 | chat flooding by any number of players |
| asks per minute from other mods, whole server | companion, fixed | 60 a game minute | a loop in another mod filling the question ring; over it, `ask` returns nil and the log says so once a minute |
| question text | companion | 400 bytes | a wall of text as a question |
| questions per player per hour | service, `agent.questions_per_player_per_hour` | 20 | one player's cost |
| questions per hour, whole server | service, `agent.questions_per_hour` | 120 | everyone's cost together |
| USD per rolling day | service, `agent.max_cost_per_day` | 5.00 | the bill, whatever the model turns out to cost |
| tokens per question | service, `agent.max_tokens_per_question` | 20,000, a cached input token counting a tenth | one runaway question |
| rounds per question | service, `agent.max_rounds` | 6 | a model that never submits |
| lookups per question | service, `agent.max_tool_calls` | 30; a round over the cap is refused and the model answers from what it has | dozens of surface scans from one question |
| output tokens per turn | service, `agent.max_output_tokens` | 4,096 | a model that writes essays |
| tool result bytes | service, `agent.max_tool_result_bytes` | 4,096 | a result that floods the context |
| answer size | service and companion | 12,000 bytes and 320 characters a line in the service; in the companion 16,000 encoded bytes in all and 1,280 bytes a line, 8 rows | chat spam per answer. An answer over a cap is sent back to the model to rewrite, at most twice, and cut only after that |
| live sessions | service | 200 | a player inventing a new `#name` every question |
| time per question | service, `agent.question_timeout` | 90 s (10 min for ollama) | one slow question holding every question behind it |
| milestone toasts per hour | service, `toasts.max_per_hour` | 6 | a burst of milestones turning the bot into a chat flood; the rest are dropped with a log line |
| unprompted lines allowed | companion, `aab-allow-say` | on | the service speaking when nobody asked; off, the `say` op answers `bad_op` |
| question ring | companion | 64 rows | an unpolled server growing storage; the oldest answered row goes first, and an unanswered one only when all 64 wait, logged and written as `question_dropped` |

A refused question is a private notice to the asker, never a line to the
audience, so a player's spam cannot become the server's. Questions asked by
another mod through the interface skip the per-player cooldown and the
per-minute limit on players, no player being behind them, but have the cap of
their own above, and still count against the service's caps. A player's
cooldown is not reset by leaving and rejoining. A mod's force names are
clipped and dropped unless they name a real force, and a scope it passes is
cleaned the way a scope provider's answer is.

## The game thread

Every tool runs inside the RCON command on the game thread, so a slow tool
is a stutter every player feels. The tools are bounded reads: engine counts
and finds with a hard limit (2,000 entities for `find_entities`), sorted and
cut lists, one flow read per item and quality. The service logs any tool
call that took over 100 ms so an operator can see which one. Two known
costs to watch: `production_since` on a game with every quality unlocked
makes one engine call per quality and precision window, and `list_players`
sorts every player the force has ever had before cutting.

A tool reply is fitted to 3,800 bytes and paged with `offset`, so no single
call sends more than that into the model's context.

Polling is one small command a second, the labels and scope probes are one
call each per question, and the catalog is rebuilt every ten minutes.

## Chat privacy

Every chat line in `events.jsonl` records who could read it: a `scope` key
and a `private` flag. A scope provider decides when one answers for the line.
With none, the line reached the speaker's force alone, so it is private to
that force once a second force has players (online or not). With a provider
installed that leaves the line to that rule, or that fails, the line stays
private to the force but is stored where no question reads it back, because
the force may have held other groups before. A provider that fails may have
been the one that knew the line was private, so after a failure only a private
answer from another provider stands. A provider's private flag reads as private
unless it is false or absent. A server console line is public. A question
carries the scope it was asked in. A question asked with the chat prefix takes
its chat line's scope, so on a server with split forces and no provider it is
private to the speaker's force. An `/ask` question still goes global past a
failed provider: it reads public rows only, but its echo and answer print to
the whole server.

The service applies one rule on every history read, in SQL, never by
telling the model:

| the question was asked | it reads |
|---|---|
| privately, in scope X | X's private rows and every public row |
| privately, in scope Y | Y's private rows and every public row, never X's |
| in the open, by anyone, a member of X included | public rows only |
| by a companion that sends no scope | public rows only |

That covers `recent_chat`, `catch_up`, `recent_events`, `last_event`,
`count_events` and the briefing's chat. A count covers only the rows the
question may read, so a private channel's volume stays private too. An
answer to a private question joins a session that only the same scope reads
back, even when a provider hands the same key to a public channel.

`recent_chat`, `catch_up` and the briefing's chat show each line's speaker
force and private flag. A private line still reaches only a question asked in
its own channel, so the flag reveals nothing new. Reads fail closed: only a
literal `false` is public, a private row with no usable key matches no
question, and an answer row with no question behind it is unreadable.

A history file from before lines carried a scope is migrated when the
service opens it. Each old chat line becomes private to the force it was
logged with, the safe side. One exception, and only the migration applies it:
when a map only ever named one force and no row there says it was private,
every player heard those lines, so they become public. Ingest never does this.
New lines are public again on a one-force server once the companion logs
scopes.

### Multi-map history

One history file spans several maps, because the service keeps tailing into it
after the server starts another map or loads an older save. Before 2026-10-11,
today's team-4 asking privately could read every earlier map's team-4 private
chat. Now no read crosses a map epoch. The epoch follows the companion's map
id: the `session` line at the top of the event file and the `map_id` in the
`status` reply. The same id dropping into a map's first five minutes is a new
map, as is a question count below what the epoch already logged. Where no map
id exists, a tick that drops into the first five minutes or by more than thirty
minutes is a new map. A rollback to a save from before logged questions also
starts an epoch and its earlier rows go unread: fail closed, accepted. The
residual case is two maps from one fixed seed where neither logged a higher
question id than the other had issued.

After an outage (a call with no reply, a reply over a new RCON connection, or a
save that blocks RCON past the 30-second I/O timeout) the service starts again
before it answers anything: it drops held answers and ends sessions. An answer,
and the notice sent for a refused artifact, only ever goes over the RCON
connection its question was polled on.

### Asker mode

With `aab-answer-audience` set to `asker`, a global question prints to its
asker alone. The companion marks it `to_asker` when it is asked, so a later
change of the setting does not rewrite it, and puts that on its event line and
its poll row. The service then stores the question under the private scope
key `asker:<name>` and keeps its session to that asker: no other player's
question reads it, and neither does one asked in the open. With no player name
it is read by nobody. A private audience that has gone, or a force that took
the name later, hears nothing; the asker alone does.

### Provider key contract

A scope provider's key is opaque and compared whole, byte for byte, and is at
most 64 bytes. A longer key is refused, never cut, and its answer is private
under the global key, so two keys that share their first 64 bytes never become
one audience. Labels are cut at 64 bytes and tags at 200, on a character
boundary. A private key is a read key for the whole life of the history, so it
must name one audience and never a later one. A provider that reuses names adds
a generation to the key. MTS is the worked example: its key is
`team-N@<claim tick>`; a team already holding a slot when a save first loads
that version gets `team-N@legacy-g<generation>-t<update tick>`; a player on a
free slot gets the global scope. Notes:

- Rows filed under the old bare key (`team-4`) match no key sent after the
  update, so nobody reads them back, including the same team. A one-time loss
  of context, on the safe side.
- The key spans a team's whole life in a slot. Members who join later share it
  and can be answered from that team's earlier private chat. A member who
  leaves stops matching it. Keys are unique per team within one map; across
  maps the epochs keep them apart.
- Two narrow gaps: a claim, a release and a new claim of one slot inside one
  tick would give two teams one key, and loading a save from before the update
  inside one epoch could make two teams share `team-N@legacy`.

### Deploy order

Update the service first, or both together. An older service applies no
visibility at all, ignores `scope` and `private`, and stores the `session`
line as an ordinary event row with no player or force. A new service with an
older companion keeps legacy chat private to its force and falls back to the
tick rule for epochs. The provider key change does not depend on order: the
new keys never match old rows.

### The bot's voice

`/aab-personality` shows the bot's voice to anyone. Only an admin can change
it, because the voice is one value for the whole server. A free-text voice is
sanitised to one short line with no rich-text tags and always ends with a guard
sentence, so it changes how an answer sounds and never a number, a unit, an item
name or a table cell.

### The map epoch's two guards

An epoch that a `status` reply starts has no session line of its own at first,
and the file may still hold lines from the map before it. Two guards cover
that. Lines that arrive before the epoch's own `session` line, or before the
tail has caught up with the status, are stored unreadable and lost to every
read. And a `session` line that names a different map than the status did is
held: its rows read as nothing until a status names that map, and for good if
a status names another. The tail itself starts at the end of the file on a
restart, so a stale session is not replayed; it starts at the top only for a
file that does not exist yet, or when the file shrinks or its first line
changes.

### Milestone toasts

A toast is a line nobody asked for, so it follows stricter rules than an
answer:

- It is made only from public history rows of the current map epoch. A
  private row, a row the history guards held and a quarantined row never
  produce one, so a toast cannot leak what a private chat or another map
  held.
- It is made only from what happens while the service runs and someone is
  online, never from history it reads at start, and is dropped when the event
  is over five game minutes old or the companion is out of reach.
- In `toasts.mode: model` the model gets no tools and no history, only the
  milestone sentence, and so cannot be asked to say anything else. It is paid
  from `agent.max_cost_per_day`, and a failed, late or multi-line reply
  becomes the plain sentence.
- At most `toasts.max_per_hour` go out in an hour (6 by default).
- The `say` op prints through the same guarded printer as an answer, to
  everyone, never to one player. The runtime setting `aab-allow-say` turns it
  off, and an admin can change it in game. The companion logs the shape of a
  line, never its text.

### Answers that print nothing

In asker mode a global question whose asker has left prints to nobody, with a
log line, instead of to the server. The `answer` op refuses an artifact that
would print nothing, or that encodes to more than 16,000 bytes, as
`bad_artifact`, counting the title and what the shape
prints within the caps. Colour and font tags, empty tags like `[img=]`, spaces
and control characters do not count.

## Data

What the service keeps on disk, and who can read it. Every file below holds
player text, including questions asked in a team's private chat, since the
operator's service answers them.

| file | holds | mode |
|---|---|---|
| `history.path`, SQLite | every chat line, question and event, private ones included, for every map it has seen | `0600`, narrowed on open if an older service left it wider |
| the ledger, `ledger.dir`, one file a day | one record per question and per round: cost, tokens, tool calls with their arguments, refusals with the arguments sent | `0600`, a new directory `0700` |
| the log, `log_file` | what the service did, with player text quoted so it cannot forge a line | `0600` |
| `agent.daily_spend_path` | the rolling day's spend | `0600` |

Back these up or delete them as private data. `events.jsonl` on the game
server holds the same lines and is the game server's file, with whatever mode
Factorio gives it. It grows for the life of a save; turn `aab-events-enabled`
off to stop it, or rotate it from the service's host. Prompts leave the server
only for the model provider; the default OpenRouter routing denies providers
that train on prompts.

### SFTP host key

Over SFTP the service checks the server's key against `known_hosts_path`. With
no such file it accepts any key, which lets a machine between you and the
server pose as it, and logs a warning at every connect. That is the default,
as it was before the option existed. Set `factorio.sftp.insecure_ignore_host_key`
to `false` (or `AAB_SFTP_INSECURE_IGNORE_HOST_KEY=false`) and the service
refuses to start until `known_hosts_path` names a file.

### Spend with an unpriced model

`agent.max_cost_per_day` can only count what it can price. A direct Anthropic
model whose id has no price in the build counts as $0, so the cap never trips.
The service says so in a warning at every start. OpenRouter reports its own
cost, and Ollama costs nothing. The day's spend is kept across restarts in
`agent.daily_spend_path`; the hourly quotas are not, and start again.
