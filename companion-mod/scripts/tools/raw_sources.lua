-- AI Agent Bridge - scripts/tools/raw_sources.lua
-- Author: bits-orio
-- License: MIT
--
-- What the game hands out without a recipe on the surface a question is
-- about: every product a resource there gives when it is mined (ores, coal,
-- stone, crude oil) and every fluid a tile there gives an offshore pump
-- (water). recipe_pick keeps these raw even once a recipe can make them,
-- which Space Age adds: asteroid crushing makes iron ore, ice melting water,
-- coal synthesis coal, scrap recycling stone. A walk that followed those put
-- a force mining iron ore on Nauvis to crushing asteroids for it.
--
-- Which surface matters. Fulgora's oil ocean gives heavy oil, so a rule
-- that asked the whole game called heavy oil mined for every force on every
-- planet, and lubricant on Nauvis lost its crude oil with approx empty. So
-- a surface hands out only what its own map generator places:
--   * a space platform mines and pumps nothing;
--   * otherwise the resources named in its map_gen_settings
--     autoplace_settings.entity and the fluid tiles named in
--     autoplace_settings.tile; when it lists no entity and no tile at all,
--     the same read of its planet's prototype;
--   * when neither says, or no surface was given, every resource and tile in
--     the game, as before, and `anywhere` says so: recipe_walk then adds
--     any_planet to approx when a recipe could make such a raw row, since
--     it may really be crafted where the player is, and that recipe stays
--     in alternatives.
-- Measured on the live 2.0.77 save, 17 surfaces, and on a fresh 2.1.17 map,
-- after generating each Space Age planet: every resource and fluid tile
-- really there was one those two lists name, and nothing they leave out
-- appeared, though every planet sets treat_missing_as_default. So that flag
-- is not read. Nor is autoplace_controls: nothing links a resource to its
-- control (autoplace_specification.control is nil for every resource but
-- scrap; Vulcanus calls tungsten-ore's control tungsten_ore). A team's
-- Nauvis copy is its own planet with Nauvis' lists; MTS's landing pen, a
-- script surface, lists nothing and falls back; a platform lists one tile,
-- empty-space, and no entity. No planet, resource or fluid name is written
-- here: the lists are read.
--
-- Read once per recipe_needs call and never kept: prototypes are the same
-- on every peer, and so is a surface's map_gen_settings.
--
-- API names verified against runtime-api.json 2.0.77 and 2.1.17:
--   LuaPrototypes::get_entity_filtered(filters) with EntityPrototypeFilter
--     "type" = "resource"; LuaPrototypes::tile, LuaCustomTable[string ->
--     LuaTilePrototype], walked with pairs.
--   LuaEntityPrototype::mineable_properties, MineableProperties: minable
--     (boolean), products (array[Product], optional).
--   LuaTilePrototype::fluid, optional LuaFluidPrototype: "The fluid offshore
--     pump produces on this tile, if any."
--   LuaSurface::map_gen_settings (MapGenSettings), ::platform (optional
--     LuaSpacePlatform), ::planet (optional LuaPlanet); LuaPlanet::prototype
--     (LuaSpaceLocationPrototype), whose map_gen_settings is optional.
--   MapGenSettings::autoplace_settings, dictionary["entity" | "tile" |
--     "decorative" -> AutoplaceSettings]; AutoplaceSettings::settings,
--     dictionary[string -> AutoplaceControl], keyed by prototype name.

local yield = require("scripts.tools.recipe_yield")

local M = {}

--- What mining one resource gives: its item and fluid products, if minable.
local function add_mined(set, proto)
  local mining = proto.mineable_properties
  if not (mining and mining.minable) then return end
  for _, product in ipairs(mining.products or {}) do
    if product.type == "item" or product.type == "fluid" then
      set[yield.key(product.type, product.name)] = true
    end
  end
end

--- The fluid an offshore pump draws on one tile, if any.
local function add_pumped(set, tile)
  local fluid = tile and tile.fluid
  if fluid then set[yield.key("fluid", fluid.name)] = true end
end

--- Every item and fluid mined or pumped on any planet. `resources` is every
--- resource prototype, by name.
local function anywhere(resources)
  local set = {}
  for _, proto in pairs(resources) do add_mined(set, proto) end
  for _, tile in pairs(prototypes.tile) do add_pumped(set, tile) end
  return set
end

--- The prototype names one map generator places of one kind, as a set.
local function placed(map_gen, kind)
  local settings = map_gen and map_gen.autoplace_settings and map_gen.autoplace_settings[kind]
  return settings and settings.settings or {}
end

--- What one map generator places that is mined or pumped, or nil when it
--- lists no entity and no tile at all. One that lists some places nothing
--- it leaves out, resources and fluid tiles included.
local function from_map_gen(map_gen, resources)
  local entities, tiles = placed(map_gen, "entity"), placed(map_gen, "tile")
  if next(entities) == nil and next(tiles) == nil then return nil end
  local set = {}
  for name, proto in pairs(resources) do
    if entities[name] then add_mined(set, proto) end
  end
  for name in pairs(tiles) do add_pumped(set, prototypes.tile[name]) end
  return set
end

--- What a surface that is not a platform hands out, from its own map
--- generator or else its planet's, or nil when neither says.
local function on_planet(surface, resources)
  local set = from_map_gen(surface.map_gen_settings, resources)
  if set then return set end
  local planet = surface.planet
  return planet and from_map_gen(planet.prototype.map_gen_settings, resources) or nil
end

--- The mined rule for one call, about `surface` (a LuaSurface, or nil for
--- none). rule.mined(kind, name): is this raw because the game hands it out
--- there, with no recipe named after it? Sulfuric acid comes out of a geyser
--- and still keeps the recipe named after it. rule.anywhere(): was that
--- answered for any planet, because no surface said? The read happens on the
--- first question that needs it, once, and a platform needs no prototype.
function M.rule(surface)
  local set, said
  local function read()
    if surface and surface.platform then
      set, said = {}, true
      return
    end
    local resources = prototypes.get_entity_filtered({ { filter = "type", type = "resource" } })
    set = surface and on_planet(surface, resources)
    said = set ~= nil
    set = set or anywhere(resources)
  end
  local rule = {}
  function rule.mined(kind, name)
    if prototypes.recipe[name] then return false end
    if not set then read() end
    return set[yield.key(kind, name)] == true
  end
  function rule.anywhere()
    if not set then read() end
    return not said
  end
  return rule
end

return M
