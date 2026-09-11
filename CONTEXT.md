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

- **Question**: text plus asker, force hint and tick, held in a bounded ring in
  the companion's `storage` until the service polls it.
- **Round**: one model turn in the agent loop. A round may call several tools
  in parallel. A question takes as many rounds as it needs, under a cap.
- **Answer**: an artifact returned for a question. Printed to chat for the
  question's audience, and raised as the `on_answer` event for any mod that
  subscribes.
- **Artifact**: a typed, small answer shape (summary, comparison, list, table,
  notice) that the model fills and the companion renders. The model never
  formats text; the companion does.

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
- **History**: the service's SQLite store fed by tailing the event file. The
  service keeps the whole history of a save; the companion keeps none.
- **Session**: the short shared transcript of questions and rendered answers
  that follow-ups see. Keyed by scope key plus an optional `#name`; ends by
  idle time, by a cap, or on `new`. Never holds a tool result.
- **Scope**: who may hear an answer. Global by default; a scope provider may
  make a question private to an audience. Fixed on the question row when the
  question is created.
- **Scope provider**: any mod exposing `chat_scope_v1(player_index, text)` on
  a remote interface. Found by scan like a tool provider, never stored.
- **Audience**: the force or player list a private answer prints to.
- **Label**: what players call a force, from any mod exposing
  `force_labels_v1`. Swapped at the edges: labels become force names in the
  question before the model reads it, force names become labels when the
  companion renders. The model only ever sees force names.
- **Tag**: the channel badge a scope provider hands back, printed verbatim
  after the companion's name on the first answer line.
- **Flow statistics**: the engine's own production history, read live. Never
  copied into history.

## Invariants

1. The companion never names another mod. Multi-force is generic; there is no
   team concept in this repository.
2. The rpc command never writes `storage`, except the `answer` operation. A lost
   RCON reply costs nothing; the service re-polls the same cursor.
3. Nothing derived from `remote.interfaces` is ever stored. The catalog is
   rebuilt from scratch on every call and sorted so its JSON is byte-stable.
4. Oversized tool results are refused with an error, never truncated.
5. The protocol, the remote interface `ai-agent-bridge-v1` and the probe name
   `agent_tools_v1` are frozen. Additive changes are safe. Breaking changes
   ship under a new name beside the old one.
