-- AI Agent Bridge - scripts/tools/locate.lua
-- Author: bits-orio
-- License: MIT
--
-- Where things are: find_entities and locate_player, the two reads behind a
-- "where" question, each answering with positions the model writes as
-- [gps=x,y,surface] so a player can click the ping.
--
-- Both walk exactly one surface, never the game. find_entities asks the
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

local SCAN_CAP      = 2000
local DEFAULT_SHOWN = 5
local MAX_SHOWN     = 10

-- nearby: the circle locate_player reads around a character. 32 tiles is a
-- generous "next to", and a radius read of a circle that size is bounded by
-- its area whatever the surface holds.
local NEARBY_MAX   = 32
local NEARBY_SHOWN = 5

-- Entity types that have a recipe to filter on, as the list the engine filter
-- takes and as the set recipe_of tests against. One source, two shapes.
-- EntitySearchFilters::type and ::ghost_type each take a string or an array of
-- strings (verified against the 2.0.77 docs).
local CRAFTER_TYPES = { "assembling-machine", "furnace", "rocket-silo" }
local CRAFTERS = {}
for _, crafter in ipairs(CRAFTER_TYPES) do CRAFTERS[crafter] = true end

--- A ghost is an entity of type entity-ghost whose real prototype sits in
--- ghost_name and ghost_type; everything below asks these two so a ghost
--- and a built machine read the same way.
local function is_ghost(entity) return entity.type == "entity-ghost" end
local function real_name(entity) return is_ghost(entity) and entity.ghost_name or entity.name end
local function real_type(entity) return is_ghost(entity) and entity.ghost_type or entity.type end

--- The recipe set on a crafting machine or on the ghost of one, or nil.
--- pcall'd: a ghost of something the engine will not answer for must read
--- as "no recipe", never as an error that ends the whole search.
local function recipe_of(entity)
  if not CRAFTERS[real_type(entity)] then return nil end
  local ok, recipe = pcall(entity.get_recipe)
  if ok and recipe and recipe.valid ~= false then return recipe end
  return nil
end

local M = {}

M.manifest = {
  find_entities = {
    desc = "Where one force's entities are on one surface: positions with a ready [gps=...] tag. Filter by entity name, type (assembling-machine, furnace, mining-drill, lab, roboport, rocket-silo), the recipe a crafting machine is set to, or the item or fluid its recipe makes (where are grenades made: product=grenade). A recipe or product search covers every crafting machine on the surface by itself; a type beside it is not needed. Ghosts match too, by what they will become, and rows say ghost=true; ghost=true or false narrows to ghosts or built. Give at least one filter. truncated=true means the scan stopped before the end of the surface, so the rows are what it saw and nothing in the reply says a thing is absent. An unknown name comes back found=false with suggestions of close names. For a count, entity_count is cheaper: it asks the engine for a number instead of walking positions.",
    tier = "2",
    params = {
      surface = "string! surface name or index, e.g. nauvis",
      name    = "string entity prototype name, e.g. lab, assembling-machine-2",
      type    = "string entity type, e.g. assembling-machine, furnace, mining-drill",
      recipe  = "string recipe name a crafting machine is set to, e.g. repair-pack",
      product = "string item or fluid name a crafting machine's recipe makes, e.g. military-science-pack",
      ghost   = "boolean true for ghosts only, false for built entities only; omit for both",
      limit   = "integer positions to return, default " .. DEFAULT_SHOWN .. ", max " .. MAX_SHOWN,
    },
  },
  locate_player = {
    desc = "Where one player's character is: surface and position with a ready [gps=...] tag, whether they are connected, and the surface they are looking at when it differs (remote view). nearby=N adds the closest built things within N tiles of the character, nearest first, each with its distance and gps (what is Bob standing next to): one read of one small circle, never a walk over the surface. all=true answers for every connected player in one call, one row each, so a per-player question is one lookup however many are online. Unknown player: found=false.",
    params = {
      player = "string player name; required unless all=true",
      all    = "boolean every connected player in one call; player is ignored",
      nearby = "integer tiles, 1 to " .. NEARBY_MAX .. ": list the built things within this radius of the character",
    },
  },
}

-- Up to five prototype names sharing a fragment of three or more letters
-- with what was asked for, so "repair-kit" answers with repair-pack. One
-- pass over one prototype table, only on the miss.
local MAX_SUGGESTIONS = 5

