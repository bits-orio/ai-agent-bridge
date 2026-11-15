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
  bounded ring of 64 in the companion's `storage` until the service polls it.
  Its text is kept to 400 bytes. It carries the scope it was asked in, fixed
  when it is created, and the `personality` setting as it stood then. A full
  ring evicts its oldest answered question first. Only when all 64 are
  unanswered does the oldest of them go, and the companion writes a
  `question_dropped` event for it. A global question whose answer prints to its
  asker alone (asker mode, see Audience) carries `to_asker: true`, fixed when it
  is created, on its event line and its poll row.
- **Round**: one model turn in the agent loop. A round may call several tools
  in parallel. A question takes as many rounds as it needs, under a cap.
- **Answer**: an artifact returned for a question. Printed to chat for the
  question's audience, and raised as the `on_answer` event for any mod that
  subscribes.
- **Artifact**: a typed, small answer shape (summary, comparison, list, table,
  notice, art) that the model fills and the companion renders. The model never
  formats text; the companion does. A line or cell holds 320 characters and the
  whole artifact 12,000 bytes on the service's side. The companion enforces its
  own ceiling on the whole artifact, 16,000 encoded bytes, above the service's
  so nothing the service sends is refused; a line it prints is cut at 1,280
  bytes as a safety net. An answer over a cap goes back to the model to
  rewrite, up to twice, and is cut at a word boundary only when no round is
  left. An artifact that is malformed, over the companion's ceiling or would
  print nothing is refused as `bad_artifact`; a failure to print it is never
  reported as one.
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
  Every game tool takes `force` as a parameter. Any player may ask about any
  force. A tool that reads history takes `force` as an optional filter, and
  leaving it out covers every force.
- **Provider**: any mod that exposes tools. The companion is itself a provider
  of the engine tools and is discovered the same way as everyone else.
- **Probe**: the zero-argument function `agent_tools_v1` a provider places on
  any remote interface it owns. It returns the provider's manifest.
- **Manifest**: the table a probe returns: `tools`, keyed by function name on
  that same interface, each with `desc` and optional `params`.
- **Catalog**: the merged, sorted list of every provider's manifest, built by
  scanning `remote.interfaces` on every agent turn and never stored. The
  service bounds what a manifest can cost: a parameter name outside
  `[a-zA-Z0-9_.-]{1,64}` is dropped, a tool description is cut at 600 bytes, a
  parameter description at 300, and a provider's tools together keep to 32 KB.
- **Reserved parameter**: `force`. A provider never declares it. The service
  fills it with the asker's force on a tool that requires it, a game tool, and
  leaves it alone on a tool that offers it only as an optional filter, a
  history tool.
- **Tool result**: what a tool returns. The companion fits each list reply
  within 3,800 bytes and pages it: a cut reply names `next_offset`, and the
  same call with `offset` reads the next rows. The service cuts a result over
  its own limit, `agent.max_tool_result_bytes`, and the cut says so.

## History

- **Event file**: `events.jsonl`, appended by the companion on deaths, alerts,
  chat, joins, research and rockets. Never rewritten, truncated once per session.
  The first line of every session is a **session event**, data `{map_id}`,
  written in the same write that truncates the file. A `question` line carries
  `to_asker` when it is true. A `question_dropped` line, data `{qid}`, says a
  full ring lost an unanswered question. The service reads the file from its
  current end, so a restart replays nothing, and from the top only when the
  file shrinks or its first line changes, or for a file that does not exist
  yet.
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
  rollback of the same map. Two guards protect an epoch a `status` reply
  started. **Quarantine**: lines that join it before its own session line
  arrives, or before the tail has caught up to the status, may belong to the
  map before it, so they are stored unreadable and move nothing. **Hold**: a
  session line in the file that names a different map than the status named,
  while no session line has yet named the status's map, may be stale or a real
  switch; the epoch it starts is held, its rows unreadable, until a status
  names that same map. A status that names any other map leaves them
  unreadable for good.
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
  (ingest never does this). A question with `to_asker` is private to its asker
  under the scope key `asker:<player name>`, whatever scope the line states:
  only that player's later questions read it, and the session it opens is
  theirs alone. With no player name it is unreadable. An answer row is as private as its question in the
  same epoch; with no `qid`, or no question there, it is unreadable.
- **Session**: the short shared transcript of questions and rendered answers
  that follow-ups see. Keyed by scope key plus an optional `#name`, the two
  held apart so a key that itself holds `#ops` never shares another key's
  `#ops` session, with a
  private scope never sharing a session with a public one, even under the
  same key; ends by idle time, by a cap, or on `new`. Never holds a tool
  result.
