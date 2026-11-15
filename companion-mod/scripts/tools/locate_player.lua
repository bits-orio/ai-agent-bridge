-- AI Agent Bridge - scripts/tools/locate_player.lua
-- Author: bits-orio
-- License: MIT
--
-- locate_player: where one player's character is, or every connected
-- player's, with a ready [gps=...] tag and, with nearby, the built things
-- in a small circle around it. Never a walk over a surface: a radius read
-- costs its circle's area whatever the surface holds.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaPlayer::position, ::surface, ::connected, ::physical_surface,
--     ::physical_position, ::character; LuaGameScript::connected_players,
--     ::get_player.
--   LuaSurface::find_entities_filtered(filter), EntitySearchFilters
--     position with radius: the entities inside that circle and no others.

local bounded     = require("scripts.tools.bounded")
local entity_view = require("scripts.tools.entity_view")

local is_ghost, real_name, real_type, gps = entity_view.is_ghost, entity_view.real_name, entity_view.real_type, entity_view.gps

-- nearby: the circle locate_player reads around a character. 32 tiles is a
-- generous "next to", and a radius read of a circle that size is bounded by
-- its area whatever the surface holds.
local NEARBY_MAX   = 32
local NEARBY_SHOWN = 5

local M = {}

M.manifest = {
  locate_player = {
    desc = "Where one player's character is: surface, position and a ready [gps=...] tag, connected, and the surface they view when it differs (remote view). nearby=N adds the closest built things within N tiles, nearest first, each with distance and gps (what is Bob standing next to): one small circle, never a surface walk. all=true: every connected player in one call. Unknown player: found=false.",
    params = {
      player = "string player name; required unless all=true",
      all    = "boolean every connected player in one call; player is ignored",
      nearby = "integer tiles, 1 to " .. NEARBY_MAX .. ": list the built things within this radius of the character",
      offset = bounded.OFFSET_PARAM,
    },
  },
}

-- What a player stands beside is something a force placed. Scenery and
-- wildlife belong to neutral or enemy, and a few forced things are not
-- builds either: players themselves, their corpses, the marker a
-- construction request leaves, an item lying on the ground.
local NOT_A_BUILD = {
  character = true, ["character-corpse"] = true, corpse = true,
  ["item-request-proxy"] = true, ["highlight-box"] = true, ["item-entity"] = true,
}

local function is_build(entity, own_character)
  if not entity.valid or entity == own_character or NOT_A_BUILD[entity.type] then return false end
  local force = entity.force
  return force ~= nil and force.name ~= "neutral" and force.name ~= "enemy"
end

--- The built things within `radius` tiles of `position` on `surface`,
--- nearest first, cut to NEARBY_SHOWN, and how many there were before the
--- cut. EntitySearchFilters.position with .radius asks the engine for the
--- entities inside that circle and nothing else (2.0.77: "If given with
--- position, will return all entities within the radius of the position"),
--- so the cost is the circle's, never the surface's. On 2026-09-16 the
--- question "what is each player standing next to" had no bounded read to
--- reach for at all.
local function nearest_builds(surface, position, radius, own_character)
  local found = {}
  for _, entity in ipairs(surface.find_entities_filtered({ position = position, radius = radius })) do
    if is_build(entity, own_character) then
      local dx, dy = entity.position.x - position.x, entity.position.y - position.y
      found[#found + 1] = { entity = entity, distance = math.sqrt(dx * dx + dy * dy) }
    end
  end
  table.sort(found, function(p, q)
    if p.distance ~= q.distance then return p.distance < q.distance end
    return real_name(p.entity) < real_name(q.entity)
  end)
  local rows = {}
  for i, near in ipairs(bounded.cut(found, NEARBY_SHOWN)) do
    local x, y, tag = gps(near.entity.position, surface.name)
    local row = { name = real_name(near.entity), type = real_type(near.entity),
                  distance = bounded.round(near.distance, 1), x = x, y = y, gps = tag }
    if is_ghost(near.entity) then row.ghost = true end
    rows[i] = row
  end
  return rows, #found
end

local function nearby_radius(v)
  if v == nil then return nil end
  if type(v) ~= "number" then error("nearby must be a number of tiles", 0) end
  return math.max(1, math.min(NEARBY_MAX, math.floor(v)))
end

--- One player's row: where the character is, what it is looking at and,
--- with a radius, what stands beside it.
local function locate_one(player, nearby)
  -- The character's place is the answer to "where is Bob"; the controller's
  -- surface (remote view) is reported beside it when it differs, since that
  -- is what Bob is looking at right now.
  local physical = player.physical_surface and player.physical_surface.valid and player.physical_surface or player.surface
  local surface_name = physical and physical.name or "?"
  local position = player.physical_position or player.position
  local x, y, tag = gps(position, surface_name)
  local out = {
    found = true, player = player.name, connected = player.connected == true,
    surface = surface_name, x = x, y = y, gps = tag,
  }
  local viewing = player.surface and player.surface.valid and player.surface.name or nil
  if viewing and viewing ~= surface_name then out.viewing = viewing end
  if nearby and physical and physical.valid then
    out.nearby_radius = nearby
    out.nearest, out.nearby_total = nearest_builds(physical, position, nearby, player.character)
  end
  return out
end

local function locate_player(a)
  local nearby = nearby_radius(a.nearby)
  if a.all == true then
    local rows = {}
    for _, player in pairs(game.connected_players) do
      if player.valid then rows[#rows + 1] = locate_one(player, nearby) end
    end
    table.sort(rows, function(p, q) return p.player < q.player end)
    return bounded.page(rows, a.offset, function(page) return { found = true, players = page } end, bounded.MAX_FORCES)
  end
  if type(a.player) ~= "string" or a.player == "" then error("player is required unless all=true", 0) end
  local player = game.get_player(a.player)
  if not (player and player.valid) then
    return { found = false, player = a.player, reason = "no player by that name" }
  end
  return locate_one(player, nearby)
end

M.functions = { locate_player = locate_player }

return M
