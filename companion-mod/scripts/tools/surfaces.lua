-- AI Agent Bridge - scripts/tools/surfaces.lua
-- Author: bits-orio
-- License: MIT
--
-- list_surfaces: what places exist in this game, and where one force's players
-- are standing. The agent needs it before any tool that takes a surface name,
-- because a modded game can have surfaces nobody would guess.
--
-- API names verified against the official docs (fetched 2026-09-10):
-- LuaSurface::name, LuaSurface::index, LuaSurface::planet (LuaPlanet?),
-- LuaPlanet::name, LuaControl::surface.

local force_lookup    = require("scripts.tools.force_lookup")
local bounded         = require("scripts.tools.bounded")
local platform_lookup = require("scripts.tools.platform_lookup")

-- Rows sort by name and a space platform is named platform-N, so every platform
-- sorts after every planet. At a default of 20 the measured server returned 20
-- of 25 surfaces and not one platform, which is why question 80 guessed
-- platform-1 before it had seen one and then spent a round re-listing with a
-- limit to find out it had guessed right. The default is the cap now: a surface
-- row is small, and a list that silently omits a whole class of surface is
-- worse than a longer one.
local DEFAULT_SURFACES = 50
local MAX_SURFACES = 50

local M = {}

M.manifest = {
  list_surfaces = {
    desc = "Surfaces in the game by name: index, planet, this force's players on it (force_players counts offline ones too, force_players_online the connected). A platform row adds platform, owner, location (left out in flight) and state. Pass a name or index as another tool's surface.",
    params = {
      limit  = "integer rows, default " .. DEFAULT_SURFACES .. ", max " .. MAX_SURFACES,
      offset = bounded.OFFSET_PARAM,
      kind   = "enum<all,planets,platforms> default all; platforms lists space platforms alone",
    },
  },
}

local function planet_name(surface)
  local planet = surface.planet
  if planet and planet.valid then return planet.name end
  return nil
end

--- Players of `force` per surface index: every one it has had, and the
--- connected ones. force.players includes players who are offline, so the
--- first count alone overstated who is there now.
local function players_by_surface(force)
  local all, online = {}, {}
  for _, player in pairs(force.players) do
    local surface = player.surface
    if surface and surface.valid then
      all[surface.index] = (all[surface.index] or 0) + 1
      if player.connected then online[surface.index] = (online[surface.index] or 0) + 1 end
    end
  end
  return all, online
end

local KINDS = { all = true, planets = true, platforms = true }

--- Whether `surface` belongs in a listing of `kind`.
local function of_kind(surface, kind)
  if kind == "all" then return true end
  local platform = platform_lookup.live(surface) ~= nil
  return (kind == "platforms") == platform
end

local function list_surfaces(a)
  local force = force_lookup.require_force(a.force)
  local kind = a.kind == nil and "all" or a.kind
  if not KINDS[kind] then error("kind must be all, planets or platforms", 0) end
  local all, online = players_by_surface(force)

  -- Every space platform is a surface of its own, so a long-running save can
  -- hold a hundred of them. Sorted, then paged: platforms sort after planets,
  -- and a cut with no way on hid exactly them.
  local rows = {}
  for _, surface in pairs(game.surfaces) do
    if of_kind(surface, kind) then
      local row = {
        name = surface.name,
        index = surface.index,
        planet = planet_name(surface),
        force_players = all[surface.index] or 0,
        force_players_online = online[surface.index] or 0,
      }
      platform_lookup.merge_into(row, surface)
      rows[#rows + 1] = row
    end
  end
  bounded.by_name(rows)
  local limit = bounded.limit(a.limit, DEFAULT_SURFACES, MAX_SURFACES)
  return bounded.page(rows, a.offset, function(page)
    return { force = force.name, surfaces = page }
  end, limit)
end

M.functions = { list_surfaces = list_surfaces }

return M
