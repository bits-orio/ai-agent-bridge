-- AI Agent Bridge - scripts/tools/recipe_pick.lua
-- Author: bits-orio
-- License: MIT
--
-- Which recipe makes one item or fluid for one force, and which others could.
-- recipe_rank.lua lists the candidates best first; three rules here change
-- that order before the first is picked, and the next three are its
-- alternatives.
--
-- A round trip is dropped. A recipe that makes the item only on the side,
-- neither named after it nor with it as main product, is a round trip when
-- one of its inputs is, by that input's own best recipe, made from the item.
-- Emptying a barrel is the case. 2.0's data-updates.lua gives every fluid an
-- empty-<fluid>-barrel recipe, unlocked by fluid handling, neither hidden nor
-- a parameter; walked, it makes water out of water barrels filled from water
-- and charges a new barrel for every 50 units. Measured on a live 2.0.77
-- force with fluid handling: blue science's iron plate went from 720 to
-- 1378.64 a minute. Dropping the round trip leaves water raw, as it is in
-- the game, and puts petroleum gas back on oil processing. The item's own
-- recipe is never dropped this way, so asking for water barrels still fills
-- them. It is the walk's cycle rule applied one step early, while another
-- recipe can still win.
--
-- A dead end ranks last. A side recipe that takes an input the force cannot
-- get, no usable recipe makes it and it is not mined or pumped where the
-- question is about, loses to every candidate whose inputs all resolve.
-- Nuclear fuel reprocessing is the case: it nets three uranium-238 a craft to
-- uranium processing's 0.993, so it won on output, and charged
-- depleted-uranium-fuel-cell, a burnt result no recipe makes, as raw where
-- uranium ore belonged. It stays an alternative. Only the inputs are looked
-- at, one level down; the named recipe and a main-product recipe keep their
-- place whatever they take.
--
-- A tie is broken by cost. Candidates level on every rule before the name
-- (same standing, same net output) go cheapest first by the raw cost of a
-- short walk down their inputs (recipe_cost.lua), a route through anything
-- unobtainable ruled out, then by name. Solid fuel is the case: by name heavy
-- oil won, about 80 crude oil a fuel, where light oil costs about 22. A pick
-- a tie decided says `tie` when two or more of the tied routes were possible,
-- and recipe_walk adds tie to approx. Costs are worked out only for the tie
-- that decides a pick.
--
-- An item the game hands out where the question is about, mined from a
-- resource or pumped from a tile, with no recipe named after it, is raw
-- however many recipes could make it (scripts/tools/raw_sources.lua): a force
-- that researched asteroid crushing still mines its iron ore. Its recipes
-- come back as alternatives instead of a pick, so the answer can still
-- mention crushing or melting, and `anywhere` says when no surface decided.
--
-- What it costs: the engine is asked a list of items at a time, never one by
-- one, when it can be helped (recipe_rank.lua). memo().prefetch ranks a
-- whole list in one filter and the inputs the round-trip and dead-end rules
-- will look at in one more, which is how recipe_walk asks for a layer of the
-- tree and machine_pick for every machine and belt.

local yield       = require("scripts.tools.recipe_yield")
local raw_sources = require("scripts.tools.raw_sources")
local rank        = require("scripts.tools.recipe_rank")
local recipe_cost = require("scripts.tools.recipe_cost")

local MAX_ALTERNATIVES = 3

local M = {}

local function side(found)
  return not (found.named or found.main)
end

--- Whether a side recipe for the item at `key` takes an input whose own best
--- recipe is made from that item.
local function round_trip(memo, found, key)
  if not side(found) then return false end
  for input, amount in pairs(found.io) do
    if amount < 0 then
      local best = rank.ranked(memo, yield.split(input))[1]
      if best and (best.io[key] or 0) < 0 then return true end
    end
  end
  return false
end

--- Whether the force can get an item at all: a usable recipe makes it, or it
--- is mined or pumped where the question is about.
local function obtainable(memo, kind, name)
  return #rank.ranked(memo, kind, name) > 0 or memo.mined(kind, name)
end

--- Whether every input `found` takes is obtainable.
local function resolves(memo, found)
  for input, amount in pairs(found.io) do
    if amount < 0 and not obtainable(memo, yield.split(input)) then return false end
  end
  return true
end

--- The order a pick is made in before a tie is broken: the named recipe,
--- then every candidate that is not a dead end, then recipe_rank's order.
local function before(a, b)
  if a.named ~= b.named then return a.named end
  if a.alive ~= b.alive then return a.alive end
  return rank.better(a, b)
end

