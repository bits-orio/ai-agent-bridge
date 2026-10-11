-- AI Agent Bridge - scripts/tools/recipe_cost.lua
-- Author: bits-orio
-- License: MIT
--
-- What breaks a tie between recipes that rank level on every rule before
-- their names: the raw cost of each, from a short walk down its inputs.
-- Solid fuel is the case. Its three vanilla recipes each make one, each with
-- solid fuel as main product, so the name used to decide, and heavy oil won:
-- about 80 crude oil a fuel, where light oil costs about 22.
--
-- The cost of one unit of an item made by a recipe: each input it takes per
-- unit, times what one unit of that input costs. An input mined or pumped
-- where the question is about costs 1. One with no route costs math.huge, so
-- a route through anything the force cannot get is ruled out. One more than
-- DEPTH levels down costs 1, as if it were raw. A raw unit is a raw unit, an
-- item or a fluid alike: rough, and only ever used to choose between routes
-- that are otherwise equal.
--
-- An input's route is the one recipe_pick ranks first for it before any tie
-- is broken (ask.route), so a cost never waits on another tie, and the walk
-- ends DEPTH levels down whatever cycles it meets. Costs are kept for the call
-- by item and depth left, a pure function of the two, so a cost is the same
-- whichever tie asked first.

local yield = require("scripts.tools.recipe_yield")

local DEPTH = 5

local M = {}

--- What one craft takes, sorted by key so the sum is the same on every peer.
local function inputs_of(io)
  local out = {}
  for key, amount in pairs(io) do
    if amount < 0 then out[#out + 1] = { key = key, amount = -amount } end
  end
  table.sort(out, function(a, b) return a.key < b.key end)
  return out
end

local cost_of

--- What one unit of the item at `key` costs, `depth` levels left to walk.
local function unit(ask, key, depth)
  local memo_key = key .. "#" .. depth
  local known = ask.costs[memo_key]
  if known then return known end
  local kind, name = yield.split(key)
  local cost = 1
  if depth > 0 and not ask.mined(kind, name) then
    local route = ask.route(kind, name)
    cost = route and cost_of(ask, route, depth - 1) or math.huge
  end
  ask.costs[memo_key] = cost
  return cost
end

--- What one unit of the item `found` ({io, net}) is ranked for costs.
cost_of = function(ask, found, depth)
  local total = 0
  for _, input in ipairs(inputs_of(found.io)) do
    total = total + input.amount / found.net * unit(ask, input.key, depth)
  end
  return total
end

--- The raw cost of one unit of an item made by `found`. `ask` is {mined =
--- function(kind, name), route = function(kind, name) -> found or nil,
--- costs = a table kept for the call}.
function M.of(ask, found)
  return cost_of(ask, found, DEPTH)
end

M.DEPTH = DEPTH

return M
