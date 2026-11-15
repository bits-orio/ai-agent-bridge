-- AI Agent Bridge - scripts/tools/anchor.lua
-- Author: bits-orio
-- License: MIT
--
-- Where a "near me" walk starts: a player's position, on the surface the
-- caller asked about. Shared by machine_status and resources so the two read
-- "near" the same way and clamp the radius the same way.
--
-- API names verified against runtime-api.json 2.1.23: LuaGameScript::get_player
-- (index or name, nil when none), LuaPlayer::valid, ::position, ::surface,
-- ::physical_position, ::physical_surface.

local M = {}

--- A radius from a caller: a whole number from 1 to `max`, `default` when it
--- is missing or not a number. Not bounded.limit, which clamps counts: a
--- radius is a distance and the clamp is a cost bound on the circle's area.
function M.radius(value, default, max)
  local n = math.floor(tonumber(value) or default)
  return math.max(1, math.min(max, n))
end

--- The player's position when they are on `surface`, else nil plus a reason.
--- The character's own place wins over the view: "near me" means where the
--- character stands. A player in remote view of `surface` with the character
--- elsewhere is anchored at the view's position, which is what they are
--- looking at and asking about.
function M.position(near, surface)
  local player = type(near) == "string" and near ~= "" and game.get_player(near) or nil
  if not (player and player.valid) then
    return nil, "no player by that name: pass a player name"
  end
  local physical = player.physical_surface
  if physical and physical.valid and physical.index == surface.index and player.physical_position then
    return player.physical_position
  end
  if player.surface and player.surface.valid and player.surface.index == surface.index then
    return player.position
  end
  local where = (physical and physical.valid and physical.name) or (player.surface and player.surface.name) or "?"
  return nil, near .. " is on " .. where .. ", not " .. surface.name .. ": ask about that surface"
end

return M
