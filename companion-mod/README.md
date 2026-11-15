# AI Agent Bridge, the companion mod

Ask your running game a question in chat and read the answer in chat, from an
AI agent that looks up only what the question needs, the moment it is asked.
Anyone can follow up. Any player can ask about any force. Other mods add their
own lookups without this mod knowing they exist.

**This mod is not an AI.** It is the game side of a protocol. It holds the
questions players ask, publishes the lookups an agent may run, renders the
answers an agent sends back, and writes an event log. The thinking happens in
a separate program, an *agent*, that talks to the mod over RCON. The
repository ships one such agent, the [AI Agent Bridge service](https://github.com/bits-orio/ai-agent-bridge),
but the mod is not tied to it: anything that speaks the protocol on this page
can drive it, in any language, with any model, and the mod cannot tell the
difference.

Contents:

1. [Setup](#setup)
2. [Asking](#asking)
3. [Settings](#settings)
4. [For agent authors: the protocol](#for-agent-authors-the-protocol)
5. [For mod authors: the five seams](#for-mod-authors-the-five-seams)
6. [Tools this mod provides](#tools-this-mod-provides)
7. [The events file](#the-events-file)
8. [Safety](#safety)

## Setup

1. **Install the mod** on the server and on every client, from the in-game
   mod browser or the [mod portal](https://mods.factorio.com/mod/ai-agent-bridge).
   It needs nothing else and changes nothing about the game on its own.
2. **Turn on RCON** on the server. Every agent talks to the mod over RCON, so
   the server needs a port and a password: `--rcon-port 27015
   --rcon-password <yours>` on the command line, or the RCON fields most
   hosting panels show in their startup settings.
3. **Run an agent.** The bundled service needs two things from you, where the
   mod's event log is (a local path, or an SFTP path on a remote host) and the
   RCON address and password, plus a model key. Its own page has the steps:
   [service/README.md](https://github.com/bits-orio/ai-agent-bridge/blob/main/service/README.md).
   Or write your own; the protocol is below.

## Asking

Type `/ask` and a question. The mod echoes the question to everyone who will
see the answer, since a command is not a chat line, and the answer arrives a
few seconds later. The echo names the command, `Bob asked with /ask: ...`, so
a player who only watches learns how to ask:

```
/ask what is my iron plate rate
/ask where am I making repair packs
/ask how is Team Ace doing on science
```

Follow-ups pile onto a shared **session**: whoever asks next in the same
scope continues the same conversation, until three minutes pass with no
question. `/ask new ...` starts over, `/ask #iron ...` uses a named session
others can join, and `/ask sessions` lists what is open.

Answers print to the whole server by default, with the same channel tag a
player's own line carries when a chat privacy mod is installed. Players in
a private chat channel get their answers privately, in a session nobody
else sees.

A question asked with the chat prefix goes where its chat line went. Once a
second force has players, a plain chat line reaches only your own force,
so its question and its answer stay inside your force too, unless a chat
privacy mod says otherwise. `/ask` is not a chat line: with no such mod its
question is echoed and answered the way `aab-answer-audience` says.

Items, fluids, machines and technologies show as icons, positions as
clickable map pings, and forces by the name players use for them when a mod
supplies it.

`/aab-ask` always works and does the same. `/ask` works too, unless another
mod already owns it or you turn off the startup setting
`aab-claim-ask-command`; the `status` reply says which name is live. A chat
prefix can ask as well, see the settings.

A player who asks again soon after is told to wait, and leaving and
rejoining does not reset that wait. The echo shows the text the mod kept: at
most 400 bytes, with control characters replaced.

`/aab-personality` shows how the bot sounds. An admin, the server console or
RCON can change it: `/aab-personality a cheerful pirate`, `off`, or `reset`
to hand it back to the operator's config. Anyone else is told only an admin
can.

### Things you can type

Three commands skip the model. They make no model round and cost no money.
`top` and `card` count against the hourly question limits; `help` does not.
`top` and `card` need a mod that has the `sweep` and `force_card` tools; on an
older one you are told to ask it as a question instead.

- `/ask help` (also `/ask ?` and `/ask commands`) lists what you can type. Only
  you see it.
- `/ask top <metric> [item]` ranks something across every force, for example
  `/ask top rockets` or `/ask top item_made iron-plate`. A metric is one of
  the `sweep` metrics.
- `/ask card [force]` shows one force at a glance: players, research, rockets,
  robots, enemies killed and its top items. With no name it is your own force.

A line must be exactly one of these forms. `/ask top 5 reasons my base is
slow` is still a question.

The service also prints a line of its own now and then, with no question
behind it, when a force reaches a milestone such as its first rocket. The
setting `aab-allow-say` turns that off.

## Settings

The first two are startup settings, read when the game loads. The rest are
runtime-global: change them under Settings, Mod settings, while the server
runs.

| setting | default | what it does |
|---|---|---|
| `aab-claim-ask-command` | on | Startup. On, the mod registers `/ask` as well as `/aab-ask`. Turn it off when another mod's `/ask` has to stay; players then use `/aab-ask`. |
| `aab-selftest-ops` | off | Startup. For test rigs only. Turns on the diagnostic ops `ping`, `big`, `write` and `pcall_test`; off, they answer `bad_op`. Leave it off on a real server. |
| `aab-events-enabled` | on | Append deaths, joins, leaves, chat, questions, answers, research and rocket launches to `script-output/ai-agent-bridge/events.jsonl`. Off stops the file growing. |
| `aab-chat-prefix` | blank (off) | Blank means only `/ask` asks. Set it and any chat line starting with those exact characters becomes a question, the rest of the line being the text. Matched literally, spaces included; pick something no sentence starts with, `?` or `@ai `. The answer goes to whoever saw the line, see [Asking](#asking). |
| `aab-answer-audience` | server | `server` prints every global answer to everyone, so anyone can follow up. `asker` prints only to the player who asked, and to nobody when they have left before the answer arrives; the log says so. A question a mod asked with no player still prints to everyone. A private question always prints to its own audience, whatever this says: one a chat privacy mod marked private, or one asked in a chat line that reached a single force. |
| `aab-ask-hints` | echo | How players learn to ask. `echo`: the line others see names the command, `Bob asked with /ask: ...`. `all`: also one private line, once per player, `You can ask too: /ask <your question>`, sent when the player joins, sees a question echoed or hears an answer. `off`: the plain `Bob asked: ...` line. |
| `aab-allow-say` | on | Runtime, global. Lets the service print a line to everyone that answers no question, such as a milestone it noticed. Off keeps the bot silent until someone asks; the `say` op then answers `bad_op`. Answers to questions are not affected. |
| `aab-ask-cooldown-seconds` | 5 | A player who asks again sooner is told to wait, privately: no echo, no question, no cost. 0 turns it off. |
| `aab-asks-per-minute` | 30 | When the whole server has asked this many times in a minute, further asks are refused privately until the minute turns. 0 turns it off. |
| `aab-personality` | blank | How the bot sounds. Blank uses the operator's config, `off` turns the voice off, `factorio` picks the built-in one, anything else describes it, `a cheerful pirate` for example, at most 500 bytes. Each question carries the value it was asked with. Admins can also set it with `/aab-personality`. |

## For agent authors: the protocol

Everything an agent needs is one console command, `aab-rpc-v1`, sent over
RCON, plus the events file for history. The bundled Go service is one client
of it; this section is enough to write another.

### What an agent does

1. **Poll.** Every second or so, send `poll` and take the unanswered
   questions it returns, oldest first.
2. **Read the catalog.** `providers`, then one `manifest` per provider, gives
   every lookup the server offers: this mod's own tools and any other mod's.
   Rebuild it now and then; nothing about it is stored on either side.
3. **Answer.** Run a model with those tools. Each tool call is one `call`;
   the reply is plain JSON the model reads. When the model has an answer,
   send it as an **artifact**, one of five small shapes, with `answer`.
4. **Remember**, if you like. The events file carries deaths, research and
   the rest, so "how much iron since I last died" is answerable. Each
   session opens with the map's id, so a new map can start a new history,
   and every chat line says who heard it.

The mod does the rendering, the audience, the icons and the safety. An
agent never prints to chat, never chooses who reads an answer, and never
runs anything that is not in the catalog.

### The command

`/aab-rpc <json>`. One JSON object in, one JSON object out through
`rcon.print`. Every request carries `"v":1`. Every reply is
`{"ok":true,"r":...}` or `{"ok":false,"e":"<code>","m":"<detail>"}`. The
command answers RCON and the server console only; a player typing it into
their own console gets one private line and nothing runs.

| op | request | reply `r` | writes storage |
|---|---|---|---|
| `status` | `{}` | protocol version, mod version, tick, connected player count, pending question count, `last_id`, `ask_command` (the `/ask` command name that is live, left out when neither could be registered), `map_id` | only the map id, once, on a save that has none yet |
| `providers` | `{}` | sorted list of `{iface, v, tools}`, one per provider, `tools` being the sorted tool names | no |
| `manifest` | `{i}` | one provider's manifest verbatim, `{v, tools}` | no |
| `tools` | `{}` | every provider's manifest at once, for small servers and tests | no |
| `call` | `{i, f, a?}` | the tool's return value, plain data | no |
| `labels` | `{}` | sorted `{name, label}` rows: what players call each force, from any labels provider | no |
| `poll` | `{after?, limit?}` | unanswered questions with id above `after`, oldest first, each `{id, text, player_index, player_name, force, viewing_force, watching_force, tick, scope, private, to_asker, surface, physical_surface, personality}` | no |
| `answer` | `{qid, artifact}` | `true` | yes: validates, renders, marks answered, and marks each player it hints as taught; raises `on_answer` |
| `answers` | `{after?, limit?}` | answered questions with id above `after`, oldest first, each `{id, shape, lines, player_index}` | no |
| `say` | `{artifact}` | `true` | no: validates and prints one unprompted line to everyone, writes a `say` event, raises `on_say`; `bad_op` when `aab-allow-say` is off; `bad_artifact` for a bad artifact or one with `to_asker` |

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_artifact`, `bad_result`, `too_large`, `no_question`.

Replies are capped: 8,000 bytes for `call`, 32,768 for `tools` and
`manifest`, 65,536 for the rest. A reply over its cap is refused whole as
`too_large`, never cut. That is why the catalog is read as `providers` then
one `manifest` each: a mod with forty wordy tools costs itself its tools and
nobody else theirs. The companion's own tools keep each list reply under
3,800 bytes and page it: a reply that was cut says `next_offset`, and the
same call with `offset` set to that value returns the next rows.

### Questions

`poll` returns questions nobody has answered yet, so `after` of 0 means
everything still waiting, and an agent that restarts re-answers nothing.
`last_id` on `status` is the highest id issued, the cursor for "from here
on". `after` defaults to 0 and `limit` to 16, at most 64; page with the last
id seen. Question text is capped at 400 bytes.

A row carries who asked (`player_name`, kept after they leave), their force,
the surface they were looking at and, in remote view, the one their
character stands on, and the chat **scope**: `scope` is the session pool the
question belongs to and `private` says its answer stays inside an audience
the mod keeps to itself. An agent uses `scope` to keep conversations apart
and never needs the audience. A question asked with the chat prefix carries
its chat line's scope: with no chat privacy mod installed, once a second
force has players, `scope` is the asker's force and `private` is true.
Where such a mod is installed but leaves the line to the game's rule,
`scope` is `global` and `private` is true. Keep each asker's private
questions under `global` apart from everyone else's: nothing says who else
heard them.

`to_asker` is present, and true, on a global question whose answer prints to
its asker alone, because `aab-answer-audience` was `asker` when it was asked.
An agent keeps such a question, and the session it opens, to that one asker.

`personality` is the `aab-personality` setting as it stood when the question
was asked, trimmed and at most 500 bytes, and is left out when blank. The
mod never reads it; the agent decides what it means. A row with 400 bytes of
text and 500 of personality is about 1,070 bytes, so a full page of 64 such
rows passes the 65,536-byte cap and answers `too_large`: page smaller then.

### Tool calls

`call` takes the interface name `i`, the function `f` and one JSON object
`a`. Every game tool takes `force`: the agent fills it with the asker's force
unless the question named another. `a` may be left out for a tool that
needs nothing. A tool that fails answers `provider_error` with the first
line of its message, which is written for a model to read and try again.

Tools are described in a manifest with one grammar per parameter,
`"<type>[!] <description>"`, the type one of `string`, `integer`, `number`,
`boolean`, a trailing `!` for required. Turn that into whatever schema your
model wants.

### Artifacts

The model fills one of five shapes; the mod renders it. Every shape but
`notice` may carry a `title`, which becomes the first line.

| shape | fields | caps |
|---|---|---|
| `summary` | `lines: string[]` | 3 lines |
| `notice` | `text`, `level?` (`warning` or `confirmation`) | 1 line |
| `list` | `items: string[]` | 10 items |
| `table` | `columns: string[]`, `rows: string[][]` | 5 columns, 8 rows |
| `comparison` | `columns: [a, b]`, `rows: {label, a, b}[]` | 5 rows |
| `art` | `lines: string[]`, printed as written in a monospace font, spaces kept | 12 lines of 60 characters |

Two optional fields on any shape: `session: {name, fresh}` renders a
`(new session)` marker when `fresh` is true, with `#name` added when `name`
matches `[a-z0-9_-]{1,16}` and left off otherwise, and `to_asker: true` prints
the answer to the asker alone, for a refusal that is nobody else's business.

The caps are the bundled service's own, so an answer it sends is never cut
again here. The artifact as a whole may be at most 16,000 bytes encoded, above
the service's own 12,000, and a larger one is `bad_artifact`. A cell is 320 characters there; here every string has its
control characters replaced, so a player name echoed into an answer cannot
forge a line, and a line past 1,280 bytes, 320 characters at four bytes
each, is cut on a UTF-8 boundary and ends with an ellipsis. Factorio rich
text passes through: `[img=item.iron-plate]` for an icon,
`[item=iron-plate]` for a clickable one, `[gps=x,y,surface]` for a map ping,
`[color=red]...[/color]`. A bare hyphenated prototype name the model writes
anyway, `iron-ore`, is turned into its icon at render time, and a force name
is turned into the label players use when a mod supplies one.

A tag the game cannot draw would print raw, brackets and all, so each one is
repaired, the first of these that finds something winning: a `planet.` path
is read as `space-location.`; the same name in another class
(`[img=item.crude-oil]` is `[img=fluid.crude-oil]`); the name respelled,
lowercase with hyphens and a plural dropped (`iron-plates` is `iron-plate`,
`batteries` is `battery`); the name players give a science pack, circuit
or belt from the base game or Space Age, colour and family whole
(`[img=item.red-transport-belt]` shows the `fast-transport-belt` icon, the
belt players call red); and last the plain words, `[img=entity.biter]`
printing as "biter". A name the game really has always wins over a
nickname. A clickable tag goes the same way and ends at its plain name.

The first chat line is laid out like a player's own: the mod's name, the
channel tag, the session marker, a colon, the answer. A table prints in the
mod's monospace font: the column names, a rule under them, then one row per
line, ` | ` between cells, each column padded to its widest cell and
numbers right-aligned.

`answer` validates before it touches anything: an unknown shape, a
mistyped field, an artifact over the size cap, or an artifact that would print
nothing, its title and every line, item or cell blank, is `bad_artifact` and
the question stays pending. Only a problem with the artifact itself comes back
as `bad_artifact`: a failure while printing is logged and never blamed on the
client. Rendering happens before the question is marked answered, so a
question is never marked done with nothing shown. Answering an already
answered question succeeds and changes nothing, so a lost reply can be sent
again. `on_answer` is raised under its own guard, so a subscriber that fails
cannot turn a delivered answer into an error.

`say` is an answer without a question. It takes the same artifact, checks it
and renders it the same way, and prints it to everyone with the mod's name in
front and no channel tag or session marker. There is no asker, so `to_asker`
is refused and the line is always global. It writes nothing to `storage` and
never touches the question ring. `on_say` is raised under its own guard too.

### Diagnostic ops

Kept from the transport checks; not part of the frozen surface. They are
for test rigs: unless the startup setting `aab-selftest-ops` is on, each one
answers `bad_op`, as an unknown op does.

| op | request | reply `r` |
|---|---|---|
| `ping` | `{}` | `{player_index, tick, has_player}` as the command received them |
| `big` | `{kb}` | a JSON string of about `kb` kilobytes, `kb` clamped to 4096 |
| `write` | `{}` | increments a counter in storage and raises `on_answer` with a test payload |
| `pcall_test` | `{}` | calls a self-test provider that always errors, reports whether `pcall` caught it |

## For mod authors: the five seams

This mod knows nothing about any other mod. Five frozen, additive-only seams
let a mod add tools, ask questions, receive answers, say who may hear an
answer, and say what players call a force. Each is a function name the mod
looks for on every remote interface, or one function on its own interface;
nothing is registered and nothing is stored.

### 1. Tools by probe, `agent_tools_v1`

Add a zero-argument `agent_tools_v1` to any remote interface you own. The
mod scans `remote.interfaces` for it on every catalog read; a removed mod
vanishes from the catalog on the next scan.

```lua
remote.add_interface("my-mod-tools", {
  agent_tools_v1 = function()
    return {
      v = 1,
      tools = {
        team_list = {
          desc = "Every claimed team: force name, member count.",
        },
        team_standings = {
          desc   = "Race leaderboard for one milestone.",
          params = {
            milestone = "string! a milestone key",
            rank_by   = "string 'elapsed' (default) or 'online_elapsed'",
          },
          -- Optional: lets the agent's sweep tool rank this across every
          -- team in one call. Say which reply field holds the rows, which
          -- row field names the team and which holds the number.
          sweep = {
            axes = { "force" }, rows = "teams", name = "force", value = "elapsed",
            unit = "seconds", subject_param = "milestone",
          },
        },
      },
    }
  end,
  team_list      = function(args) --[[ args.force is injected; return plain data ]] end,
  team_standings = function(args) --[[ ditto ]] end,
})
```

- `force` is reserved: the agent injects it into every call; declare it
  nowhere.
- `sweep` is optional. A tool that carries one becomes a metric of the
  agent's `sweep` tool, so "which team has the most X" is one call across
  every team; the full contract is in the repo under
  docs/design/phase5-sweep.md, "Provider-declared metrics". Nothing in the
  agent names your mod: any multi-team mod can declare it.
- Return plain data only; a Lua object leaks through `remote.call` intact
  and a value that cannot be JSON breaks the caller.
- Your tool is always handed a table, so `args.force` is safe to read.
- Raise a message a model should read with `error(message, 0)`; level 0
  keeps your file and line out of it.
- Keep every tool bounded; a reply over the cap is refused, never cut.
- A manifest entry whose `desc` is not a string, or whose `params` is not a
  map of strings, is dropped with one line in the log, and cannot be called
  either. A probe that errors costs you every tool but nobody else theirs.

### 2. Questions by interface, `ai-agent-bridge-v1`

Frozen, owned by this mod. Guard every call the way you would guard any
optional dependency:

```lua
if remote.interfaces["ai-agent-bridge-v1"] then
  local qid = remote.call("ai-agent-bridge-v1", "ask", {
    text = "How's the north force doing on science?",
    force = "north",       -- optional force hint
    player_index = nil,    -- optional, if this came from a player
    scope = nil,           -- optional: who may hear the answer, see seam 4
  })
end
```

| function | args | returns |
|---|---|---|
| `ask` | `{ text, player_index?, force?, viewing_force?, watching_force?, scope? }` | the new question's id, or `nil` if `text` was missing or the mod is over its cap |
| `get_event_id` | `"on_answer"` or `"on_say"` | this session's event id for that event, or `nil` |

`text` must be a non-empty string; `force`, `viewing_force` and `watching_force`
are force *names*; `player_index` a number. A field of the wrong type is dropped
rather than stored, because one unencodable question would break every later
poll. A force name is clipped to 64 bytes and dropped unless it names a real
force. A `scope` you pass is cleaned exactly as a scope provider's answer is
(see seam 4). Nothing here errors. Questions asked this way skip the per-player
cooldown and the per-minute limit on players, since no player is behind them.
They have a cap of their own: 60 a game minute for the whole server, and `ask`
returns `nil` over it. They still count against the agent's own caps.

### 3. Answers by event, `on_answer`

Raised once per answered question. `e.artifact` is what the agent sent;
`e.shape` and `e.lines` are what this mod rendered, the same lines the
`answers` op returns. `e.scope` and `e.private` are the question's: a
subscriber that relays answers anywhere else keeps a private one inside its
channel, and one with `e.artifact.to_asker` to the asker alone. A subscriber
that errors is logged and changes nothing for the player. A private
one under `global` has no channel to keep it in, so relay it nowhere. A `generate_event_name()` id is only valid in the
session that made it, so fetch it in `on_init` and
`on_configuration_changed`, cache it in `storage`, and reuse it in `on_load`:

```lua
local function on_answer(e) --[[ e.qid, e.question, e.artifact, e.shape, e.lines, e.scope, e.private ]] end

local function fetch_event_id()
  if remote.interfaces["ai-agent-bridge-v1"] then
    storage.my_mod_answer_event = remote.call("ai-agent-bridge-v1", "get_event_id", "on_answer")
  end
end

local function register_handler()
  if storage.my_mod_answer_event then
    script.on_event(storage.my_mod_answer_event, on_answer)
  end
end

script.on_init(function() fetch_event_id(); register_handler() end)
script.on_configuration_changed(function() fetch_event_id(); register_handler() end)
script.on_load(register_handler)
```

The service can also print a line nobody asked for, a milestone for example,
through the `say` op. It raises a second event, `on_say`, with
`{ artifact, shape, lines }`: what the service sent and what this mod printed.
There is no question, scope or asker, because the line went to everyone. Get
its id with `remote.call("ai-agent-bridge-v1", "get_event_id", "on_say")` and
cache it the same way. A subscriber that errors is logged and changes
nothing. Nothing is raised while `aab-allow-say` is off, since nothing is
printed.

### 4. Chat scope by probe, `chat_scope_v1`

A mod with a chat privacy feature says who heard a line and who may hear an
answer. Add `chat_scope_v1(player_index, text)` to any interface you own.
The mod calls it for every question a player asks and for every chat line
the events file records, on every peer, and stores the result with each:
on the question, and beside the line as its `scope` and `private`.

```lua
chat_scope_v1 = function(player_index, text)
  local player = game.get_player(player_index)
  if not player then return nil end              -- not yours to say
  if my_channel_reaches_everyone(player, text) then
    return { key = "global", private = false, tag = GLOBAL_BADGE }
  end
  if not my_channel_is_private(player) then return nil end
  return {
    key = my_channel_key(player),              -- one audience, never reused
    private = true,                            -- the answer stays inside the audience
    audience = { force = player.force.name },  -- or { players = { 1, 5, 9 } }
    label = "North",                           -- optional, shown by /ask sessions
    tag = PRIVATE_BADGE,                       -- optional rich text, printed verbatim
  }
end
```

A `key` is at most 64 bytes. A longer one is refused, never cut, since two
keys that share their first 64 bytes would become one audience. The answer
that carried it is then read as private under `global`, whatever it said
about `private`, and the log names your interface. A `label` is kept to 64
bytes and a `tag` to 200, each cut on a character boundary. A `tag` cut
never tears a rich-text tag inside it: a torn tag is dropped and an open
colour or font is closed, within the 200.

What you return is read as who heard the text:

- Return `nil` when you do not know, or when your mod leaves the line to the
  game. The game's own rule applies then: a plain chat line reaches the
  speaker's force alone, so once a second force has players the line, and
  a question asked with the chat prefix, is private to that force. An
  `/ask` question is global.
- The game's rule keeps such a line under its force name only on a server
  where no mod provides `chat_scope_v1`. Where one does, a force may have
  passed from one group to another, and history keeps every earlier
  group's private lines under that same name. So there, a line no provider
  answers for is kept private under `global` instead: it still reaches only
  the speaker's force, nobody reads it back, and a question asked with the
  chat prefix on it reads public text alone. Answer with a key of your own
  to keep such lines readable.
- A provider that errors may be the one that knew the line was private. For
  a chat line, and a question asked with the chat prefix, an error leaves
  only a private answer from another mod standing: a global one gives way
  to the game's own rule, kept under `global` as above. For an `/ask`
  question an error counts as `nil`.
- Return `{ key = "global", private = false }` only when the text really
  reached everyone, a channel your mod relays to every force for example.
  A line you call global can be read back by any question anyone asks.
- `private` is read as private unless it is `false` or absent, so a `1` or
  a `"true"` keeps the text private rather than opening it to everyone. A
  private answer with no string `key` is stored under `global`, which no
  question reads private text under: it is read back by nobody.
- A private `key` is a read key. Every private line and question stored
  under it can be read back by every later private question under the same
  key, for as long as the agent keeps history. So a key must name one
  audience and never a later one. If your mod reuses names, a force slot
  handed to new players or a channel name taken again, add a generation:
  `north@51840` for the group that claimed `north` at tick 51840, so the
  next group starts with nothing. Keep the name short enough that the
  generation still fits in the 64 bytes.
- A key is opaque. It is compared whole, byte for byte, and no character
  in it means anything, `#` and `@` included. Equal keys share sessions
  and private history. A private scope never shares a session with a
  public one, even under the same key, and a private question under
  `global` keeps a session to its own asker.

`text` is the line as typed, so a "shout" rule can apply. When several mods
answer, a private answer wins. The result is fixed when the question is
asked, so an answer prints to that audience however the channel has changed
since, and a channel that leaves private mode never sees its private session
continue in the open. A force audience is kept with the force's index as
well as its name: if another force takes that name later, it hears nothing,
and the asker alone sees the answer. Keep the probe cheap and free of side effects: it runs
on every chat line.

### 5. Force labels by probe, `force_labels_v1`

A mod that names forces adds a zero-argument `force_labels_v1` to any
interface it owns, returning `{ ["team-1"] = "Team Ace", ... }`. A label
may carry colour and font tags, the way a team mod colours its teams; this
mod keeps those two when it prints a label and strips every other tag. The
swap happens at the edges: the agent turns a label in a question into the
force name before the model reads it, and this mod turns force names in an
answer back into labels, in their colour. Only force names with a digit,
hyphen or underscore take part, so `player` stays a word. The `labels` op
lists the merged map as plain text.

### 6. Home force by probe, `home_force_v1`

A mod that moves players onto another force for a while, a spectator force
or a lobby, adds `home_force_v1(player_index)` to any interface it owns and
returns the force the player belongs to. A player's question is then about
that force: "how about our team?" asked while spectating means the team, not
the spectator force. The question carries the force they sit on now as
`viewing_force`, and the model is told both. Return nil when you do not know.
A name no force has, or an error, is ignored, and the first interface by name
that names a real force wins. Who hears the answer is still the chat scope's
call; this probe changes only what the question is about.

A mod that lets players watch another force's base adds
`watching_force_v1(player_index)` the same way, returning the force being
watched, nil when nobody's. The question carries it as `watching_force`,
and the model is told that "here" is that force's base, so "are they making
repair packs here?" is about the watched force.

## Tools this mod provides

The mod is a provider like any other, on the interface
`ai-agent-bridge-tools`. `force` is injected into every game tool here; a
tool that reads history, which the service owns, treats it as an optional
filter instead.

| tool | arguments | returns |
|---|---|---|
| `list_forces` | `include_empty`, `names`, `limit`, `offset` | the forces that have ever had a player: name, player count, connected player count, with `total`, `empty` (left out as never-joined slots) and `shown`; `include_empty=true` lists every force; `names=true` adds the name each force was given |
| `list_players` | `connected`, `all`, `limit`, `offset` | that force's players: name, connected, admin, with `known`, `total` and `shown`; `all=true` answers for every force in one call |
| `list_surfaces` | `kind`, `limit`, `offset` | the surfaces: name, index, planet if it has one, how many of that force's players stand on it (`force_players`) and how many of them are connected (`force_players_online`); `kind` is `all`, `planets` or `platforms`, and a platform row adds its owner, location and state |
| `current_research` | `all` | what that force is researching, and its progress through that one technology; `all=true` gives one row per force that has players or research, in one call |
| `research_queue` | `limit` | the running technology and the queue behind it: name, level, research units, progress |
| `research_cost` | `tech`, `level` | the science the queue, or one technology, still needs: units left, packs per unit and in all, minutes at the current rate and the bottleneck pack |
| `tech_status` | `tech`, `all`, `limit`, `offset` | one technology: researched, enabled, `available` (false once researched), level, progress, missing prerequisites, and its cost: `units`, `unit_time`, `ingredients`, or `research_trigger` for one that unlocks by doing something; `all=true` gives one row per force |
| `item_rate` | `surface?`, `item`, `window` | how fast one item is made and used, per minute over the window, summed over every quality; with no `surface`, totalled over every surface with a per-surface breakdown; a rate, never a total |
| `top_items` | `surface?`, `window`, `n` | the n most-produced items, ranked, each summed over every quality; with no `surface`, over every surface |
| `production_since` | `surface?`, `item` or `items`, `since_tick` | how much of one item, or up to the listed items, that force produced and consumed since a tick, 0 for the whole game; with no surface, every surface the force has made it on, with a per-surface breakdown; the answer to "how much" |
| `fluid_rate` | `surface`, `fluid`, `window`, `all` | how fast one fluid is made and used, units per minute; `all=true` ranks every force |
| `logistics_summary` | `surface`, `limit`, `contents` | that force's logistic networks on one surface: robots, idle robots, cells, the eight largest item counts |
| `entity_count` | `surface`, one of `name`, `names` or `type`; `all`, `per_surface`, `offset` | how many entities that force has, counted by the engine, per name and in total; `type` counts every entity of a prototype type such as `furnace`; `all=true` gives one row per force |
| `machine_status` | `surface`, one of `name` or `type`; `near`, `radius`, `limit`, `offset` | why machines are slow or idle: that force's entities of a name or type on a surface, counted by status with example gps tags, optionally around one player |
| `power` | `surface`, `limit` | the electric networks on a surface, largest first: power delivered now, accumulator charge, and the top producers and consumers. Delivered power cannot show a shortage; look for empty accumulators here and `low_power` in `machine_status` |
| `resources` | `surface`, `near`, `resource`, `radius`, `offset` | the ore and oil left around a player, per resource: tiles, total and the richest tile as a gps tag; infinite resources give their average yield |
| `evolution` | `surface` | the evolution factor on one surface and its time, pollution and spawner-kill parts |
| `pollution` | `surface` | total pollution on one surface and which pollutant it uses |
| `rockets` | `limit`, `all`, `offset` | rockets launched by that force and the items it sent up, largest first; `all=true` gives counts for every force |
| `trains` | `surface?`, `all`, `offset` | how many trains that force has and how many are moving or in manual control |
| `kills` | `surface?`, `limit`, `all`, `offset` | what that force has destroyed and lost; `kills` is split into `enemies`, `trees_and_rocks` and `other`, so "enemies killed" is `enemies` |
| `built` | `surface?`, `limit`, `all`, `offset` | how many entities that force has built and mined back, counted as events over the game rather than what stands now |
| `game_time` | force only | tick, ticks played, hours played, connected players on the server and on that force |
| `mods` | `offset` | the active mods and their versions |
| `find_entities` | `surface`, one of `name`, `names`, `type`, `recipe`, `product`; `ghost`, `limit` | where that force's entities are on one surface, each with a ready `[gps=x,y,surface]` tag: by prototype name or several names, entity type, the recipe a crafting machine is set to (an idle furnace by its last one), or the item or fluid that recipe makes; ghosts match by what they will become and say `ghost = true`; an unknown name comes back with up to five close names |
| `locate_player` | `player`, `all`, `nearby`, `offset` | where one player's character is, with a gps tag, whether they are connected, and `viewing` when they look at another surface; `nearby=N` adds the closest built things within N tiles, nearest first, with distance and gps; `all=true` answers for every connected player in one call |
| `recipe_needs` | `items`, one of `per_min` or `machines`; `machine`, `belt`, `stop_at`, `surface` | what it takes to make the targets, worked out from that force's own unlocked recipes: every crafted item per minute with its recipe, machine and machine count, the raw inputs per minute, and belts of each; see below |
| `force_card` | none, `force` only | one summary of one force: `players_total`, `players_online`, `researched`, `current_research` with `research_progress` and `queue`, `rockets`, `robots_total` with `logistic_robots` and `construction_robots`, `enemies_killed` and `top_items` (the five most made, per minute over one hour). A key with no value is left out; `surfaces_cut` or `partial_window` say when a figure is partial. Use it first for "how is this force doing" |
| `sweep` | `metric`, `axis`, `subject`, `window`, `limit` | which force, surface, platform or player has the most of something, in one call; the metrics are `entities`, `rockets`, `research`, `researched`, `players`, `item_made`, `item_used`, `item_rate`, `pollution`, `evolution`, `robots`, `logistic_robots`, `construction_robots`, `networks`, `kills`, `enemies_killed`, `built`, `fluid_rate` and `trains` |

Every list is bounded and sorted before it is cut, and reports `total`
beside `shown`, so an agent can say "12 online of 214 known" instead of
believing it saw everyone. A reply fits 3,800 bytes: rows past that are left
off, `next_offset` names where the next page starts, and a list tool takes
`offset` to read it. `find_entities` asks the engine for at most 2,000
entities in one pass and returns 5 positions by default, 10 at most;
`truncated` says when the pass hit its cap, and a truncated reply shows
what the pass saw, never that something is absent. A `recipe` or `product`
search with no name or type asks for the three crafter types rather than
for everything, so the 2,000 are spent on machines that can match instead
of on belts and poles. Every fraction is rounded and
sent as a short decimal string, `"1.79"`, because the engine's JSON writer
would otherwise print fifty digits of it; whole numbers stay numbers. Every
`surface` argument takes a name or an index and answers `found = false` with
a reason for one this game does not have, as `tech_status`, `entity_count`
and `find_entities` do for a name. A label two forces share is refused as
ambiguous, never resolved to one of them silently.

Rates and totals are summed over every quality, because the engine treats a
bare item name as normal quality alone. `window` is one of the engine's own
precisions: `five_seconds`, `one_minute`, `ten_minutes`, `one_hour`,
`ten_hours`, `fifty_hours`, `two_hundred_fifty_hours`, `one_thousand_hours`.
A rate reply carries `window_ticks` and `map_age_ticks`, and says
`partial_window = true` when the map is younger than the window, so a rate
over ten minutes on a three-minute-old map is not read as a full one.
`production_since` sums flow samples from the smallest window that covers
the elapsed ticks and reports `elapsed_ticks`, `covered_ticks` and
`covers_full_period`, since the engine keeps 300 samples per window.

`recipe_needs` works from what the force has unlocked. With a `surface` it
counts only what can run and be mined or pumped there, and says
`not_makeable_on_surface` in `approx` when the target cannot be made on it.
With neither `per_min` nor `machines` it answers for one machine per item
and says `assumed: machines=1`. A single close name is resolved, with
`resolved_from` showing what was asked. Every reply carries `approx`, a list
of why its figures are rough: `no_modules_or_beacons` (research productivity
only), `no_fuel` when a machine burns fuel, and where they apply `byproducts` not balanced, a `cycle`,
a `cap` reached. A machine or belt the force cannot build yet says
`unlocked = false`, a raw item whose recipe is still locked says
`recipe_locked`, and a raw input the game hands out, mined from a resource or
pumped from a tile, says `raw = "mined"`, with the recipes that could make
it anyway, asteroid crushing or ice melting, in `alternatives`. Unknown
names come back `found = false` with `did_you_mean`, up to five real names
each.

`sweep` takes the metrics of every provider that declares one, beside its
own. `robots` counts logistic and construction robots together, with
`logistic_robots` and `construction_robots` for each alone. `research` is
progress through the current technology only; `researched` counts finished
ones. A refusal names the metric, axis or subject that would have passed. A
reply may carry `better`, `higher` or `lower`, which says which end of the
ranking is the good one.

## The events file

Appended to `script-output/ai-agent-bridge/events.jsonl` while
`aab-events-enabled` is on, truncated once per session. The server writes
it, never a client. One JSON object per line:

```json
{"event":"session","tick":1200,"data":{"map_id":"2894571-0-1804289383"}}
{"event":"player_died","tick":1234,"data":{"player":"Bob","force":"player","cause":"small-biter"}}
{"event":"console_chat","tick":1236,"data":{"player":"Bob","force":"player","message":"our outpost is at 300,40","scope":"player","private":true}}
{"event":"question","tick":1240,"data":{"qid":7,"player":"Bob","force":"player","text":"how much iron since I died","scope":"global","private":false}}
{"event":"answer","tick":1512,"data":{"qid":7,"shape":"summary"}}
```

| `event` | `data` |
|---|---|
| `session` | `map_id` |
| `player_died` | `player`, `force`, `cause` |
| `player_joined`, `player_left` | `player`, `force` |
| `console_chat` | `player`, `force`, `message`, `scope`, `private` |
| `research_finished` | `force`, `tech`, `level` |
| `rocket_launched` | `force`, `surface` |
| `question` | `qid`, `player`, `force`, `text`, `scope`, `private`, `to_asker` (only when true) |
| `answer` | `qid`, `shape` |
| `question_dropped` | `qid` |
| `say` | `shape`, never the text |

`session` is the first line of every session. Its `map_id` is made once
per map, from the map seed, the tick it was made at and a random number,
and kept in the save. Every save and load of one map carries the same id,
so ticks that drop under the same id are an older save of that map, and a
new id is a new map. Two maps made from one seed at the same tick can share
an id; the game offers no other source of chance. The `status` op reports
the same id as its own `map_id`, so an agent knows the map before any event
is written.

A `question` row's `text` is the text the question kept, at most 400 bytes,
the same as its `poll` row. A row with `to_asker` true was shown to its asker
alone: treat it as private to that player, whatever `scope` says.

The question ring holds 64. When it is full, the oldest answered question goes
first. Only when all 64 are still waiting does the oldest of them go, and the
mod writes `question_dropped` with its `qid`, since an answer to it can no
longer land.

`scope` and `private` say who heard a chat line or a question. A row with
`private` true was heard only inside its `scope` key: show its text only to
a question asked privately under that same key, never to one asked in the
open. A private row under `global` was heard by an audience no key names:
show it to nobody. A row with `private` false was heard by everyone. A line typed into
the server console has no `force` and is public. An `answer` is as private
as its question.

A `say` row is one line the service printed to everyone with no question
behind it. It records the shape and never the text, so it is public and
holds nothing to hide.

An agent on the same machine reads the file; one elsewhere reads it over
SFTP. The bundled service does either.

## Safety

What players can do, what an agent can do, what it can cost and the guards
on each are in [SECURITY.md](https://github.com/bits-orio/ai-agent-bridge/blob/main/SECURITY.md).
In short: the rpc command runs for RCON only, asking is rate limited before
anything else happens (a mod's asks have a cap of their own), refusals are
private, every tool is a bounded read, the diagnostic ops are off unless a
startup setting turns them on, and the agent side keeps its own quotas and a
daily budget.

The full design lives in the repository: [CONTEXT.md](https://github.com/bits-orio/ai-agent-bridge/blob/main/CONTEXT.md)
for the words, [PLAN.md](https://github.com/bits-orio/ai-agent-bridge/blob/main/PLAN.md)
for the decisions.

## Licence

MIT, the mod and the whole repository alike: [LICENSE](https://github.com/bits-orio/ai-agent-bridge/blob/main/LICENSE)
ships inside the mod zip, and every Lua file carries the notice in its header.