local function suggestions(table_, query)
  local out, seen = {}, {}
  for fragment in tostring(query):lower():gmatch("[%l%d]+") do
    if #fragment >= 3 then
      for name in pairs(table_) do
        if not seen[name] and name:find(fragment, 1, true) then
          seen[name] = true
          out[#out + 1] = name
          if #out >= MAX_SUGGESTIONS then return out end
        end
      end
    end
  end
  table.sort(out)
  return out
end

--- Whether a recipe makes `product`, by product name.
local function makes(recipe, product)
  for _, p in ipairs(recipe.products or {}) do
    if p.name == product then return true end
  end
  return false
end

local function gps(position, surface_name)
  local x, y = math.floor(position.x + 0.5), math.floor(position.y + 0.5)
  return x, y, string.format("[gps=%d,%d,%s]", x, y, surface_name)
end

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

local function optional_string(v, what)
  if v == nil then return nil end
  if type(v) ~= "string" or v == "" then error(what .. " must be a non-empty string", 0) end
  return v
end

local function find_entities(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local name    = optional_string(a.name, "name")
  local etype   = optional_string(a.type, "type")
  local recipe  = optional_string(a.recipe, "recipe")
  local product = optional_string(a.product, "product")
  local ghost   = a.ghost
  if ghost ~= nil and type(ghost) ~= "boolean" then error("ghost must be true or false", 0) end
  if not (name or etype or recipe or product) then
    error("give at least one of name, type, recipe or product", 0)
  end
  if name and not prototypes.entity[name] then
    return { found = false, force = force.name, surface = surface.name, name = name,
             reason = "no entity prototype by that name: use the internal name, for example assembling-machine-2",
             suggestions = suggestions(prototypes.entity, name) }
  end
  if recipe and not prototypes.recipe[recipe] then
    return { found = false, force = force.name, surface = surface.name, recipe = recipe,
             reason = "no recipe by that name: use the internal name, for example repair-pack",
             suggestions = suggestions(prototypes.recipe, recipe) }
  end
  if product and not (prototypes.item[product] or prototypes.fluid[product]) then
    return { found = false, force = force.name, surface = surface.name, product = product,
             reason = "no item or fluid by that name: use the internal name, for example military-science-pack",
             suggestions = suggestions(prototypes.item, product) }
  end

  -- Built entities and ghosts are two engine passes, because a name or type
  -- filter matches the ghost shell ("entity-ghost") rather than what it will
  -- become; ghost_name and ghost_type are the ghost-side twins. With no name
  -- or type, one pass over everything covers both.
  local scanned = {}
  local function scan(filter)
    filter.force, filter.limit = force.name, SCAN_CAP - #scanned
    if filter.limit <= 0 then return end
    for _, entity in ipairs(surface.find_entities_filtered(filter)) do scanned[#scanned + 1] = entity end
  end
  -- A recipe or product filter can only ever match a crafting machine, so a
  -- search that names neither an entity nor a type asks the engine for the
  -- three crafter types rather than for everything. An unfiltered pass spends
  -- the whole SCAN_CAP on belts, inserters and poles and then reports zero
  -- matches on a surface that has the machine: measured on a live server,
  -- product=firearm-magazine scanned 2000 entities and found none of the six
  -- assembling machines set to it, where the same search over the crafter
  -- types scanned 335 and found all six.
  local types = etype
  if not (name or etype) and (recipe or product) then types = CRAFTER_TYPES end

  if name or types then
    if ghost ~= true then scan({ name = name, type = types }) end
    if ghost ~= false then scan({ ghost_name = name, ghost_type = types }) end
  else
    scan({})
  end

  local matches = {}
  for _, entity in ipairs(scanned) do
    local keep = ghost == nil or is_ghost(entity) == ghost
    if keep and (recipe or product) then
      local set = recipe_of(entity)
      keep = set ~= nil
        and (not recipe or set.name == recipe)
        and (not product or makes(prototypes.recipe[set.name] or set, product))
    end
    if keep then matches[#matches + 1] = entity end
  end

  local shown = bounded.cut(matches, bounded.limit(a.limit, DEFAULT_SHOWN, MAX_SHOWN))
  local rows = {}
  for i, entity in ipairs(shown) do
    local x, y, tag = gps(entity.position, surface.name)
    local row = { name = real_name(entity), x = x, y = y, gps = tag }
    if is_ghost(entity) then row.ghost = true end
    local set = recipe_of(entity)
    if set then row.recipe = set.name end
    rows[i] = row
  end
  return {
    found = true, force = force.name, surface = surface.name,
    name = name, type = etype, recipe = recipe, product = product, ghost = ghost,
    scanned = #scanned, truncated = #scanned >= SCAN_CAP,
    total = #matches, shown = #rows, entities = rows,
  }
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
    local shown = bounded.fit(bounded.cut(rows, bounded.MAX_FORCES))
    return { found = true, total = #rows, shown = #shown, players = shown }
  end
  if type(a.player) ~= "string" or a.player == "" then error("player is required unless all=true", 0) end
  local player = game.get_player(a.player)
  if not (player and player.valid) then
    return { found = false, player = a.player, reason = "no player by that name" }
  end
  return locate_one(player, nearby)
end

M.functions = { find_entities = find_entities, locate_player = locate_player }

return M
