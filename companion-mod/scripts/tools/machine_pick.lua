-- AI Agent Bridge - scripts/tools/machine_pick.lua
-- Author: bits-orio
-- License: MIT
--
-- Which crafting machine makes each recipe, which belt the rows are counted
-- in, and whether the force can build either.
--
-- The crafting machines and belts are read once per call. A machine can make
-- a recipe when some key of its crafting_categories passes
-- recipe.has_category(key). That way round on purpose: 2.1 removes
-- LuaRecipePrototype::category and ::additional_categories in favour of
-- `categories`, while has_category and crafting_categories exist in 2.0.77
-- and 2.1 alike.
--
-- A machine or a belt is one the force can build when some item in its
-- items_to_place_this has a recipe the force can use. The fastest of those
-- wins, by crafting speed at normal quality or by belt_speed, then by name.
-- When the force can build none, the fastest is taken anyway and marked
-- unlocked = false, so the answer still names a machine and says it is not
-- available yet. A `machine` the caller names wins for every recipe it can
-- make. Hidden and parameter prototypes are never offered: nobody builds one.
--
-- What it costs. has_category is under a microsecond a call (0.1 to 0.9 us
-- on 2.0.77), but a live Space Age save has 18 crafting machines over 30
-- categories, and asking every machine about every recipe adds up. So the
-- machines the force can build are asked first, fastest first, and the first
-- that makes the recipe is the answer without a slower one ever being asked.
-- Within a machine, the categories that already matched a recipe this call
-- are asked first: most recipes share a few. Neither changes which machine
-- wins, only how soon it is found; choosing for all 39 recipes of seven
-- science packs measured 0.4 ms. Whether the force can build each machine
-- and belt is a recipe pick of the item that places it; those items are
-- ranked in one engine filter when the call starts (recipe_pick
-- memo().prefetch).
--
-- Each machine carries its own base productivity,
-- effect_receiver.base_effect.productivity: +50% on the electromagnetic
-- plant, the foundry and the biochamber, 0 on most. recipe_machine.lua adds
-- it to the research bonus of every recipe the machine is chosen for. It
-- does not change which machine is chosen: still the fastest.
--
-- A belt moves belt_speed * 480 items a second over both lanes, one item per
-- slot, no stacking: the prototype docs' own "speed x 480 = x Items/second"
-- (TransportBeltConnectablePrototype::speed), and RateCalculator's factor
-- (scripts/gui-util.lua line 139).
--
-- API names verified against runtime-api.json 2.0.77 and 2.1.17:
--   LuaPrototypes::get_entity_filtered(filters) -> LuaCustomTable[string ->
--     LuaEntityPrototype]; EntityPrototypeFilter "type" takes a type or an
--     array of types.
--   LuaEntityPrototype::crafting_categories (dictionary[string -> true],
--     CraftingMachine), ::get_crafting_speed(quality?) (CraftingMachine),
--     ::belt_speed (TransportBeltConnectable), ::items_to_place_this
--     (array[ItemToPlace], {name, count}), LuaPrototypeBase::hidden, ::parameter.
--   LuaEntityPrototype::effect_receiver (optional EffectReceiver; CraftingMachine,
--     Lab, MiningDrill, and AgriculturalTower in 2.1), EffectReceiver::base_effect
--     (ModuleEffects in 2.0.77, Effect in 2.1.17), whose productivity is an
--     optional float in both.
--   LuaRecipePrototype::has_category(category) -> boolean, ::name.

local CRAFTER_TYPES = { "assembling-machine", "furnace", "rocket-silo" }
local BELT_TYPE = "transport-belt"
local ITEMS_PER_BELT_SPEED = 480
local SECONDS_PER_MINUTE = 60

local M = {}

