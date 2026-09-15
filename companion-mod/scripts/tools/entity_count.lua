-- AI Agent Bridge - scripts/tools/entity_count.lua
-- Author: bits-orio
-- License: MIT
--
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
local bounded        = require("scripts.tools.bounded")

-- One count_entities_filtered is a C++ pass over a surface, not a Lua walk, but
-- a sweep multiplies them: every force against every surface.
--
-- The sweep does not add game time, it removes round trips. A force owns
-- entities on its own surface and almost nowhere else, so the passes that find
-- nothing are cheap and only a handful do real work, and those same passes were
-- always going to run. Question 64 on 2026-09-15 asked how many solar panels
-- each team had and cost fourteen entity_count calls in one round: fourteen
-- RCON trips for engine work that one trip can carry.
--
-- So MAX_PASSES guards the pathological shape rather than the ordinary one. The
-- measured server runs 23 surfaces against 14 populated forces, 322 passes; a
-- save that has accumulated a hundred space platforms (surfaces.lua says they
-- do) against twenty teams is 2000, and that one refuses and says which
-- argument makes it affordable. docs/design/phase4-spec.md, "Bounding a walk"
-- and "Refusal as a Lua contract".
local MAX_PASSES = 600

local M = {}

M.manifest = {
  entity_count = {
    desc = "How many entities of one prototype one force has on one surface (how many labs do we have). Internal prototype name. Unknown name or surface: found=false. all=true answers for every force that has players in one call, one row each, largest count first: use it for any each-team or every-force question instead of one call per force. Under all=true, leave surface out to count every surface a force has, the right call for a per-team total, or name one to compare the same surface across teams.",
    params = {
      surface = "string surface name or index, e.g. nauvis; required unless all=true, where omitting it counts every surface",
      name    = "string! entity prototype name, e.g. lab, assembling-machine-2",
      all     = "boolean default false: one row per force that has players; the force argument is ignored",
    },
  },
}

--- Which forces a sweep answers for: the ones a team mod has actually put
--- players on, the same rule current_research's own sweep follows. An empty
--- slot owns nothing anywhere, so a row of zeroes for it is noise in every
--- row of the answer.
local function sweep_forces()
  local out = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then out[#out + 1] = force end
  end
  table.sort(out, function(x, y) return x.name < y.name end)
  return out
end

--- Which surfaces a sweep counts over: the one named, or every surface in the
--- game when none was, the rule production_since already follows so a force's
--- total never depends on the model guessing where that force lives. Returns
--- the surfaces, the label to quote back, and a ready-to-return miss reply when
--- a named surface does not resolve.
local function sweep_surfaces(a)
  if a.surface == nil or a.surface == "" or a.surface == "all" then
    local all = {}
    for _, surface in pairs(game.surfaces) do
      if surface.valid then all[#all + 1] = surface end
    end
    table.sort(all, function(x, y) return x.name < y.name end)
    return all, "all"
  end
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then return nil, nil, miss end
  return { surface }, surface.name
end

--- all=true is the every-team question in one call. The reply carries no
--- top-level force field: that absence is what tells a sweep from the
--- single-force shape, which carries one, and both carry rows.
local function entity_count_all(a)
  if type(a.name) ~= "string" or a.name == "" then error("name is required", 0) end
  if not prototypes.entity[a.name] then
    return {
      found = false, name = a.name,
      reason = "no entity prototype by that name: use the internal name, for example assembling-machine-2",
    }
  end

  local surfaces, label, miss = sweep_surfaces(a)
  if not surfaces then return miss end
  local forces = sweep_forces()

  -- Refused before a single pass runs, never half-counted: a partial sweep
  -- would answer with numbers that look complete.
  local passes = #forces * #surfaces
  if passes > MAX_PASSES then
    return {
      found = false, name = a.name, surface = label,
      force_count = #forces, surface_count = #surfaces,
      passes = passes, max_passes = MAX_PASSES,
      reason = "counting every force on every surface would be " .. passes ..
               " passes over the map, past the " .. MAX_PASSES .. " one call may spend: " ..
               "name a surface to compare the same one across forces",
    }
  end

  local rows = {}
  for _, force in ipairs(forces) do
    local count = 0
    for _, surface in ipairs(surfaces) do
      count = count + surface.count_entities_filtered{ force = force.name, name = a.name }
    end
    rows[#rows + 1] = { force = force.name, count = count }
  end
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.force < y.force
  end)

  local shown = bounded.cut(rows, bounded.MAX_FORCES)
  return {
    found = true, name = a.name, surface = label, surfaces_counted = #surfaces,
    total = #rows, shown = #shown, forces = shown,
  }
end

local function entity_count(a)
  a = a or {}
  if a.all == true then return entity_count_all(a) end
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
