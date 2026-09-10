-- list_surfaces: what places exist in this game, and where one force's players
-- are standing. The agent needs it before any tool that takes a surface name,
-- because a modded game can have surfaces nobody would guess.
--
-- API names verified against the official docs (fetched 2026-09-10):
-- LuaSurface::name, LuaSurface::index, LuaSurface::planet (LuaPlanet?),
-- LuaPlanet::name, LuaControl::surface.

local force_lookup = require("scripts.tools.force_lookup")

local M = {}

M.manifest = {
  list_surfaces = {
    desc = "Every surface in the game: name, index, the planet it belongs to if it has one, and how many of this force's players are standing on it. Call this first when a question is about a place, then pass one of these names as the surface argument of another tool.",
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

  local rows = {}
  for _, surface in pairs(game.surfaces) do
    rows[#rows + 1] = {
      name = surface.name,
      index = surface.index,
      planet = planet_name(surface),
      force_players = players_on[surface.index] or 0,
    }
  end
  table.sort(rows, function(x, y) return x.name < y.name end)
  return { force = force.name, surfaces = rows }
end

M.functions = { list_surfaces = list_surfaces }

return M
