# AI Agent Bridge (AAB): domain language

The words below mean exactly one thing in this repository. Code, docs, commit
messages and the portal page use them the same way.

## The two halves

- **Companion**: the Factorio mod in `companion-mod/`. Lua, control stage, runs
  on every peer. It knows nothing about any other mod and nothing about which
  service is driving it.
- **Service**: the Go binary in `service/`, one process per Factorio server,
  run by the operator. It is the only party that polls. It holds the API key,
  runs the agent loop and keeps history.
- **Protocol**: the frozen contract between the two halves, `aab-rpc-v1`: one
  console command carrying one JSON object in and one JSON object out. Any
  client that speaks it can drive the companion; the service is the reference
  client.

## People

- **Player**: anyone in the game who asks a question.
- **Operator**: whoever runs the service. Brings the API key, picks the model,
  sees the cost.
- **Asker**: the player or mod a question came from. Carries a player index
  when it is a player, and a force hint either way.

## Questions and answers

- **Question**: text plus asker, force hint, tick and personality, held in a
  bounded ring in the companion's `storage` until the service polls it. Its text
  is kept to 400 bytes. It carries the scope it was asked in, fixed when it is
  created, and the `personality` setting as it stood then.
- **Round**: one model turn in the agent loop. A round may call several tools
  in parallel. A question takes as many rounds as it needs, under a cap.
- **Answer**: an artifact returned for a question. Printed to chat for the
  question's audience, and raised as the `on_answer` event for any mod that
  subscribes.
- **Artifact**: a typed, small answer shape (summary, comparison, list, table,
  notice) that the model fills and the companion renders. The model never
  formats text; the companion does. A line or cell holds 320 characters and the
  whole artifact 12,000 bytes; a line the companion prints is cut at 1,280
  bytes as a safety net. An answer over a cap goes back to the model to
  rewrite, up to twice, and is cut at a word boundary only when no round is
  left. An artifact that would print nothing is refused as `bad_artifact`.
- **Voice**: how the bot sounds, set by the `personality`: the runtime-global
  setting `aab-personality`, or the operator's `agent.personality` when the
  setting is blank. One value for the whole server at any moment; the
  question carries it, so a change applies from the next question. It is `off`,
  a preset the service owns, or one line of free text, always followed by a
  guard sentence so a voice never changes a number, a unit, an item name or a
  table cell. Only an admin changes it in game (`/aab-personality`).

## Tools

- **Tool**: one bounded read of game state, implemented as a Lua function on a
  remote interface, taking one plain table and returning one plain value.
  Every tool takes `force` as a parameter. Any player may ask about any force.
- **Provider**: any mod that exposes tools. The companion is itself a provider
  of the engine tools and is discovered the same way as everyone else.
- **Probe**: the zero-argument function `agent_tools_v1` a provider places on
  any remote interface it owns. It returns the provider's manifest.
- **Manifest**: the table a probe returns: `tools`, keyed by function name on
  that same interface, each with `desc` and optional `params`.
- **Catalog**: the merged, sorted list of every provider's manifest, built by
  scanning `remote.interfaces` on every agent turn and never stored.
- **Reserved parameter**: `force`. The service injects it on every tool
  definition; a provider never declares it.

## History

- **Event file**: `events.jsonl`, appended by the companion on deaths, alerts,
  chat, joins, research and rockets. Never rewritten, truncated once per session.
  The first line of every session is a **session event**, data `{map_id}`,
  written in the same write that truncates the file.
  A chat line records who said it, their force, the message and its scope: the
  `scope` key and the `private` flag. A scope provider decides that when one
  answers for the line. A provider that fails may be the one that knew the line
  was private, so after a failure only a private answer from another provider
  stands, and a global one gives way to the force rule. A provider's private
  flag reads as private unless it is false or absent. Without a provider, a
  plain line reached the speaker's force alone, so it is private to that force
  once two forces have players (online or not), and public while every player
  shares one force. Its key is the force name only on a server with no
  provider installed. Where one is installed, a line the engine's rule decides
  is still private to the speaker's force and prints there, but is stored under
  the global key, which no question reads private rows under: nobody reads it
  back, and a chat-prefix question on it reads public rows only. A server
  console line is public.
- **Map id**: made once per map and kept in the companion's storage
  (`storage.aab.map_id`), as `<seed>-<tick>-<random>`. It is made in `on_init`,
  or at the first event write for a save from before 1.0.6. Every save and load
  of one map carries the same id; the session event and the `status` reply both
  name it.
