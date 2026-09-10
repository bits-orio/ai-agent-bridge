-- Append-only event log the service tails into history (CONTEXT.md "Event
-- file", "History"; PLAN.md decision 4). Truncated once per session, then
-- appended, written by the server copy only.

local EVENTS_FILE = "ai-agent-bridge/events.jsonl"

-- Session-scoped Lua local (NOT storage): resets to false on every load, so
-- the first event after a new game or a load overwrites the stale file and
-- the service sees a fresh byte-count it can detect as a truncation.
local truncated_this_session = false

local function events_enabled()
  return settings.global["aab-events-enabled"].value
end

local function write_event(event_key, data)
  if not events_enabled() then return end
  local line = helpers.table_to_json({
    event = event_key,
    tick  = game.tick,
    data  = data or {},
  }) .. "\n"
  -- for_player=0: only the server writes its own copy. Without it, every
  -- connected client would independently write its own copy of the file,
  -- which is wasteful and can race on a server with multiple clients.
  helpers.write_file(EVENTS_FILE, line, truncated_this_session, 0)
  truncated_this_session = true
end

local function death_cause(e)
  if not (e.cause and e.cause.valid) then return nil end
  return (e.cause.type == "character" and e.cause.player and e.cause.player.name) or e.cause.name
end

local M = {}

function M.register()
  script.on_event(defines.events.on_player_died, function(e)
    local player = game.get_player(e.player_index)
    if not player then return end
    write_event("player_died", { player = player.name, force = player.force.name, cause = death_cause(e) })
  end)

  script.on_event(defines.events.on_player_joined_game, function(e)
    local player = game.get_player(e.player_index)
    if not player then return end
    write_event("player_joined", { player = player.name, force = player.force.name })
  end)

  script.on_event(defines.events.on_player_left_game, function(e)
    local player = game.get_player(e.player_index)
    if not player then return end
    write_event("player_left", { player = player.name, force = player.force.name })
  end)

  script.on_event(defines.events.on_console_chat, function(e)
    -- player_index is absent for messages typed into the server's own
    -- console (stdin), not just for players -- log those too, tagged Server.
    if not e.player_index then
      write_event("console_chat", { player = "Server", message = e.message })
      return
    end
    local player = game.get_player(e.player_index)
    if not player then return end
    write_event("console_chat", { player = player.name, force = player.force.name, message = e.message })
  end)

  script.on_event(defines.events.on_research_finished, function(e)
    local research = e.research
    if not research then return end
    write_event("research_finished", { force = research.force.name, tech = research.name, level = research.level })
  end)

  script.on_event(defines.events.on_rocket_launched, function(e)
    local rocket = e.rocket
    if not (rocket and rocket.valid) then return end
    write_event("rocket_launched", { force = rocket.force.name, surface = rocket.surface.name })
  end)
end

return M
