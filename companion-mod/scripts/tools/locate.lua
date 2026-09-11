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
--     area, position or radius it covers the surface.
--   LuaEntity::get_recipe() -> LuaRecipe? (crafting machines), ::position,
--     ::name, ::type. LuaPlayer::position, ::surface, ::connected.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local bounded        = require("scripts.tools.bounded")

local SCAN_CAP      = 2000
local DEFAULT_SHOWN = 5
local MAX_SHOWN     = 10

-- Entity types that have a recipe to filter on.
local CRAFTERS = { ["assembling-machine"] = true, furnace = true, ["rocket-silo"] = true }

local M = {}

M.manifest = {
  find_entities = {
    desc = "Where one force's entities are on one surface: positions with a ready [gps=...] tag. Filter by entity name, type (assembling-machine, furnace, mining-drill, lab, roboport, rocket-silo), the recipe a crafting machine is set to, or the item or fluid its recipe makes (where are grenades made: product=grenade). Give at least one filter. An unknown name comes back found=false with suggestions of close names.",
    params = {
      surface = "string! surface name or index, e.g. nauvis",
      name    = "string entity prototype name, e.g. lab, assembling-machine-2",
      type    = "string entity type, e.g. assembling-machine, furnace, mining-drill",
      recipe  = "string recipe name a crafting machine is set to, e.g. repair-pack",
      product = "string item or fluid name a crafting machine's recipe makes, e.g. military-science-pack",
      limit   = "integer positions to return, default " .. DEFAULT_SHOWN .. ", max " .. MAX_SHOWN,
    },
  },
  locate_player = {
    desc = "Where one player's character is: surface and position with a ready [gps=...] tag, whether they are connected, and the surface they are looking at when it differs (remote view). Unknown player: found=false.",
    params = {
      player = "string! player name",
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

  local filter = { force = force.name, limit = SCAN_CAP }
  if name then filter.name = name end
  if etype then filter.type = etype end
  local scanned = surface.find_entities_filtered(filter)

  local matches = {}
  for _, entity in ipairs(scanned) do
    local keep = true
    if recipe or product then
      local set = CRAFTERS[entity.type] and entity.get_recipe() or nil
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
    local row = { name = entity.name, x = x, y = y, gps = tag }
    if recipe or product then row.recipe = entity.get_recipe().name end
    rows[i] = row
  end
  return {
    found = true, force = force.name, surface = surface.name,
    name = name, type = etype, recipe = recipe, product = product,
    scanned = #scanned, truncated = #scanned >= SCAN_CAP,
    total = #matches, shown = #rows, entities = rows,
  }
end

local function locate_player(a)
  if type(a.player) ~= "string" or a.player == "" then error("player is required", 0) end
  local player = game.get_player(a.player)
  if not (player and player.valid) then
    return { found = false, player = a.player, reason = "no player by that name" }
  end
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
  return out
end

M.functions = { find_entities = find_entities, locate_player = locate_player }

return M
