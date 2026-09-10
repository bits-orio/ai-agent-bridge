-- game_time: the clock, and who is on right now. The tool an agent reaches for
-- before it quotes any tick at a player, because a tick means nothing to anyone
-- until it is hours.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaGameScript.html and
-- https://lua-api.factorio.com/2.0.77/classes/LuaForce.html:
--   LuaGameScript::tick              R MapTick, "Current map tick."
--   LuaGameScript::ticks_played      R MapTick, "The number of ticks since this
--                                    game was created using either new game or
--                                    new game from scenario."
--   LuaGameScript::connected_players R array[LuaPlayer]
--   LuaForce::connected_players      R array[LuaPlayer]

local force_lookup = require("scripts.tools.force_lookup")

-- 60 ticks a second, 3600 seconds an hour. The engine's own nominal rate, which
-- is what a player means by "how long have we been playing" even on a server
-- that has spent time below 60 updates a second.
local TICKS_PER_HOUR = 216000

local M = {}

M.manifest = {
  game_time = {
    desc = "How long this game has been running and who is on: current tick, ticks played, hours played, connected players on the server and connected players on this force. Use it to turn a tick into hours before you quote it, and as the denominator for any \"per hour\" claim. Ticks played counts from when the map was created, so it is the number a player means by playtime.",
  },
}

local function round_hundredths(value)
  return math.floor(value * 100 + 0.5) / 100
end

local function game_time(a)
  local force = force_lookup.require_force(a.force)
  local played = game.ticks_played
  return {
    force = force.name,
    tick = game.tick,
    ticks_played = played,
    hours = round_hundredths(played / TICKS_PER_HOUR),
    connected_players = #game.connected_players,
    force_connected_players = #force.connected_players,
  }
end

M.functions = { game_time = game_time }

return M
