-- list_surfaces: what places exist in this game, and where one force's players
-- are standing. The agent needs it before any tool that takes a surface name,
-- because a modded game can have surfaces nobody would guess.
--
-- API names verified against the official docs (fetched 2026-09-10):
-- LuaSurface::name, LuaSurface::index, LuaSurface::planet (LuaPlanet?),
-- LuaPlanet::name, LuaControl::surface.

local force_lookup = require("scripts.tools.force_lookup")
local bounded      = require("scripts.tools.bounded")

local DEFAULT_SURFACES = 20
local MAX_SURFACES = 50

local M = {}

M.manifest = {
  list_surfaces = {
    desc = "Surfaces in the game: name, index, planet, this force's players on it, by name. Pass a name or index as another tool's surface. total beside shown says if there are more.",
    params = {
      limit = "integer rows, default " .. DEFAULT_SURFACES .. ", max " .. MAX_SURFACES,
    },
  },
}

local function planet_name(surface)
  local planet = surface.planet
  if planet and planet.valid then return planet.name end
  return nil
end

local function list_surfaces(a)
  local force = force_lookup.require_force(a.force)

  local players_on = {}
  for _, player in pairs(force.players) do
    local surface = player.surface
    if surface and surface.valid then
      players_on[surface.index] = (players_on[surface.index] or 0) + 1
    end
  end

  -- Every space platform is a surface of its own, so a long-running save can
  -- hold a hundred of them. Sorted, then cut.
  local rows = {}
  for _, surface in pairs(game.surfaces) do
    rows[#rows + 1] = {
      name = surface.name,
      index = surface.index,
      planet = planet_name(surface),
      force_players = players_on[surface.index] or 0,
    }
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_SURFACES, MAX_SURFACES))
  return { force = force.name, total = #rows, shown = #shown, surfaces = shown }
end

M.functions = { list_surfaces = list_surfaces }

return M
