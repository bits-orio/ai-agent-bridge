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

Once it's running, type `/ask <question>` in chat and the answer prints back
to you a few seconds later.

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

Raised once per answered question, for any subscriber. `get_event_id` must
be resolved fresh every session, a `generate_event_name()` id is only valid
in the session that generated it, so fetch it from `on_init` and
`on_configuration_changed` (where `remote.call` is legal), cache it in
`storage`, and read the cached value back in `on_load` (where it isn't):

```lua
local function on_answer(e) --[[ e.qid, e.question, e.artifact ]] end

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
| `answer` | `{qid, artifact}` | `true` | yes: marks answered, renders to chat, raises `on_answer` |

Error codes: `bad_json`, `bad_version`, `bad_op`, `no_provider`, `no_tool`,
`provider_error`, `bad_result`, `too_large`, `no_question`.

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

The model fills one of five shapes; this mod renders it to chat, never the
other way round:

- `summary`, up to three lines.
- `comparison`, two named columns, up to five rows.
- `list`, up to ten rows, one line each.
- `table`, up to five columns, up to eight rows.
- `notice`, one line, a warning or confirmation.

## `events.jsonl` line shape

Appended to `script-output/ai-agent-bridge/events.jsonl` on player deaths,
joins, leaves, console chat, research finishes and rocket launches, gated
by the `aab-events-enabled` setting. One JSON object per line:

```json
{"event":"player_died","tick":1234,"data":{"player":"Bob","force":"player","cause":"biter"}}
```

See the [GitHub repo](https://github.com/bits-orio/ai-agent-bridge) for the
full design (`CONTEXT.md`, `PLAN.md`) and the service that drives this
protocol.
