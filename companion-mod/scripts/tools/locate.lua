-- AI Agent Bridge - scripts/tools/locate.lua
-- Author: bits-orio
-- License: MIT
--
-- Where things are: find_entities, one of the two reads behind a "where"
-- question (locate_player.lua is the other), answering with positions the
-- model writes as [gps=x,y,surface] so a player can click the ping.
--
-- It walks exactly one surface, never the game. find_entities asks the
-- engine for one force's entities of one name or type on one surface,
-- capped at SCAN_CAP so a base of a hundred thousand machines costs one
-- bounded pass, then keeps the ones on the recipe asked for. The system
-- prompt tells the model to ask the player which surface when the question
-- does not say and the force has several.
--
-- API names verified against the 2.0.77 docs:
--   LuaSurface::find_entities_filtered(filter) -> array[LuaEntity];
--     EntitySearchFilters: name, type, force, limit, all optional; with no
--     area, position or radius it covers the surface; position with radius
--     covers that circle and nothing else.
--   LuaEntity::get_recipe() -> LuaRecipe? (crafting machines), ::position,
--     ::name, ::type. LuaPlayer::position, ::surface, ::connected.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local bounded        = require("scripts.tools.bounded")
local name_lookup    = require("scripts.tools.name_lookup")
local entity_view    = require("scripts.tools.entity_view")

local is_ghost, real_name, real_type, gps = entity_view.is_ghost, entity_view.real_name, entity_view.real_type, entity_view.gps

local SCAN_CAP      = 2000
local DEFAULT_SHOWN = 5
local MAX_SHOWN     = 10

-- Entity types that have a recipe to filter on, as the list the engine filter
-- takes and as the set recipe_of tests against. One source, two shapes.
-- EntitySearchFilters::type and ::ghost_type each take a string or an array of
-- strings (verified against the 2.0.77 docs).
local CRAFTER_TYPES = { "assembling-machine", "furnace", "rocket-silo" }
local CRAFTERS = {}
for _, crafter in ipairs(CRAFTER_TYPES) do CRAFTERS[crafter] = true end

--- The recipe set on a crafting machine or on the ghost of one, or nil.
--- pcall'd: a ghost of something the engine will not answer for must read
--- as "no recipe", never as an error that ends the whole search.
local function recipe_of(entity)
  if not CRAFTERS[real_type(entity)] then return nil end
  local ok, recipe = pcall(entity.get_recipe)
  if ok and recipe and recipe.valid ~= false then return recipe end
  return nil
end

--- The recipe an idle furnace last ran, as {name}, or nil. A furnace picks
--- its recipe from what is put in it and drops it when it runs dry, so a
--- recipe search missed every furnace that had stopped. LuaEntity::
--- previous_recipe (Furnace only, RecipeIDAndQualityIDPair, verified in
--- runtime-api.json 2.1.23); its name is a RecipeID, read as a string or a
--- prototype. Measured on a live 2.1.24 save, 837 idle furnaces: a plain
--- table whose name is a LuaRecipePrototype and quality a
--- LuaQualityPrototype, both userdata, or nil on a furnace that never ran.
--- pcall'd for the same reason as recipe_of.
local function previous_recipe_of(entity)
  if is_ghost(entity) or entity.type ~= "furnace" then return nil end
  local ok, previous = pcall(function() return entity.previous_recipe end)
  if not (ok and previous) then return nil end
  local name = previous.name
  if type(name) ~= "string" then name = name and name.name end
  return name and { name = name } or nil
end

--- The recipe a machine runs, or the one an idle furnace last ran, and
--- whether it is the idle case.
local function working_recipe(entity)
  local set = recipe_of(entity)
  if set then return set, false end
  local previous = previous_recipe_of(entity)
  return previous, previous ~= nil
end

local M = {}

M.manifest = {
  find_entities = {
    desc = "Where one force's entities are on one surface, with ready [gps=...] tags. Filter by name or names, type (assembling-machine, furnace, mining-drill, lab), the recipe a crafting machine is set to (an idle furnace by its last one, idle=true) or the item or fluid its recipe makes (product=grenade); a recipe or product search covers every crafting machine by itself. Ghosts match by what they will become (ghost=true rows); ghost=true or false narrows to them or to built. truncated=true: the scan stopped early, so nothing is proven absent. A single close name is used, resolved_from says so; unknown: found=false with suggestions. For a count use entity_count.",
    tier = "2",
    params = {
      surface = "string! surface name or index, e.g. nauvis",
      name    = "string entity prototype name, e.g. lab, assembling-machine-2",
      names   = "list<string> several entity prototype names at once, e.g. [\"stone-furnace\", \"steel-furnace\"]",
      type    = "string entity type, e.g. assembling-machine, furnace, mining-drill",
      recipe  = "string recipe name a crafting machine is set to, e.g. repair-pack",
      product = "string item or fluid name a crafting machine's recipe makes, e.g. military-science-pack",
      ghost   = "boolean true for ghosts only, false for built entities only; omit for both",
      limit   = "integer positions to return, default " .. DEFAULT_SHOWN .. ", max " .. MAX_SHOWN,
    },
  },
}

--- Whether a recipe makes `product`, by product name.
local function makes(recipe, product)
  for _, p in ipairs(recipe.products or {}) do
    if p.name == product then return true end
  end
  return false
end

local function optional_string(v, what)
  if v == nil then return nil end
  if type(v) ~= "string" or v == "" then error(what .. " must be a non-empty string", 0) end
  return v
