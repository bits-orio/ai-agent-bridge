-- AI Agent Bridge - scripts/tools/window_age.lua
-- Author: bits-orio
-- License: MIT
--
-- Says, on every rate reply, how long the averaging window is and how old
-- the map is, so a rate read over a window longer than the map's whole life
-- is marked rather than passed off as the force's steady output. The engine
-- averages get_flow_count over the window's full span, the time before the
-- map existed counted as zeroes (review finding T6). Measured on a fresh
-- 2.1.23 map 51 seconds old: 600 iron plates added with on_flow read 600 a
-- minute over one_minute, 60 over ten_minutes, 10 over one_hour and 1 over
-- ten_hours. So a partial window's rate is understated by map age over
-- window length. The figure is left as the engine gave it, never rescaled,
-- and the two ages are reported beside it so the caller can tell.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaGameScript::ticks_played (MapTick): ticks since the game was created,
--   which unlike ::tick starts at 0 for a game made from a scenario.

local M = {}

--- Adds window_ticks, map_age_ticks and, when the map is younger than the
--- window, partial_window = true to `reply`. `window` is flow.require_window's
--- {precision, ticks}. Returns the reply.
function M.stamp(reply, window)
  local age = game.ticks_played
  reply.window_ticks = window.ticks
  reply.map_age_ticks = age
  if age < window.ticks then reply.partial_window = true end
  return reply
end

return M
