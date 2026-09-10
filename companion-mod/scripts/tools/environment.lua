-- evolution and pollution: what the surface itself is doing to a force. They
-- share a file because they share a subject and an argument, and because one
-- feeds the other: pollution is one of the three inputs the engine adds up into
-- the evolution factor.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaForce.html and
-- https://lua-api.factorio.com/2.0.77/classes/LuaSurface.html:
--   LuaForce::get_evolution_factor(surface?) -> double
--   LuaForce::get_evolution_factor_by_time(surface?) -> double
--   LuaForce::get_evolution_factor_by_pollution(surface?) -> double
--   LuaForce::get_evolution_factor_by_killing_spawners(surface?) -> double
--     All four take SurfaceIdentification, which accepts a LuaSurface directly.
--   LuaSurface::get_total_pollution() -> double, "Gets the total amount of
--     pollution on the surface by iterating over all the chunks containing
--     pollution."
--   LuaSurface::pollutant_type -> LuaAirbornePollutantPrototype?, "nil if no
--     pollutant is enabled"; its name comes from LuaPrototypeBase::name.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local bounded        = require("scripts.tools.bounded")

-- Four decimals on an evolution factor, which is where the number stops meaning
-- anything to a player, and two on a pollution total. Without this each one
-- reaches the model as fifty-odd digits of a double (scripts/tools/bounded.lua).
local EVOLUTION_PLACES = 4

local M = {}

M.manifest = {
  evolution = {
    desc = "How far the enemies on one surface have evolved against one force, from 0 to 1, plus the three parts the engine adds up to get there: time survived, pollution absorbed, spawners killed. Say which part is driving the number. Evolution is per surface in this version, so ask about the surface the question is about. An unknown surface comes back as found = false.",
    params = {
      surface = "string! surface name or index from list_surfaces, for example nauvis",
    },
  },
  pollution = {
    desc = "Total pollution on one surface right now, and which pollutant that surface uses. Context for an evolution answer. This is the whole-surface sum, not a reading at one position, and a surface with pollution turned off answers 0 with pollution_enabled false. An unknown surface comes back as found = false.",
    params = {
      surface = "string! surface name or index from list_surfaces, for example nauvis",
    },
  },
}

local function evolution(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  return {
    found = true,
    force = force.name,
    surface = surface.name,
    evolution_factor = bounded.round(force.get_evolution_factor(surface), EVOLUTION_PLACES),
    by_time = bounded.round(force.get_evolution_factor_by_time(surface), EVOLUTION_PLACES),
    by_pollution = bounded.round(force.get_evolution_factor_by_pollution(surface), EVOLUTION_PLACES),
    by_killing_spawners = bounded.round(force.get_evolution_factor_by_killing_spawners(surface), EVOLUTION_PLACES),
  }
end

local function pollution(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local pollutant = surface.pollutant_type
  return {
    found = true,
    force = force.name,
    surface = surface.name,
    pollution_enabled = pollutant ~= nil,
    pollutant = pollutant and pollutant.name or nil,
    total_pollution = bounded.round(surface.get_total_pollution(), 2),
  }
end

M.functions = { evolution = evolution, pollution = pollution }

return M