- **Map epoch**: the run of history rows one map logged. Every read covers the
  current epoch only, the epoch of the newest row, so an earlier map's rows,
  private chat included, never reach a later map's questions. A map id other
  than the epoch's starts a new epoch. So does the same id dropping into the
  map's first five minutes (18,000 ticks), because two maps made from one fixed
  seed share an id; so does a question count below the highest the epoch
  logged. A session line the service never saw is replaced by the `status`
  reply's map id, recorded as a session row of source `status`. With no map id
  at all, the tick decides: a drop into the first five minutes, or of more than
  thirty minutes (108,000 ticks), is a new map. A smaller drop is an autosave
  rollback of the same map.
- **History**: the service's SQLite store fed by tailing the event file. The
  service keeps every map's rows in one file and reads the current map's only;
  the companion keeps none. Every row keeps the scope it was said in and its
  epoch. A private row, a chat line or a question
  said privately, reaches only a question asked privately in that same scope
  key. A question asked in the open reads public rows only, whoever asks it;
  one asked privately in another scope reads its own private rows and the
  public ones. A chat line logged before lines carried a scope is private to
  the force it was logged with, unless the migration finds its map only ever
  named one force and no row there says it was private, which makes it public
  (ingest never does this). An answer row is as private as its question in the
  same epoch; with no `qid`, or no question there, it is unreadable.
- **Session**: the short shared transcript of questions and rendered answers
  that follow-ups see. Keyed by scope key plus an optional `#name`, the two
  held apart so a key that itself holds `#ops` never shares another key's
  `#ops` session, with a
  private scope never sharing a session with a public one, even under the
  same key; ends by idle time, by a cap, or on `new`. Never holds a tool
  result.
- **Scope**: who may hear an answer. Global by default; a scope provider may
  make a question private to an audience. Fixed on the question row when the
  question is created. A question asked with the chat prefix takes its chat
  line's scope; with no provider answering and two or more forces with players
  that is private, key = the speaker's force name. `/ask` and remote asks stay
  global unless a provider says otherwise.
- **Scope provider**: any mod exposing `chat_scope_v1(player_index, text)` on
  a remote interface. Found by scan like a tool provider, never stored. It is
  asked about every question and every logged chat line, on every peer. Nil, or
  an error, means "do not know". Its key is opaque and compared whole, byte for
  byte, at most 64 bytes; a longer key is refused, never cut, and its answer is
  private under the global key. A private key is a read key for the whole life
  of the history, so it must name one audience and never a later one: a provider
  that reuses names adds a generation to the key (an incarnation), such as
  `north@51840`.
- **Audience**: the force or player list a private answer prints to. In asker
  mode (the `aab-answer-audience` setting), a global question whose player
  asker has left prints to nobody, with a log line; it is still marked answered. A private question always prints to its
  own audience whatever the setting says.
- **Label**: what players call a force, from any mod exposing
  `force_labels_v1`. Swapped at the edges: labels become force names in the
  question before the model reads it, force names become labels when the
  companion renders. The model only ever sees force names.
- **Tag**: the channel badge a scope provider hands back, printed verbatim, up
  to 200 bytes, after the companion's name on the first answer line. A longer
  tag is cut without tearing a rich-text tag inside it.
- **Flow statistics**: the engine's own production history, read live. Never
  copied into history.

## Invariants

1. The companion never names another mod. Multi-force is generic; there is no
   team concept in this repository.
2. The rpc command never writes `storage`, except the `answer` operation and
   the `status` operation, which makes the map id once on a save that has none
   yet. Every peer runs the command at the same tick and makes the same id, and
   a lost reply costs nothing because the id is kept. A call that gets no reply,
   or a reply over a new RCON connection, is an outage. A restart is the only way
   a headless server loads a save, so on the next reply the service starts again
   before answering anything: cursor back to the first question, held answers
   dropped, every session ended. The game still loses nothing, because only
   `answer` writes storage.
3. Nothing derived from `remote.interfaces` is ever stored. The catalog is
   rebuilt from scratch on every call and sorted so its JSON is byte-stable.
4. Oversized tool results are refused with an error, never truncated.
5. The rpc command runs for RCON and the server console only; a player's
   console never reaches an op. Asking is rate limited at the companion
   before anything else happens, and a refusal is private to the asker.
6. The protocol, the remote interface `ai-agent-bridge-v1` and the probe name
   `agent_tools_v1` are frozen. Additive changes are safe. Breaking changes
   ship under a new name beside the old one.
7. A private history row reaches the model only for a question asked
   privately in the same scope, and only from the current map epoch. The store
   applies this on every read, never the prompt, and a read that carries no
   scope sees public rows only.