--- The candidates for one item in pick order before ties, round trips
--- dropped: worked out once a call, and the route recipe_cost walks.
local function ordered(memo, kind, name)
  local key = yield.key(kind, name)
  local order = memo.orders[key]
  if order then return order end
  order = {}
  for _, found in ipairs(rank.ranked(memo, kind, name)) do
    if not round_trip(memo, found, key) then order[#order + 1] = found end
  end
  if #order > 1 then
    for _, found in ipairs(order) do found.alive = not side(found) or resolves(memo, found) end
    table.sort(order, before)
  end
  memo.orders[key] = order
  return order
end

--- Whether two candidates rank level on every rule but the name.
local function level(a, b)
  return a.named == b.named and a.alive == b.alive and a.main == b.main and a.net == b.net
end

--- How many candidates at the head of `order` are level with the first.
local function tied(order)
  local n = 1
  while order[n + 1] and level(order[n + 1], order[1]) do n = n + 1 end
  return n
end

--- `order` with its first `n`, which are level, cheapest first, and whether
--- two or more of those could be made at all, so a tie chose the route.
local function break_tie(memo, order, n)
  local ask = { mined = memo.mined, costs = memo.costs,
                route = function(kind, name) return ordered(memo, kind, name)[1] end }
  local head = {}
  for i = 1, n do head[i] = { found = order[i], cost = recipe_cost.of(ask, order[i]) } end
  table.sort(head, function(a, b)
    if a.cost ~= b.cost then return a.cost < b.cost end
    return a.found.name < b.found.name
  end)
  local out, possible = {}, 0
  for i, one in ipairs(head) do
    out[i] = one.found
    if one.cost < math.huge then possible = possible + 1 end
  end
  for i = n + 1, #order do out[i] = order[i] end
  return out, possible > 1
end

--- The names of up to MAX_ALTERNATIVES candidates from position `from` on.
local function names_from(order, from)
  local out = {}
  for i = from, math.min(#order, from + MAX_ALTERNATIVES - 1) do
    out[#out + 1] = order[i].name
  end
  return out
end

--- The recipe that makes one item or fluid, as {name, proto, recipe, io,
--- net, alternatives, byproducts, tie}. With no pick it returns nil, whether
--- the item is only locked, and, for an item the game hands out, {alternatives,
--- anywhere}: the recipes that could make it anyway, and whether that was
--- reckoned for any planet.
local function pick(memo, kind, name)
  local order = ordered(memo, kind, name)
  if memo.mined(kind, name) then
    return nil, false, { alternatives = names_from(order, 1), anywhere = memo.anywhere() }
  end
  if #order == 0 then
    local _, locked = rank.ranked(memo, kind, name)
    return nil, locked
  end
  local tie = false
  local n = tied(order)
  if n > 1 then order, tie = break_tie(memo, order, n) end
  local best = order[1]
  return {
    name = best.name, proto = best.proto, recipe = best.recipe, io = best.io, net = best.net,
    alternatives = names_from(order, 2), byproducts = yield.outputs(best.io) > 1, tie = tie,
  }
end

--- What the side rules will look up for the items of `list`, all ranked:
--- the inputs of every candidate that is neither named nor a main product.
local function side_inputs(memo, list)
  local out = {}
  for _, one in ipairs(list) do
    for _, found in ipairs(memo.ranks[yield.key(one.kind, one.name)].usable) do
      if side(found) then
        for input, amount in pairs(found.io) do
          if amount < 0 then
            local kind, name = yield.split(input)
            out[#out + 1] = { kind = kind, name = name }
          end
        end
      end
    end
  end
  return out
end

--- Ranks every item of `list` not ranked yet in one engine filter, then in
--- one more the inputs the side rules will ask about. Either filter is
--- skipped when it would have no item to ask for.
local function prefetch(memo, list)
  rank.rank_all(memo, list)
  rank.rank_all(memo, side_inputs(memo, list))
end

--- Picks for one force, remembered for the length of one call, with what is
--- mined or pumped read for `surface` (a LuaSurface, or nil for any planet).
--- pick(kind, name) answers as described above; prefetch(list) ranks many
--- items ahead of their picks, changing nothing a pick returns. Nothing
--- outlives the call.
function M.memo(force, surface)
  local memo = rank.memo(force)
  local rule = raw_sources.rule(surface)
  memo.mined, memo.anywhere = rule.mined, rule.anywhere
  memo.orders, memo.costs, memo.picks = {}, {}, {}
  local picker = {}
  function picker.pick(kind, name)
    local key = yield.key(kind, name)
    local known = memo.picks[key]
    if not known then
      local found, locked, mined = pick(memo, kind, name)
      known = { found = found, locked = locked, mined = mined }
      memo.picks[key] = known
    end
    return known.found, known.locked, known.mined
  end
  function picker.prefetch(list) prefetch(memo, list) end
  return picker
end

return M
