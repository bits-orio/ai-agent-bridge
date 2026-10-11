-- AI Agent Bridge - scripts/tools/recipe_needs.lua
-- Author: bits-orio
-- License: MIT
--
-- recipe_needs: what it takes to make something, walked through the game's
-- own recipes for one force (docs/design/recipe-needs-spec.md, part 1). The
-- model turns a question into item names and amounts; the arithmetic happens
-- here, from the prototypes this save actually loaded, so a modded recipe is
-- answered as it is rather than as the model remembers vanilla.
--
-- This file owns the manifest, the argument checks and the flow. The pieces:
-- recipe_names finds the names the game does not have and what was meant,
-- recipe_pick picks a recipe per item (recipe_rank lists the candidates,
-- recipe_cost breaks ties, raw_sources keeps what is mined raw on the
-- surface asked about), machine_pick chooses machines and the belt,
-- recipe_machine nets each pick on its machine, recipe_walk sums the tree,
-- recipe_reply shapes the answer.
--
-- `surface` is where the things are made, the asker's own as a rule: it
-- decides what counts as mined or pumped (raw_sources.lua). Left out, or on
-- a surface whose map generator does not say, anything mined on any planet
-- counts, and approx says any_planet when a recipe could make such a raw
-- row instead.
--
-- A pure read: no storage write, nothing kept between calls, no on_tick.
-- Prototypes are the same on every peer, so the reply is too.
--
-- Targets go in as parallel lists, `items` beside one of `per_min` or
-- `machines`, the probe grammar's list<string> and list<number>
-- (service/internal/catalog/params.go): no service change, no probe change.
--
-- API names verified against runtime-api.json 2.0.77 and 2.1.17:
--   LuaPrototypes::item and ::fluid, LuaCustomTable[string -> prototype],
--   nil for a name the game does not have.

local force_lookup   = require("scripts.tools.force_lookup")
local machine_pick   = require("scripts.tools.machine_pick")
local recipe_machine = require("scripts.tools.recipe_machine")
local recipe_names   = require("scripts.tools.recipe_names")
local recipe_pick    = require("scripts.tools.recipe_pick")
local recipe_reply   = require("scripts.tools.recipe_reply")
local recipe_walk    = require("scripts.tools.recipe_walk")
local surface_lookup = require("scripts.tools.surface_lookup")
local yield          = require("scripts.tools.recipe_yield")

local MAX_TARGETS = 12
local MAX_PER_MIN = 1e9
local MAX_MACHINES = 1e6

local M = {}

M.manifest = {
  recipe_needs = {
    desc = "What it takes to make something: machines, every input per minute down to raw resources, and belts of each. Every target in one call; the totals are summed for you. Never work a ratio out yourself. A count of something the player \"has\" or \"runs\" is machines: \"I have 48 blue science\" is machines=[48], not per_min=[48]. Only a rate, \"60 a minute\", is per_min. Say which you assumed. Uses this force's unlocked recipes, research and machine productivity; approx says why figures are rough. Unknown names: found=false, with did_you_mean.",
    tier = "1",
    params = {
      items    = "list<string>! target item or fluid prototype names, e.g. chemical-science-pack",
      per_min  = "list<number> wanted output per minute, one per item, only for a stated rate; give this or machines",
      machines = "list<number> machines making each target, one per item: a bare count the player has; give this or per_min",
      machine  = "string one machine to use for every recipe it can make, e.g. assembling-machine-2; default is the fastest the force can build",
      belt     = "string belt prototype to count belts in, e.g. fast-transport-belt; default is the fastest the force can build",
      stop_at  = "list<string> items to treat as inputs and not expand, e.g. iron-plate",
      surface  = "string the asker's surface, e.g. nauvis: raw rows are what it mines or pumps",
    },
  },
}

--- One bare value is a one-entry list: the grammar asks for a list, and a
--- model that sends 60 where it meant [60] still means 60.
local function as_list(value)
  if value == nil then return {} end
  if type(value) ~= "table" then return { value } end
  return value
end

local function names_arg(value, what)
  local out = {}
  for i, name in ipairs(as_list(value)) do
    if type(name) ~= "string" or name == "" then
      error(what .. " must be a list of item or fluid names, e.g. [\"iron-plate\"]", 0)
    end
    out[i] = name
  end
  return out
end