--- The usable prototypes of one or more entity types, by name.
local function listed(types)
  local out = {}
  for _, proto in pairs(prototypes.get_entity_filtered({ { filter = "type", type = types } })) do
    if not (proto.hidden or proto.parameter) then out[#out + 1] = proto end
  end
  table.sort(out, function(a, b) return a.name < b.name end)
  return out
end

local function faster(a, b)
  if a.speed ~= b.speed then return a.speed > b.speed end
  return a.name < b.name
end

local function sorted_keys(set)
  local out = {}
  for key in pairs(set) do out[#out + 1] = key end
  table.sort(out)
  return out
end

--- One machine or belt as the call keeps it, fastest first in its list.
local function entry(proto, speed)
  return { proto = proto, name = proto.name, speed = speed, places = proto.items_to_place_this or {} }
end

--- A crafting machine's own productivity, 0 when it has none.
local function base_productivity(proto)
  local receiver = proto.effect_receiver
  local effect = receiver and receiver.base_effect
  return effect and effect.productivity or 0
end

--- Crafting speed and belt speed are both positive by the prototype rules
--- (CraftingMachinePrototype::crafting_speed "has to be positive",
--- TransportBeltConnectablePrototype::speed "a positive non-infinite
--- number"), so neither is ever a divide by zero below.
local function crafters()
  local out = {}
  for _, proto in ipairs(listed(CRAFTER_TYPES)) do
    local one = entry(proto, proto.get_crafting_speed())
    one.categories = proto.crafting_categories or {}
    one.sorted = sorted_keys(one.categories)
    one.productivity = base_productivity(proto)
    out[#out + 1] = one
  end
  table.sort(out, faster)
  return out
end

local function belts()
  local out = {}
  for _, proto in ipairs(listed(BELT_TYPE)) do out[#out + 1] = entry(proto, proto.belt_speed) end
  table.sort(out, faster)
  return out
end

--- Every item that places a crafting machine or a belt, ranked in one go.
local function prefetch_placers(ctx)
  local list = {}
  for _, group in ipairs({ ctx.crafters, ctx.belts }) do
    for _, one in ipairs(group) do
      for _, item in ipairs(one.places) do list[#list + 1] = { kind = "item", name = item.name } end
    end
  end
  ctx.picker.prefetch(list)
end

--- Whether the force can build a machine or belt: some item that places it
--- has a recipe the force can use. Remembered for the call.
local function can_build(ctx, one)
  local known = ctx.built[one.name]
  if known ~= nil then return known end
  local yes = false
  for _, item in ipairs(one.places) do
    if ctx.picker.pick("item", item.name) then yes = true break end
  end
  ctx.built[one.name] = yes
  return yes
end

--- recipe.has_category(category), asked at most once per category for one
--- recipe: `seen` holds its answers. A category that answers yes joins
--- ctx.matched, asked first from then on.
local function has(ctx, recipe, seen, category)
  local answer = seen[category]
  if answer == nil then
    answer = recipe.has_category(category)
    seen[category] = answer
    if answer and not ctx.matched[category] then
      ctx.matched[category] = true
      ctx.matched[#ctx.matched + 1] = category
    end
  end
  return answer
end

local function makes(ctx, crafter, recipe, seen)
  for _, category in ipairs(ctx.matched) do
    if crafter.categories[category] and has(ctx, recipe, seen, category) then return true end
  end
  for _, category in ipairs(crafter.sorted) do
    if has(ctx, recipe, seen, category) then return true end
  end
  return false
end

local function verdict(ctx, crafter)
  return { name = crafter.name, speed = crafter.speed, productivity = crafter.productivity,
           unlocked = can_build(ctx, crafter) }
end

--- What the caller sent, for an error: a name as itself, anything else as its
--- type, never a table's address.
local function shown(value)
  if type(value) == "string" then return "\"" .. value .. "\"" end
  return type(value)
end

local function named(list, name)
  for _, one in ipairs(list) do
    if one.name == name then return one end
  end
  return nil
end

--- Everything one call needs to choose machines and the belt: the crafting
--- machines and belts read once, `picker` (recipe_pick.memo for the force) to
--- tell what it can build, and `wanted`, a machine the caller named. Errors
--- on a `wanted` that is not a crafting machine, in words the caller can act
--- on.
function M.context(picker, wanted)
  local ctx = { picker = picker, built = {}, chosen = {}, matched = {} }
  ctx.crafters, ctx.belts = crafters(), belts()
  if wanted ~= nil then
    ctx.wanted = named(ctx.crafters, wanted)
    if not ctx.wanted then
      error("machine must be a crafting machine prototype name, e.g. assembling-machine-2, not " .. shown(wanted), 0)
    end
  end
  prefetch_placers(ctx)
  return ctx
end

--- The named machine when it makes the recipe, else the fastest the force can
--- build that does, else the fastest that does, else false.
local function choose(ctx, recipe)
  local seen = {}
  if ctx.wanted and makes(ctx, ctx.wanted, recipe, seen) then return verdict(ctx, ctx.wanted) end
  for _, crafter in ipairs(ctx.crafters) do
    if can_build(ctx, crafter) and makes(ctx, crafter, recipe, seen) then return verdict(ctx, crafter) end
  end
  for _, crafter in ipairs(ctx.crafters) do
    if makes(ctx, crafter, recipe, seen) then return verdict(ctx, crafter) end
  end
  return false
end

--- The machine for one recipe as {name, speed, productivity, unlocked}, or
--- nil when no crafting machine can make it at all. Chosen once per recipe a
--- call.
function M.for_recipe(ctx, recipe)
  local chosen = ctx.chosen[recipe.name]
  if chosen == nil then
    chosen = choose(ctx, recipe)
    ctx.chosen[recipe.name] = chosen
  end
  return chosen or nil
end

--- Machines needed to run `crafts_per_min` crafts of `recipe` on `machine`.
function M.count(recipe, machine, crafts_per_min)
  return crafts_per_min * recipe.energy / (machine.speed * SECONDS_PER_MINUTE)
end

--- Crafts a minute that `machines` of `machine` run of `recipe`.
function M.crafts(recipe, machine, machines)
  return machines * machine.speed * SECONDS_PER_MINUTE / recipe.energy
end

local function belt_of(ctx, belt)
  return { name = belt.name, per_sec = belt.speed * ITEMS_PER_BELT_SPEED, unlocked = can_build(ctx, belt) }
end

--- The belt rows are counted in, as {name, per_sec, unlocked}: `wanted` when
--- the caller named one, else the fastest the force can build. Nil only when
--- the game has no transport belt at all.
function M.belt(ctx, wanted)
  if wanted ~= nil then
    local belt = named(ctx.belts, wanted)
    if belt then return belt_of(ctx, belt) end
    error("belt must be a transport belt prototype name, e.g. fast-transport-belt, not " .. shown(wanted), 0)
  end
  for _, belt in ipairs(ctx.belts) do
    if can_build(ctx, belt) then return belt_of(ctx, belt) end
  end
  return ctx.belts[1] and belt_of(ctx, ctx.belts[1]) or nil
end

return M
