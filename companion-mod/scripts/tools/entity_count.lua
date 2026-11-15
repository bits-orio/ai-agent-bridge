-- AI Agent Bridge - scripts/tools/entity_count.lua
-- Author: bits-orio
-- License: MIT
--
-- entity_count: how many of one entity a force owns on one surface. The count
-- happens inside the engine, over the whole surface, so this is one C++ pass and
-- not a Lua walk over entities however large the base is. `names` and `type`
-- count several prototypes in one call: "which team has the most furnaces"
-- was one call per furnace type. The all=true paths live in
-- scripts/tools/entity_count_all.lua.
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

local force_lookup     = require("scripts.tools.force_lookup")
local surface_lookup   = require("scripts.tools.surface_lookup")
local name_lookup      = require("scripts.tools.name_lookup")
local entity_count_all = require("scripts.tools.entity_count_all")

local MAX_NAMES = 8
local MAX_TYPE_NAMES = 25
local NO_NAME = "no entity prototype by that name: use the internal name, for example assembling-machine-2"

local M = {}

M.manifest = {
  entity_count = {
    desc = "How many entities one force has on one surface (how many labs do we have): by name, names (up to " .. MAX_NAMES .. ") or a prototype type such as furnace, per name and in total. all=true: one row per force with players, largest first; leave surface out there for each force's total everywhere, an O(1) engine counter, the right call for which-team-has-most. all=true with per_surface=true: one row per force and surface that has any, platform rows with owner, location, state and gps, the ping at the ship's hub: put that gps in a where-is-it answer. Unknown name or surface: found=false.",
    params = {
      surface     = "string surface name or index, e.g. nauvis; required unless all=true, where omitting it counts every surface",
      name        = "string entity prototype name, e.g. lab; give name, names or type",
      names       = "list<string> up to " .. MAX_NAMES .. " entity prototype names counted together, one row each",
      type        = "string entity prototype type, e.g. furnace, assembling-machine: every entity of that type",
      all         = "boolean default false: one row per force that has players; the force argument is ignored",
      per_surface = "boolean default false, only with all=true, surface left out and one name: one row per force-and-surface that has any",
      offset      = require("scripts.tools.bounded").OFFSET_PARAM,
    },
  },
}

--- Every non-hidden entity prototype of one type, by name, at most
--- MAX_TYPE_NAMES. LuaPrototypes::get_entity_filtered, EntityPrototypeFilter
--- "type" (runtime-api.json 2.1.23), returns a LuaCustomTable walked with
--- pairs.
local function names_of_type(entity_type)
  local out = {}
  for name, proto in pairs(prototypes.get_entity_filtered({ { filter = "type", type = entity_type } })) do
    if not proto.hidden then out[#out + 1] = name end
  end
  table.sort(out)
  for i = #out, MAX_TYPE_NAMES + 1, -1 do out[i] = nil end
  return out
end

--- What to count: the resolved names, whether it was one `name` alone, the
--- head of the reply, or nil and a found=false reply.
local function targets_of(a)
  if a.name == nil and a.names == nil and a.type == nil then error("name is required", 0) end
  local asked, head = {}, {}
  if a.name ~= nil then
    if type(a.name) ~= "string" or a.name == "" then error("name is required", 0) end
    asked[1] = a.name
  end
  local list = a.names == nil and {} or (type(a.names) == "table" and a.names or { a.names })
  if #list > MAX_NAMES then error("at most " .. MAX_NAMES .. " names in one call", 0) end
  for _, one in ipairs(list) do asked[#asked + 1] = one end
  local names, resolved, missing = name_lookup.resolve_all(asked, "entity")
  if missing then
    local first = next(missing)
    return nil, nil, nil, { found = false, name = first, reason = NO_NAME, suggestions = missing[first] }
  end
  if a.type ~= nil then
    local typed = names_of_type(a.type)
    if #typed == 0 then
      return nil, nil, nil, { found = false, type = a.type, reason = "no entity prototype of that type: types are such as furnace, assembling-machine, mining-drill" }
    end
    for _, one in ipairs(typed) do names[#names + 1] = one end
    head.type = a.type
  end
  local single = a.name ~= nil and #list == 0 and a.type == nil
  if single then head.name = names[1] else head.names = names end
  head.resolved_from = resolved and (single and resolved[1] or resolved) or nil
  return names, single, head
end

--- One force on one surface, several names: a row per name it has any of,
--- the O(1) counter deciding which are worth a pass.
local function count_many(force, surface, names, head)
  local rows, total = {}, 0
  for _, name in ipairs(names) do
    if force.get_entity_count(name) > 0 then
      local n = surface.count_entities_filtered{ force = force.name, name = name }
      total = total + n
      if n > 0 then rows[#rows + 1] = { name = name, count = n } end
    end
  end
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.name < y.name
  end)
  head.found, head.force, head.surface, head.count = true, force.name, surface.name, total
  head.names = rows
  return head
end

local function entity_count(a)
  a = a or {}
  if a.all == true then
    local names, _, head, refused = targets_of(a)
    if not names then return refused end
    return entity_count_all.run(a, names, head)
  end
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  -- Checked here rather than inside the filter: an unknown prototype name is
  -- the likeliest thing to be wrong about, and a clear found = false beats
  -- the engine's own error for the model's next round.
  local names, single, head, refused = targets_of(a)
  if not names then
    refused.force, refused.surface = force.name, surface.name
    return refused
  end
  if not single then return count_many(force, surface, names, head) end
  return {
    found = true,
    force = force.name,
    surface = surface.name,
    name = names[1],
    resolved_from = head.resolved_from,
    count = surface.count_entities_filtered{ force = force.name, name = names[1] },
  }
end

M.functions = { entity_count = entity_count }

return M
