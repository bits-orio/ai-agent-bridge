-- AI Agent Bridge - scripts/tools/recipe_rank.lua
-- Author: bits-orio
-- License: MIT
--
-- Every recipe one force could use to make an item or fluid, ranked: what
-- recipe_pick chooses from. recipe_pick then drops round trips, sets dead
-- ends below the rest and breaks ties; this file only asks the engine and
-- orders what it says.
--
-- Candidates are the recipes the engine says produce the item, narrowed to
-- its one name. A candidate stays when the force has it enabled, the
-- prototype is neither hidden nor a parameter, and it nets more of the item
-- than it takes back as an ingredient, which handles a catalyst like kovarex
-- without naming it. The engine's filter drops hidden recipes itself.
--
-- Ranking, first rule wins: the recipe named after the item, then a recipe
-- whose main_product is the item, then the largest net output, then by name.
-- Net output is reckoned at the force's research bonus alone: the machine a
-- recipe will run on is chosen after the pick (recipe_machine.lua), so its
-- base productivity changes the figures, never the ranking.
--
-- `locked` reports a recipe named after the item that passes everything but
-- `enabled`: the force could make the item once it researches it. Only the
-- named recipe counts, so ore that a Space Age asteroid recipe could make, or
-- water a barrel could, is not called locked.
--
-- What it costs, measured on a live 2.0.77 Space Age save of 712 recipes. A
-- filtered LuaCustomTable is evaluated as it is walked: steel plate has 81
-- makers, 78 of them hidden recycling, and walking them all took 137 us
-- where the 3 that are not hidden took 55. So the engine drops hidden
-- recipes itself, with "hidden" inverted ahead of the product filter, which
-- is the cheaper order (about 30 us for one name, against 60 the other way
-- round). And one filter answers for many items at once: ten names cost
-- about what one does, eighty about what three do, which is why rank() takes
-- a list. Reading a recipe costs too, its products 2 to 17 us and its
-- ingredients about 9, so each recipe is read once per call and kept for it,
-- and one the force has not enabled is read no further unless it is named
-- after an item asked for, the only way it can matter.
--
-- API names verified against runtime-api.json 2.0.77 and 2.1.17:
--   LuaPrototypes::get_recipe_filtered(filters) -> LuaCustomTable[string ->
--     LuaRecipePrototype], keyed by recipe name. RecipePrototypeFilter
--     "hidden" (with invert), and "has-product-item" / "has-product-fluid"
--     with elem_filters, an array of ItemPrototypeFilter or
--     FluidPrototypeFilter, here one {filter = "name", name = <list of
--     names>}. mode joins a filter to the one before; "and" binds tighter
--     than "or". Measured on 2.0.77: an empty filter list matches every
--     recipe, so makers is never asked with no items.
--   LuaPrototypeBase::hidden and ::parameter, in both versions. Not
--     LuaRecipePrototype::is_parameter, which 2.1 removes.
--   LuaRecipePrototype::main_product (Product, optional).
--   LuaForce::recipes -> LuaCustomTable[string -> LuaRecipe]; LuaRecipe::enabled.
-- The filter returns a LuaCustomTable: walked with pairs, never next(), which
-- does not work on one.

local yield = require("scripts.tools.recipe_yield")

local FILTER = { item = "has-product-item", fluid = "has-product-fluid" }
local KINDS = { "item", "fluid" }

local M = {}