end

local MISS_REASONS = {
  name    = "no entity prototype by that name: use the internal name, for example assembling-machine-2",
  recipe  = "no recipe by that name: use the internal name, for example repair-pack",
  product = "no item or fluid by that name: use the internal name, for example military-science-pack",
}
local LOOKUP_KIND = { name = "entity", recipe = "recipe", product = "goods" }

--- The filters asked for, names resolved (scripts/tools/name_lookup.lua):
--- an exact name as it is, a single close match in its place with
--- resolved_from saying so, or nil and the found=false reply with
--- suggestions, products among them fluids.
local function filters_of(a, base)
  local f = { type = optional_string(a.type, "type"), ghost = a.ghost }
  if f.ghost ~= nil and type(f.ghost) ~= "boolean" then error("ghost must be true or false", 0) end
  for field in pairs(LOOKUP_KIND) do
    local asked = optional_string(a[field], field)
    if asked then
      local used, extra = name_lookup.resolve(asked, LOOKUP_KIND[field])
      if not used then
        base[field], base.reason, base.suggestions = asked, MISS_REASONS[field], extra
        return nil, base
      end
      f[field], f.resolved_from = used, extra or f.resolved_from
    end
  end
  if a.names ~= nil then
    local list = type(a.names) == "table" and a.names or { a.names }
    local used, _, missing = name_lookup.resolve_all(list, "entity")
    if missing then
      local first = next(missing)
      base.name, base.reason, base.suggestions = first, MISS_REASONS.name, missing[first]
      return nil, base
    end
    f.names = used
  end
  return f
end

--- What the engine filter's name takes: one name, the list, or nil.
local function name_filter(f)
  if f.names then
    local list = {}
    for _, one in ipairs(f.names) do list[#list + 1] = one end
    if f.name then list[#list + 1] = f.name end
    return list
  end
  return f.name
end

--- The entities one force has on `surface` matching the name or type
--- filters, ghosts included, capped at SCAN_CAP. Built entities and ghosts
--- are two engine passes, because a name or type filter matches the ghost
--- shell ("entity-ghost") rather than what it will become; ghost_name and
--- ghost_type are the ghost-side twins. With no name or type, one pass over
--- everything covers both.
---
--- A recipe or product filter can only ever match a crafting machine, so a
--- search that names neither an entity nor a type asks the engine for the
--- three crafter types rather than for everything. An unfiltered pass spent
--- the whole SCAN_CAP on belts, inserters and poles: measured on a live
--- server, product=firearm-magazine scanned 2000 entities and found none of
--- the six assembling machines set to it, where the crafter types scanned
--- 335 and found all six.
local function scan_entities(surface, force, f)
  local scanned = {}
  local function scan(filter)
    filter.force, filter.limit = force.name, SCAN_CAP - #scanned
    if filter.limit <= 0 then return end
    for _, entity in ipairs(surface.find_entities_filtered(filter)) do scanned[#scanned + 1] = entity end
  end
  local names = name_filter(f)
  local types = f.type
  if not (names or types) and (f.recipe or f.product) then types = CRAFTER_TYPES end
  if names or types then
    if f.ghost ~= true then scan({ name = names, type = types }) end
    if f.ghost ~= false then scan({ ghost_name = names, ghost_type = types }) end
  else
    scan({})
  end
  return scanned
end

--- Whether one scanned entity passes the ghost, recipe and product filters,
--- and whether it matched through an idle furnace's previous recipe.
local function keeps(entity, f)
  if f.ghost ~= nil and is_ghost(entity) ~= f.ghost then return false end
  if not (f.recipe or f.product) then return true end
  local set, idle = working_recipe(entity)
  if not set then return false end
  local ok = (not f.recipe or set.name == f.recipe)
    and (not f.product or makes(prototypes.recipe[set.name] or set, f.product))
  return ok, idle
end

local function entity_row(entity, surface, idle)
  local x, y, tag = gps(entity.position, surface.name)
  local row = { name = real_name(entity), x = x, y = y, gps = tag }
  if is_ghost(entity) then row.ghost = true end
  local set, was_idle = working_recipe(entity)
  if set then row.recipe = set.name end
  if idle or was_idle then row.idle = true end
  return row
end

local function find_entities(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local f, refused = filters_of(a, { found = false, force = force.name, surface = surface.name })
  if not f then return refused end
  if not (f.name or f.names or f.type or f.recipe or f.product) then
    error("give at least one of name, names, type, recipe or product", 0)
  end
  local scanned = scan_entities(surface, force, f)
  local matches = {}
  for _, entity in ipairs(scanned) do
    local ok, idle = keeps(entity, f)
    if ok then matches[#matches + 1] = { entity = entity, idle = idle } end
  end
  local rows = {}
  for i, match in ipairs(bounded.cut(matches, bounded.limit(a.limit, DEFAULT_SHOWN, MAX_SHOWN))) do
    rows[i] = entity_row(match.entity, surface, match.idle)
  end
  return {
    found = true, force = force.name, surface = surface.name,
    name = f.name, names = f.names, type = f.type, recipe = f.recipe, product = f.product, ghost = f.ghost,
    resolved_from = f.resolved_from,
    scanned = #scanned, truncated = #scanned >= SCAN_CAP,
    total = #matches, shown = #rows, entities = rows,
  }
end

M.functions = { find_entities = find_entities }

return M