--- A list<number> argument with one entry per item, or nil when left out.
local function amounts_arg(value, what, count, max)
  local list = as_list(value)
  if #list == 0 then return nil end
  if #list ~= count then
    error(string.format("%s needs one entry per item: %d items but %d %s", what, count, #list, what), 0)
  end
  local out = {}
  for i, raw in ipairs(list) do
    local n = tonumber(raw)
    if n == nil or n <= 0 or n > max then
      error(string.format("%s must hold numbers above 0 and at most %g", what, max), 0)
    end
    out[i] = n
  end
  return out
end

--- Exactly one of per_min or machines, as the mode and its amounts.
local function mode_of(a, count)
  local per_min = amounts_arg(a.per_min, "per_min", count, MAX_PER_MIN)
  local machines = amounts_arg(a.machines, "machines", count, MAX_MACHINES)
  if per_min and machines then
    error("give per_min or machines, not both: per_min is a rate, machines is how many machines make each item", 0)
  end
  if per_min then return "per_min", per_min end
  if machines then return "machines", machines end
  error("give per_min (a rate per minute) or machines (how many machines make each item), one entry per item", 0)
end

--- stop_at as walk keys. A name both an item and a fluid stops both.
local function stop_set(stop_at)
  local set = {}
  for _, name in ipairs(stop_at) do
    if prototypes.item[name] then set[yield.key("item", name)] = true end
    if prototypes.fluid[name] then set[yield.key("fluid", name)] = true end
  end
  return set
end

--- A machine count turned into a rate: crafts the machines run, times what
--- each craft nets. 0 and a reason when the target cannot be sized.
local function rate_from_machines(picker, ctx, target)
  local pick, _, mined = picker.pick(target.kind, target.name)
  if mined then return 0, "it is mined or pumped, not crafted, so a machine count cannot become a rate" end
  if not pick then return 0, "no recipe this force can use makes it, so a machine count cannot become a rate" end
  local machine = machine_pick.for_recipe(ctx, pick.proto)
  if not machine then return 0, "no crafting machine can make its recipe" end
  return machine_pick.crafts(pick.proto, machine, target.machines) * pick.net
end

--- The targets with their rates. Sized by machine count, every target's
--- recipe is asked of the engine in one prefetch before the first is sized.
local function targets_of(items, mode, amounts, picker, ctx)
  local out = {}
  for i, name in ipairs(items) do out[i] = { kind = recipe_names.kind_of(name), name = name } end
  if mode == "machines" then picker.prefetch(out) end
  for i, target in ipairs(out) do
    if mode == "per_min" then
      target.per_min = amounts[i]
    else
      target.machines = amounts[i]
      target.per_min, target.reason = rate_from_machines(picker, ctx, target)
    end
  end
  return out
end

--- The targets with a rate to walk: one that could not be sized adds nothing.
local function walkable(targets)
  local out = {}
  for _, target in ipairs(targets) do
    if target.per_min > 0 then out[#out + 1] = target end
  end
  return out
end

--- The surface asked about, nil when none was given, or nil and the
--- found = false reply for a name or index the game does not have.
local function surface_of(a, force)
  local wanted = a.surface
  if wanted == nil or wanted == "" then return nil end
  if type(wanted) ~= "string" and type(wanted) ~= "number" then
    error("surface must be one surface name or index, e.g. nauvis, not " .. type(wanted), 0)
  end
  local surface, miss = surface_lookup.find(wanted)
  if surface then return surface end
  miss.force = force.name
  return nil, miss
end

local function unknown_reply(force, unknown)
  return { found = false, force = force.name, unknown = unknown, did_you_mean = recipe_names.guesses(unknown),
           reason = "no item or fluid by that name: use internal names such as automation-science-pack or iron-plate; did_you_mean lists real ones close to each" }
end

--- Walks the targets and shapes the reply, the arguments known good. Every
--- pick is netted on its machine (recipe_machine), for sizing a machine
--- count as for the walk.
local function answer(force, surface, args)
  -- machine_pick refuses a machine or belt it does not know, whatever its type.
  local picker = recipe_pick.memo(force, surface)
  local ctx = machine_pick.context(picker, args.machine)
  local belt = machine_pick.belt(ctx, args.belt)
  local crafting = recipe_machine.picker(picker, ctx)
  local targets = targets_of(args.items, args.mode, args.amounts, crafting, ctx)
  local walk = recipe_walk.run(walkable(targets), crafting, stop_set(args.stop_at))
  return recipe_reply.answer({ force = force, mode = args.mode, belt = belt, ctx = ctx, walk = walk, targets = targets })
end

local function recipe_needs(a)
  local force = force_lookup.require_force(a.force)
  local items = names_arg(a.items, "items")
  if #items == 0 then error("items is required: target item or fluid names, e.g. [\"automation-science-pack\"]", 0) end
  if #items > MAX_TARGETS then error("at most " .. MAX_TARGETS .. " items in one call", 0) end
  local mode, amounts = mode_of(a, #items)
  local stop_at = names_arg(a.stop_at, "stop_at")
  local unknown = recipe_names.unknown(items, stop_at)
  if #unknown > 0 then return unknown_reply(force, unknown) end
  local surface, miss = surface_of(a, force)
  if miss then return miss end
  return answer(force, surface, { items = items, mode = mode, amounts = amounts, stop_at = stop_at,
                                  machine = a.machine, belt = a.belt })
end

M.functions = { recipe_needs = recipe_needs }

return M