--- Every recipe that is not hidden and makes any of `items`, in one engine
--- filter: (not hidden and makes one of these items) or (not hidden and
--- makes one of these fluids).
local function makers(items)
  local names = { item = {}, fluid = {} }
  for _, item in ipairs(items) do
    local list = names[item.kind]
    list[#list + 1] = item.name
  end
  local filters = {}
  for _, kind in ipairs(KINDS) do
    if #names[kind] > 0 then
      filters[#filters + 1] = { filter = "hidden", invert = true, mode = "or" }
      filters[#filters + 1] = { filter = FILTER[kind], mode = "and",
                                elem_filters = { { filter = "name", name = names[kind] } } }
    end
  end
  return prototypes.get_recipe_filtered(filters)
end

--- What this call knows of one recipe: its prototype, the force's LuaRecipe
--- and whether it is enabled, each read from the engine once.
local function known_of(memo, name, proto)
  local known = memo.known[name]
  if not known then
    local recipe = memo.recipes[name]
    known = { name = name, proto = proto, recipe = recipe, enabled = recipe ~= nil and recipe.enabled }
    memo.known[name] = known
  end
  return known
end

--- What one craft of a known recipe nets at research productivity, worked
--- out once a call.
local function io_of(known)
  if not known.io then known.io = yield.net(known.proto, yield.bonus(known.recipe, known.proto)) end
  return known.io
end

local function main_is(known, item)
  if known.main == nil then known.main = known.proto.main_product or false end
  local main = known.main
  return main ~= false and main.type == item.kind and main.name == item.name
end

--- The ranking above, as a sort order.
function M.better(a, b)
  if a.named ~= b.named then return a.named end
  if a.main ~= b.main then return a.main end
  if a.net ~= b.net then return a.net > b.net end
  return a.name < b.name
end

--- Files one recipe under one item it nets above zero: a candidate when the
--- force has it enabled, the item locked when it is the named recipe.
local function file(memo, known, item, net)
  local rank = memo.ranks[item.key]
  if known.enabled then
    rank.usable[#rank.usable + 1] = { name = known.name, proto = known.proto, recipe = known.recipe, io = known.io,
                                      net = net, named = known.name == item.name, main = main_is(known, item) }
  elseif known.name == item.name then
    rank.locked = true
  end
end

--- One recipe the filter returned, filed under every item asked for that it
--- nets. One the force has not enabled matters only when it is named after
--- one of them.
local function consider(memo, name, proto, wanted, named)
  local known = known_of(memo, name, proto)
  if not (known.enabled or named[name]) then return end
  if proto.parameter then return end
  for key, net in pairs(io_of(known)) do
    local item = wanted[key]
    if item and net > 0 then file(memo, known, item, net) end
  end
end

--- Ranks every recipe that makes each of `items` ({kind, name, key}, at
--- least one, none ranked yet) into memo.ranks[key] = {usable, best first;
--- locked}, in one engine filter.
function M.rank(memo, items)
  local wanted, named = {}, {}
  for _, item in ipairs(items) do
    wanted[item.key], named[item.name] = item, true
    memo.ranks[item.key] = { usable = {}, locked = false }
  end
  for name, proto in pairs(makers(items)) do consider(memo, name, proto, wanted, named) end
  for _, item in ipairs(items) do table.sort(memo.ranks[item.key].usable, M.better) end
end

--- The usable recipes for one item, best first, and whether it is locked.
function M.ranked(memo, kind, name)
  local key = yield.key(kind, name)
  if not memo.ranks[key] then M.rank(memo, { { kind = kind, name = name, key = key } }) end
  return memo.ranks[key].usable, memo.ranks[key].locked
end

--- `list` ({kind, name}) as items to rank: keyed, once each, none ranked yet.
function M.unranked(memo, list)
  local out, seen = {}, {}
  for _, one in ipairs(list) do
    local key = yield.key(one.kind, one.name)
    if not (memo.ranks[key] or seen[key]) then
      seen[key] = true
      out[#out + 1] = { kind = one.kind, name = one.name, key = key }
    end
  end
  return out
end

--- Ranks every item of `list` not ranked yet, in one engine filter, or none
--- when there is nothing new to ask about.
function M.rank_all(memo, list)
  local fresh = M.unranked(memo, list)
  if #fresh > 0 then M.rank(memo, fresh) end
end

--- A memo for one force and one call: what rank() fills and what
--- recipe_pick keeps beside it. Nothing outlives the call.
function M.memo(force)
  return { recipes = force.recipes, known = {}, ranks = {} }
end

return M