- **Free command**: a line a player types after `/ask` that the service
  answers without a model round: `help`, `top <metric> [item]` and
  `card [force]`. It costs no money; `top` and `card` still count against the
  hourly question quotas, `help` does not. It must be the whole line, or the
  line is a question.
- **Toast**: one unprompted line the service prints for the whole server when
  a force reaches a milestone, with no question, no asker and no session. It
  is made only from live public history rows of the current map epoch, sent
  through the `say` operation, and silenced by the `aab-allow-say` setting.
- **Scope**: who may hear an answer. Global by default; a scope provider may
  make a question private to an audience. Fixed on the question row when the
  question is created. A question asked with the chat prefix takes its chat
  line's scope; with no provider answering and two or more forces with players
  that is private, key = the speaker's force name. `/ask` and remote asks stay
  global unless a provider says otherwise.
- **Scope provider**: any mod exposing `chat_scope_v1(player_index, text)` on
  a remote interface. Found by scan like a tool provider, never stored. It is
  asked about every question and every logged chat line, on every peer. Nil, or
  an error, means "do not know". A scope a mod hands to a remote `ask` is
  cleaned the same way a provider's answer is: an over-long key is refused, a
  tag is fitted, an audience is checked. Its key is opaque and compared whole, byte for
  byte, at most 64 bytes; a longer key is refused, never cut, and its answer is
  private under the global key. A private key is a read key for the whole life
  of the history, so it must name one audience and never a later one: a provider
  that reuses names adds a generation to the key (an incarnation), such as
  `north@51840`.
- **Audience**: the force or player list a private answer prints to. A force
  audience is kept with the force's index as well as its name: a force that
  takes the name later hears nothing, and the asker alone does. In asker mode
  (the `aab-answer-audience` setting), a global question whose player asker has
  left prints to nobody, with a log line; it is still marked answered. A
  private question always prints to its own audience whatever the setting says.
- **Label**: what players call a force, from any mod exposing
  `force_labels_v1`. Swapped at the edges: labels become force names in the
  question before the model reads it, force names become labels when the
  companion renders. The model only ever sees force names.
- **Home force**: the force a player's question is about, from any mod exposing
  `home_force_v1(player_index)`; the player's own force when none names one.
  A member spectating from another force still asks about their own. The
  question carries the force they sit on as its viewing force. It never
  changes who hears the answer.
- **Watching force**: the force whose base a player is looking at, from any mod
  exposing `watching_force_v1(player_index)`. Carried on the question when it is
  not the home force; "here" in such a question is that force's base.
- **Tag**: the channel badge a scope provider hands back, printed verbatim, up
  to 200 bytes, after the companion's name on the first answer line. A longer
  tag is cut without tearing a rich-text tag inside it.
- **Flow statistics**: the engine's own production history, read live. Never
  copied into history.

## Invariants

1. The companion never names another mod. Multi-force is generic; there is no
   team concept in this repository.
2. The rpc command never writes `storage`, except the `answer` operation (it
   marks the question answered, and marks as taught each player it hints; see
   `aab-ask-hints`), the `status` operation, which makes the map id once on a
   save that has none yet, and the diagnostic `write` operation, which answers
   `bad_op` unless the startup setting `aab-selftest-ops` is on. Every peer runs the command at the same tick and makes the same id, and
   a lost reply costs nothing because the id is kept. A call that gets no reply,
   or a reply over a new RCON connection, is an outage. A restart is the only way
   a headless server loads a save, so on the next reply the service starts again
   before answering anything: cursor back to the first question, held answers
   dropped, every session ended. The game still loses nothing, because only
   `answer` writes storage.
   The `say` operation, which prints one unprompted line, writes no storage
   either: it only prints, logs and raises `on_say`.
3. Nothing derived from `remote.interfaces` is ever stored. The catalog is
   rebuilt from scratch on every call and sorted so its JSON is byte-stable.
4. A tool result is never cut by the companion without saying so. The
   companion fits its own lists to 3,800 bytes, pages them with
   `next_offset`, and refuses any reply over the command's byte cap whole as
   `too_large`. The service cuts a result over its own limit and says so in the
   result.
5. The rpc command runs for RCON and the server console only; a player's
   console never reaches an op. Asking is rate limited at the companion
   before anything else happens, and a refusal is private to the asker. A
   player has a cooldown that leaving and rejoining does not reset, and the
   server has a per-minute limit. A remote ask has a cap of its own, 60 a game
   minute for the whole server, and returns nil over it.
6. The protocol, the remote interface `ai-agent-bridge-v1` and the probe name
   `agent_tools_v1` are frozen. Additive changes are safe. Breaking changes
   ship under a new name beside the old one.
7. A private history row reaches the model only for a question asked
   privately in the same scope, and only from the current map epoch. The store
   applies this on every read, never the prompt, and a read that carries no
   scope sees public rows only.
