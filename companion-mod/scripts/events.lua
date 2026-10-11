-- AI Agent Bridge - scripts/events.lua
-- Author: bits-orio
-- License: MIT
--
-- Append-only event log the service tails into history (CONTEXT.md "Event
-- file", "History"; PLAN.md decision 4). Truncated once per session, then
-- appended, written by the server copy only. The first line of every session
-- is a `session` event naming the map (scripts/map_id.lua), so the service
-- can tell a new map from an older save of the same one.
--
-- One handler in this mod may register for any given event, so on_console_chat
-- is NOT registered here: control.lua owns the single handler and calls
-- M.on_console_chat as one of its two fan-out targets.

local chat_visibility = require("scripts.chat_visibility")
local map_id          = require("scripts.map_id")

local EVENTS_FILE = "ai-agent-bridge/events.jsonl"

-- Session-scoped Lua local (NOT storage): resets to false on every load, so
-- the first event after a new game or a load overwrites the stale file and
-- the service sees a fresh byte-count it can detect as a truncation.
local truncated_this_session = false

local function events_enabled()
  return settings.global["aab-events-enabled"].value
end

local function json_line(event_key, data)
  return helpers.table_to_json({
    event = event_key,
    tick  = game.tick,
    data  = data or {},
  }) .. "\n"
end

local M = {}

--- Appends one line to events.jsonl, or does nothing when the setting is off.
--- Public because questions and answers are logged from outside this module.
function M.write(event_key, data)
  if not events_enabled() then return end
  -- A save made before map ids has none until here. Every peer runs this
  -- same handler with the same storage, so every peer makes the same id.
  local id = map_id.ensure()
  local text = json_line(event_key, data)
  if not truncated_this_session then
    text = json_line("session", { map_id = id }) .. text
  end
  -- for_player=0: only the server writes its own copy. Without it, every
  -- connected client would independently write its own copy of the file,
  -- which is wasteful and can race on a server with multiple clients.
  helpers.write_file(EVENTS_FILE, text, truncated_this_session, 0)
  truncated_this_session = true
end

local function death_cause(e)
  if not (e.cause and e.cause.valid) then return nil end
  return (e.cause.type == "character" and e.cause.player and e.cause.player.name) or e.cause.name
end

--- Logs one chat line with who could read it (scripts/chat_visibility.lua):
--- `scope` and `private` beside the player, force and message every
--- companion has always logged. Called by control.lua's single
--- on_console_chat handler.
function M.on_console_chat(e)
  -- Nothing to log, so no scope provider is asked either.
  if not events_enabled() then return end
  -- player_index is absent for messages typed into the server's own console
  -- (stdin), not just for players. Log those too, tagged Server.
  local player = e.player_index and game.get_player(e.player_index) or nil
  if e.player_index and not player then return end
  local seen_by = chat_visibility.of(player, e.message)
  M.write("console_chat", {
    player = player and player.name or "Server",
    force = player and player.force.name or nil,
    message = e.message,
    scope = seen_by.scope,
    private = seen_by.private,
  })
end

--- Logs a player leaving. Public because control.lua fans on_player_left_game
--- out to two readers, this one and the ask rate limiter.
function M.on_player_left_game(e)
  local player = game.get_player(e.player_index)
  if not player then return end
  M.write("player_left", { player = player.name, force = player.force.name })
end

function M.register()
  script.on_event(defines.events.on_player_died, function(e)
    local player = game.get_player(e.player_index)
    if not player then return end
    M.write("player_died", { player = player.name, force = player.force.name, cause = death_cause(e) })
  end)

  script.on_event(defines.events.on_player_joined_game, function(e)
    local player = game.get_player(e.player_index)
    if not player then return end
    M.write("player_joined", { player = player.name, force = player.force.name })
  end)


  script.on_event(defines.events.on_research_finished, function(e)
    local research = e.research
    if not research then return end
    M.write("research_finished", { force = research.force.name, tech = research.name, level = research.level })
  end)

  script.on_event(defines.events.on_rocket_launched, function(e)
    local rocket = e.rocket
    if not (rocket and rocket.valid) then return end
    M.write("rocket_launched", { force = rocket.force.name, surface = rocket.surface.name })
  end)
end

return M
