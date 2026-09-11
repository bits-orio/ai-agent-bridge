# AI Agent Bridge, Companion Mod

Ask your running game a question in chat and get the answer back in-game from
an AI agent. Any player can ask about any force. Other mods can add their own
tools to the agent without this mod knowing anything about them.

> ### ⚠️ This mod needs a companion program to actually answer anything.
> On its own the mod just holds questions in a ring buffer and waits. The piece
> that reads them, calls an AI model, and answers back is the **AI Agent
> Bridge service**, a small program you run alongside your server. It brings
> its own API key and picks the model, this mod never talks to any AI
> provider directly. Downloads and setup instructions are on GitHub:
>
> # → https://github.com/bits-orio/ai-agent-bridge

## Setup in three steps

1. **Install this mod** (you've done this, or get it from the in-game mod browser).
2. **Enable RCON** on your server, the service uses it to poll questions and
   send answers. The repo shows exactly how for each hosting style.
3. **Run the service** from [the GitHub repo](https://github.com/bits-orio/ai-agent-bridge)
   and point it at your server. Bring your own OpenRouter key and pick the
   model in its config.

Once it's running, type `/ask <question>` in chat. The question is echoed to
everyone who will see the answer, since a command is not a chat line, and the
answer comes back a few seconds later, in chat, to everyone on the server so
anyone can follow up.
`/ask new ...` starts a fresh session, `/ask #iron ...` uses a named one that
others can join, and `/ask sessions` lists what is open.

## Settings

All of them are runtime-global: change them from Settings > Mod settings
while the server runs, no restart needed.

| setting | default | what it does |
|---|---|---|
| `aab-events-enabled` | on | Append deaths, joins, leaves, chat, questions, answers, research and rocket launches to `script-output/ai-agent-bridge/events.jsonl`. Turn it off and the file stops growing. |
| `aab-chat-prefix` | blank (off) | Blank means only `/ask` asks a question. Set it and any chat line starting with those exact characters becomes a question, with the rest of the line as the text. |
| `aab-answer-audience` | server | `server` prints every global answer to the whole server, so anyone can follow up on it. `asker` prints only to the player who asked. A question a chat privacy mod marked private always prints to its own audience, whatever this says. |
| `aab-ask-cooldown-seconds` | 5 | A player who asks again sooner is told to wait, privately; no echo, no question, no cost. 0 turns it off. |
| `aab-asks-per-minute` | 30 | When the whole server has asked this many times in a minute, further asks are refused privately until the minute turns. 0 turns it off. |

The chat prefix is matched literally, spaces included, and never as a pattern.
Pick something no ordinary sentence starts with, `?` or `@ai ` for example, or
a prefix like `ai` will also fire on "airlocks are cheaper".

A question asked by another mod, or by a player who has since left, prints
to the server (or to its private audience) like any other.

---

## For mod authors, the five seams

The companion knows nothing about any other mod. Five frozen, additive-only
seams let other mods add tools, submit questions, receive answers, say who
may hear an answer, and say what players call a force.

### 1. Tools by probe, `agent_tools_v1`

Add a zero-argument `agent_tools_v1` function to any remote interface your
mod already owns. The companion scans `remote.interfaces` for it on every
agent turn, nothing to register, no dependency in either direction, and a
removed mod just vanishes from the catalog on the next scan.

```lua
-- Everything a provider needs to write, in full, no change to its own
-- frozen API, no dependency on this mod.
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
        },
      },
    }
  end,
  team_list      = function(args) --[[ args.force is injected; return plain data ]] end,
  team_standings = function(args) --[[ ditto ]] end,
})
```

Notes:

- `force` is reserved. The service injects it into every tool call's
  argument table; a provider declares it nowhere in its own manifest.
- Parameters use one grammar: `<type>[!] <description>`, type one of
  `string`, `integer`, `number`, `boolean`, trailing `!` for required.
- Tools return plain data only, a Lua table leaks through `remote.call`
  intact, and a value that can't be serialised to JSON breaks the caller.
- Your tool is always handed a table, even when the caller sent no arguments,
  so `args.force` is safe to read without checking the table itself.
- Raise a message a model should read with `error(message, 0)`. Level 0 keeps
  your mod's file and line out of the sentence the agent sees.
- Keep each tool bounded. A result over the size cap the service enforces is
  refused, never truncated.
- Your manifest is checked before it reaches the agent. An entry whose `desc`
  is not a string, or whose `params` is not a map of strings, is dropped with
  one line in the log, and a probe that errors or returns no `tools` table
  costs you every tool but nobody else theirs. A dropped tool cannot be
  called either, so a typo in a manifest shows up as `no_tool` rather than as
  a broken catalog.
- Keep your manifest small enough to send on its own. It is fetched one
  provider at a time, so wordy descriptions cost you your own tools and
  nobody else's, but they do still cost you yours.

### 2. Questions by interface, `ai-agent-bridge-v1`

Frozen, owned by this mod. Guard every call the way you'd guard a call into
any optional dependency:

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
| `ask` | `{ text, player_index?, force?, scope? }` | the new question's id, or `nil` if `text` was missing |
| `get_event_id` | `"on_answer"` | this session's event id for `on_answer`, or `nil` |

`text` must be a non-empty string. `force` must be the force's *name*, a
string, and `player_index` a number; pass a `LuaForce` or a name where an index
belongs and that field is dropped rather than stored, because one unencodable
question would break every later poll for everybody. Nothing here errors: a
mistake in your spec costs you the question, never a crash.

### 4. Chat scope by probe, `chat_scope_v1`

A mod with a chat privacy feature, team-only chat for instance, decides who
may hear an answer. Add a `chat_scope_v1(player_index, text)` function to any
remote interface you own; the companion scans for it when a question is
created, the way it scans for tools, and stores nothing.

```lua
chat_scope_v1 = function(player_index, text)
  local player = game.get_player(player_index)
  if not (player and my_channel_is_team_only(player)) then
    return { key = "global", private = false, tag = GLOBAL_BADGE }
  end
  return {
    key = player.force.name,                   -- equal keys share sessions
    private = true,                            -- the answer stays inside the audience
    audience = { force = player.force.name },  -- or { players = { 1, 5, 9 } }
    label = "Team 3",                          -- optional, shown by /ask sessions
    tag = TEAM_BADGE,                          -- optional rich text, printed verbatim
  }
end
```

`text` is the line as typed, so a "shout" rule can apply. A nil return means
global. When several mods answer, a private answer wins over a global one. The
result is fixed on the question when it is asked: an answer prints to that
audience however the channel has changed since, so a team leaving private
mode never sees its private session continue in the open. The `scope` table
`ask` accepts has the same shape, for a mod asking on behalf of a channel of
its own.

### 5. Force labels by probe, `force_labels_v1`

A mod that names forces, a team mod calling `team-1` "Team Ace" for instance,
adds a zero-argument `force_labels_v1` to any interface it owns, returning
`{ ["team-1"] = "Team Ace", ... }`. The companion scans for it and strips rich
text. The swap happens at the edges and costs the model nothing: the service
turns a label in a question into the force name before the model reads it
("how is Team Ace doing" becomes "how is team-1 doing", and a bare "Ace" works
too), and the renderer turns force names in an answer back into labels. Only
force names with a digit, hyphen or underscore take part, so `player` stays a
word. The `labels` op lists the merged map.

### 3. Answers by event, `on_answer`

Raised once per answered question, for any subscriber. `e.artifact` is what
the model submitted; `e.shape` and `e.lines` are what this mod rendered from
it, the same lines the `answers` op returns. `get_event_id` must
be resolved fresh every session, a `generate_event_name()` id is only valid
in the session that generated it, so fetch it from `on_init` and
`on_configuration_changed` (where `remote.call` is legal), cache it in
`storage`, and read the cached value back in `on_load` (where it isn't):

```lua
local function on_answer(e) --[[ e.qid, e.question, e.artifact, e.shape, e.lines ]] end

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
script.on_load(register_handler) -- no remote.call here; reuses the cached id
```

---

## The protocol, `aab-rpc-v1`

The `/aab-rpc` command answers RCON and the server console only. A player
typing it into their own console gets one private line and nothing runs.

One console command, `/aab-rpc <json>`. One JSON object in, one JSON object
out through `rcon.print`. Every reply is `{"ok":true,"r":...}` or
`{"ok":false,"e":"<code>","m":"<detail>"}`. Every request needs `{"v":1, ...}`.

| op | request | reply `r` | writes storage |
|---|---|---|---|
| `status` | `{}` | protocol version, mod version, tick, connected player count, pending question count, `last_id`, which `/ask` command name is live | no |
| `providers` | `{}` | sorted list of `{iface, v, tools}`, one per provider, `tools` being the sorted tool names | no |
| `manifest` | `{i}` | one provider's manifest verbatim, `{v, tools}` | no |
| `tools` | `{}` | sorted list of `{iface, v, tools}`, one per provider, manifests and all | no |
| `call` | `{i, f, a?}` | the provider's return value, plain data | no |
| `poll` | `{after?, limit?}` | unanswered questions with id greater than `after`, oldest first, each `{id, text, player_index, player_name, force, tick}` | no |
| `answer` | `{qid, artifact}` | `true` | yes: marks answered, renders it, raises `on_answer` |
| `answers` | `{after?, limit?}` | answered questions with id greater than `after`, oldest first, each `{id, shape, lines, player_index}` | no |

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_artifact`, `bad_result`, `too_large`, `no_question`.

### Reading the catalog in two steps

`providers` then one `manifest` per provider is how a client should read the
catalog. Every reply this command sends has a byte cap, and `tools` puts every
provider on the server under one of them: install one mod with forty wordy tool
descriptions and the whole catalog comes back `too_large`, which leaves an agent
answering with no tools at all, the companion's own included. `providers` is
names only, so it stays small however much anyone had to say, and a `manifest`
too large to send costs that one provider its tools and nobody else theirs.
`tools` stays for small servers and for the test harness.

`manifest` takes `i`, the provider's interface name, and answers `no_provider`
when nothing by that name carries a probe. Both ops drop exactly what the
catalog drops: an entry whose `desc` is not a string, a provider whose probe
errors.

`a` is optional on `call`. A tool always receives a table, so a tool that takes
no arguments can be called with none and a tool that needs `force` answers
"force is required" in its own words rather than erroring on a nil argument. An
`a` that is not an object is `bad_json`.

`after` defaults to 0 and `limit` to 16, with 64 the most any one reply
carries. Page by sending the id of the last row you saw as the next `after`.

`poll` returns questions nobody has answered yet, so `after` 0 means
"everything still waiting" and a client that restarts and forgets its cursor
re-answers nothing. `last_id` on `status` is the highest id the game has
issued, which is the cursor a client wants for "tell me about questions from
here on". Nothing about a cursor is stored in the save.

`player_name` is the asker's name as it was when they asked, kept on the row
even after they leave, because history is keyed by player name rather than by
index. It is absent for a question another mod asked with no player.

`answer` validates the artifact before it touches anything: a shape it does
not know, or a field of the wrong type, comes back as `bad_artifact` and the
question stays pending and answerable. Rendering happens before the question
is marked answered, so an artifact that cannot be drawn never leaves a
question marked done with nothing behind it. Answering a question that is
already answered succeeds and changes nothing, so a client whose reply went
missing can safely send the same answer again.

`lines` on an `answers` row is exactly what the asker saw, title first when
the artifact had one, already clipped to the shape's caps and stripped of
control characters. The `answer` op stores them; `answers` only reads them.

This mod never writes `storage` from the `aab-rpc` command except on
`answer`. A lost RCON reply costs nothing, the service re-polls the same
cursor.

### Phase 0 diagnostic ops

Used to measure the transport assumptions the design rests on; not part of
the frozen v1 surface above.

| op | request | reply `r` |
|---|---|---|
| `ping` | `{}` | `{player_index, tick, has_player}` as received by the command |
| `big` | `{kb}` | a JSON string of roughly `kb` kilobytes, to find where RCON truncates a reply, `kb` clamped to 4096 |
| `write` | `{}` | increments a counter in storage, raises `on_answer` with a test payload, returns the new counter |
| `pcall_test` | `{}` | calls a self-test provider that always errors, returns whether `pcall` caught it |

## Answer artifacts

The model fills one of five shapes; this mod renders it, never the other way
round. Every shape but `notice` may carry a `title`, which becomes the first
chat line. Every shape may carry `session = { name, fresh }`, rendered as a
`(new session)` or `(new session #name)` marker after the channel tag when
`fresh` is true.

- `summary`, up to three lines.
- `comparison`, two named columns, up to five rows.
- `list`, up to ten rows, one line each.
- `table`, up to five columns, up to eight rows.
- `notice`, one line, a warning or confirmation.

Every string has its control characters replaced by spaces, so a player name
echoed back into an answer cannot forge an extra line, and is clipped to 640
bytes on a UTF-8 boundary. That clip is a backstop for a client that sends
something silly: the service clips every cell to 160 characters first, and 160
characters of Japanese or emoji is up to 640 bytes. Factorio rich text passes
through untouched; the service sends icons as sprites alone, `[img=item.iron-plate]`
rather than `[item=iron-plate]`, so an answer reads as icons and numbers the way a
player's own line does. A bare hyphenated prototype name the model writes anyway,
`iron-ore` or `assembling-machine-2`, is turned into its sprite at render time; the
companion knows every prototype, so the lookup is exact.

The first chat line is laid out like a player's own: the companion's name,
the channel tag the scope provider gave, the session marker, a colon, then
the answer. A `table` prints its column names and one row per line with
` | ` between cells.

## Tools this mod provides

The companion is a provider like any other mod, on the interface
`ai-agent-bridge-tools`. `force` is injected into every one of these by the
service.

| tool | arguments | returns |
|---|---|---|
| `list_forces` | `limit` | the forces: name, player count, connected player count, with `total` and `shown` |
| `list_players` | `connected`, `limit` | that force's players: name, connected, admin, with `known`, `total` and `shown` |
| `list_surfaces` | `limit` | the surfaces: name, index, planet if it has one, how many of that force's players stand on it, with `total` and `shown` |
| `current_research` | force only | what that force is researching, and its progress |
| `research_queue` | `limit` | the running technology and the queue behind it, in engine order: name, level, research units, progress, with `queued` and `shown` |
| `tech_status` | `tech`, `limit` | one technology: researched, enabled, available, level, units, progress, and which prerequisites are still missing |
| `item_rate` | `surface`, `item`, `window` | production and consumption of one item, per minute, summed over every quality |
| `top_items` | `surface`, `window`, `n` | the n most-produced items, ranked, each summed over every quality |
| `production_since` | `surface`, `item`, `since_tick` | how many of one item that force produced and consumed since a tick, over every quality |
| `logistics_summary` | `surface`, `limit` | that force's logistic networks on one surface: robot totals, robots available, cells, and the eight largest item counts, busiest network first |
| `entity_count` | `surface`, `name` | how many entities of one prototype name that force has on one surface, counted by the engine |
| `evolution` | `surface` | the evolution factor on one surface, and its time, pollution and spawner-kill parts |
| `pollution` | `surface` | total pollution on one surface, and which pollutant it uses |
| `rockets` | `limit` | rockets launched by that force, and the items it sent up, largest first |
| `game_time` | force only | tick, ticks played, hours played, connected players on the server and on that force |
| `find_entities` | `surface`, one of `name`, `type`, `recipe`, `product`, `ghost`, `limit` | where that force's entities are on one surface: name, x, y and a ready `[gps=x,y,surface]` tag per row, filtered by prototype name, entity type, the recipe a crafting machine is set to or the item or fluid that recipe makes; ghosts match by what they will become and rows carry `ghost = true`, `ghost` narrows to ghosts or built; with `scanned`, `truncated`, `total` and `shown`; an unknown name comes back with up to five close names |
| `locate_player` | `player` | where one player's character is: surface, x, y, a ready `[gps=...]` tag, whether they are connected, and `viewing` when they are looking at another surface in remote view |

Every tool that lists things is bounded, because a reply over the byte cap is
refused whole rather than cut short. `list_players` shows connected players
only unless you pass `connected = false`, and returns 20 rows by default, 50 at
most. `list_surfaces` returns 20 by default and 50 at most; `list_forces` 50 by
default and 100 at most. All three sort by name before they cut and report
`total` beside `shown`, so an agent can say "12 online of 214 known" instead of
believing it saw everyone. `research_queue` and `tech_status` return 10 rows by
default and 25 at most, `rockets` the same, and `logistics_summary` 5 networks,
which is also its maximum, with eight item rows inside each. Contents and items
are ranked by count before the cut, so what survives is the part worth reading.
`find_entities` asks the engine for at most 2,000 entities of one force on one
surface, one bounded pass however large the base, then keeps the ones on the
recipe asked for and returns 5 positions by default, 10 at most; `truncated`
says when the pass hit its cap.

Every double in a reply is rounded before it is sent: four decimals for an
evolution factor, two for a progress fraction, a rate, an hour count or a
pollution total. A raw double reaches a reader as fifty-odd digits of
`0.3100000000000000088817841970012523233890533447265625`, which spends the
reply's budget on nothing and invites a model to quote precision that was never
measured.

Every tool that takes a `surface` takes either a name or the index
`list_surfaces` publishes, resolves it the same way as every other, and answers
`found = false` with a reason when this game has no such surface. `tech_status`
does the same for a technology name and `entity_count` for an entity prototype
name. Those three arguments are the ones a model guesses from memory, and a
guess that costs a whole round teaches it nothing; a reply that says "no surface
by that name or index, call `list_surfaces`" gets the next call right. "Surface
is required" is reserved for an argument that was genuinely absent. A force name that does
not exist is still an error, because the service injects that one rather than
guessing it.

`logistics_summary` reads the force's own list of networks, `entity_count` asks
the engine to count, and `rockets`, `game_time` and `evolution` read counters the
engine already keeps. None of them walks entities in Lua, so they cost the same
on a thousand-hour base as on a fresh map. `pollution` is the exception worth
knowing about: it is the engine's whole-surface sum, which visits every chunk
holding pollution.

`item_rate`, `top_items` and `production_since` read one figure per quality and
add them up, because the engine treats a bare item name as normal quality alone:
a force making the same plate at five qualities would otherwise be told its own
production was a fifth of what it is. `logistics_summary` aggregates a network's
contents by item name for the same reason, reports `distinct_items` as the number
of names, and adds a `qualities` breakdown to a row held at more than one. A row
held at a single quality other than normal says which one.

`research_queue` gives a level-based technology one row per queue entry, because
three queued levels of mining productivity are three entries resolving to one
technology. Each row reports the level it will research, the current level plus
the repeats ahead of it. `units` and `progress` belong to the first row only, so
never sum `units` over the repeats: the later levels cost more and the engine
keeps that in a count formula this does not evaluate.

`window` is one of the engine's own precisions: `five_seconds`, `one_minute`,
`ten_minutes`, `one_hour`, `ten_hours`, `fifty_hours`,
`two_hundred_fifty_hours`, `one_thousand_hours`.

`production_since` pairs with a tick out of the event history, so "how much
iron since I last died" is one call. It sums the engine's flow samples from the
smallest precision window that covers the elapsed ticks. The engine keeps 300
samples per window, so the sum rounds up to a whole number of samples. The
reply reports `elapsed_ticks`, `covered_ticks` and `covers_full_period` so you
can see by how much, and the newest sample is still filling as you read it.
Both errors shrink as the period grows.

## `events.jsonl` line shape

Appended to `script-output/ai-agent-bridge/events.jsonl`, gated by the
`aab-events-enabled` setting, truncated once per session and appended to after
that. The server writes it, never a client. One JSON object per line:

```json
{"event":"player_died","tick":1234,"data":{"player":"Bob","force":"player","cause":"small-biter"}}
{"event":"question","tick":1240,"data":{"qid":7,"player":"Bob","force":"player","text":"how much iron since I died"}}
{"event":"answer","tick":1512,"data":{"qid":7,"shape":"summary"}}
```

| `event` | `data` |
|---|---|
| `player_died` | `player`, `force`, `cause` |
| `player_joined`, `player_left` | `player`, `force` |
| `console_chat` | `player`, `force`, `message` |
| `research_finished` | `force`, `tech`, `level` |
| `rocket_launched` | `force`, `surface` |
| `question` | `qid`, `player`, `force`, `text` |
| `answer` | `qid`, `shape` |

A question a player asked with the chat prefix appears twice, once as the
`console_chat` line they typed and once as the `question` it became.

See the [GitHub repo](https://github.com/bits-orio/ai-agent-bridge) for the
full design (`CONTEXT.md`, `PLAN.md`) and the service that drives this
protocol.

Question text is kept to 400 bytes, cut on a UTF-8 boundary, so a full poll page of sixteen questions always fits one reply. The service halves its page size if a reply is still refused as too large.
