-- entity_count: how many of one entity a force owns on one surface. The count
-- happens inside the engine, over the whole surface, so this is one C++ pass and
-- not a Lua walk over entities however large the base is.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaSurface.html,
-- https://lua-api.factorio.com/2.0.77/concepts/EntitySearchFilters.html and
-- https://lua-api.factorio.com/2.0.77/classes/LuaPrototypes.html:
--   LuaSurface::count_entities_filtered(filter) -> uint32
--   EntitySearchFilters: area, position and radius are all optional, so a filter
--     with none of them covers the surface; name :: EntityID or array[EntityID];
--     force :: ForceSet
--   prototypes, the global LuaPrototypes object, ::entity
--     LuaCustomTable[string -> LuaEntityPrototype]

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")

local M = {}

M.manifest = {
  entity_count = {
    desc = "How many entities of one prototype name one force has built on one surface, counted by the engine over the whole surface. Answers \"how many labs do we have\". Pass the internal prototype name, not the display name. A name no prototype has, or a surface this game does not have, comes back as found = false rather than an error, so guess and read the reply.",
    params = {
      surface = "string! surface name or index from list_surfaces, for example nauvis",
      name    = "string! entity prototype name, for example lab, assembling-machine-2, rocket-silo",
    },
  },
}

local function entity_count(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  if type(a.name) ~= "string" or a.name == "" then error("name is required", 0) end

  -- Checked here rather than inside the filter: an unknown prototype name is the
  -- likeliest thing to be wrong about, and a clear found = false beats the
  -- engine's own error for the model's next round.
  if not prototypes.entity[a.name] then
    return {
      found = false, force = force.name, surface = surface.name, name = a.name,
      reason = "no entity prototype by that name: use the internal name, for example assembling-machine-2",
    }
  end

  return {
    found = true,
    force = force.name,
    surface = surface.name,
    name = a.name,
    count = surface.count_entities_filtered{ force = force.name, name = a.name },
  }
end

M.functions = { entity_count = entity_count }

return M
