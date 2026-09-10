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
   and point it at your server. Bring your own Anthropic API key.

Once it's running, type `/ask <question>` in chat and the answer comes back a
few seconds later, in chat or in a popup window depending on its shape.

## Settings

All three are runtime-global: change them from Settings > Mod settings while
the server runs, no restart needed.

| setting | default | what it does |
|---|---|---|
| `aab-events-enabled` | on | Append deaths, joins, leaves, chat, questions, answers, research and rocket launches to `script-output/ai-agent-bridge/events.jsonl`. Turn it off and the file stops growing. |
| `aab-chat-prefix` | blank (off) | Blank means only `/ask` asks a question. Set it and any chat line starting with those exact characters becomes a question, with the rest of the line as the text. |
| `aab-answer-style` | auto | `auto` opens a popup for tables, comparisons and lists longer than three items, and prints everything else to chat. `chat` always prints. `popup` always opens the window. |

The chat prefix is matched literally, spaces included, and never as a pattern.
Pick something no ordinary sentence starts with, `?` or `@ai ` for example, or
a prefix like `ai` will also fire on "airlocks are cheaper".

The popup needs the asker to still be connected. A question asked by another
mod, or by a player who has since left, is answered in chat whatever the
setting says.

---

## For mod authors, the three seams

The companion knows nothing about any other mod. Three frozen, additive-only
seams let other mods add tools, submit questions, and receive answers.

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
- Keep each tool bounded. A result over the size cap the service enforces is
  refused, never truncated.

### 2. Questions by interface, `ai-agent-bridge-v1`

Frozen, owned by this mod. Guard every call the way you'd guard a call into
any optional dependency:

```lua
if remote.interfaces["ai-agent-bridge-v1"] then
  local qid = remote.call("ai-agent-bridge-v1", "ask", {
    text = "How's the north force doing on science?",
    force = "north",       -- optional force hint
    player_index = nil,    -- optional, if this came from a player
  })
end
```

| function | args | returns |
|---|---|---|
| `ask` | `{ text, player_index?, force? }` | the new question's id, or `nil` if `text` was missing |
| `get_event_id` | `"on_answer"` | this session's event id for `on_answer`, or `nil` |

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

One console command, `/aab-rpc <json>`. One JSON object in, one JSON object
out through `rcon.print`. Every reply is `{"ok":true,"r":...}` or
`{"ok":false,"e":"<code>","m":"<detail>"}`. Every request needs `{"v":1, ...}`.

| op | request | reply `r` | writes storage |
|---|---|---|---|
| `status` | `{}` | protocol version, mod version, tick, connected player count, pending question count, which `/ask` command name is live | no |
| `tools` | `{}` | sorted list of `{iface, v, tools}`, one per provider, manifests verbatim | no |
| `call` | `{i, f, a}` | the provider's return value, plain data | no |
| `poll` | `{after}` | questions with id greater than `after`, oldest first | no |
| `answer` | `{qid, artifact}` | `true` | yes: marks answered, renders it, raises `on_answer` |
| `answers` | `{after}` | answered questions with id greater than `after`, oldest first, each `{id, shape, lines, player_index}` | no |

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_result`, `too_large`, `no_question`.

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
| `big` | `{kb}` | a JSON string of roughly `kb` kilobytes, to find where RCON truncates a reply |
| `write` | `{}` | increments a counter in storage, raises `on_answer` with a test payload, returns the new counter |
| `pcall_test` | `{}` | calls a self-test provider that always errors, returns whether `pcall` caught it |

## Answer artifacts

The model fills one of five shapes; this mod renders it, never the other way
round. Every shape but `notice` may carry a `title`, which becomes the first
chat line or the popup's window caption.

- `summary`, up to three lines.
- `comparison`, two named columns, up to five rows.
- `list`, up to ten rows, one line each.
- `table`, up to five columns, up to eight rows.
- `notice`, one line, a warning or confirmation.

Every string is clipped to 160 bytes on a UTF-8 boundary and has its control
characters replaced by spaces, so a player name echoed back into an answer
cannot forge an extra line. Factorio rich text such as `[item=iron-plate]`
passes through untouched.

In a popup, the `table` shape becomes a real GUI table with a bold header row.
Every other shape becomes a column of labels. The window centres itself, its
titlebar drags, and Esc or the close button dismisses it.

## Tools this mod provides

The companion is a provider like any other mod, on the interface
`ai-agent-bridge-tools`. `force` is injected into every one of these by the
service.

| tool | arguments | returns |
|---|---|---|
| `list_forces` | force only | every force: name, player count, connected player count |
| `list_players` | force only | that force's players: name, connected, admin |
| `current_research` | force only | what that force is researching, and its progress |
| `list_surfaces` | force only | every surface: name, index, planet if it has one, how many of that force's players stand on it |
| `item_rate` | `surface`, `item`, `window` | production and consumption of one item, per minute |
| `top_items` | `surface`, `window`, `n` | the n most-produced items, ranked |
| `production_since` | `surface`, `item`, `since_tick` | how many of one item that force produced and consumed since a tick |

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
