-- AI Agent Bridge - scripts/tools/recipe_reach.lua
-- Author: bits-orio
-- License: MIT
--
-- Whether a recipe's chain can be finished on the surface a question is
-- about: every input, all the way down, either mined, pumped or harvested
-- there (raw_sources rule.available) or made by a recipe that can run there
-- (recipe_rank's candidates, already narrowed by surface conditions). Only
-- asked when a surface was given.
--
-- Why it exists. The named recipe always won the pick: iron-plate for iron
-- plate. On Vulcanus no iron ore is mined and the crusher that makes it
-- needs gravity 0, so that recipe's chain cannot be finished there, while
-- casting-iron from lava can. recipe_pick puts the candidates that complete
-- ahead of those that do not, and keeps its order when none does.
--
-- Bounded like the walk it serves (recipe_walk.lua): 15 deep, and 200
-- distinct items for the whole call rather than the walk's 80, since it
-- also looks down the alternatives the walk never takes. Past either an
-- item is assumed to complete, so a cap can only keep today's pick, never
-- move it, and reach.capped says it was hit so approx can say cap. An item met again while it is
-- being decided is a cycle and does not complete by that route; an answer
-- reached through a cycle is not remembered, since the item it leaned on
-- may still complete another way.

local yield = require("scripts.tools.recipe_yield")
local rank  = require("scripts.tools.recipe_rank")

local MAX_DEPTH = 15
local MAX_ITEMS = 200

local M = {}

--- The net inputs of one candidate, sorted so the check visits them in the
--- same order on every peer.
local function inputs_of(found)
  local out = {}
  for key, amount in pairs(found.io) do
    if amount < 0 then out[#out + 1] = key end
  end
  table.sort(out)
  return out
end

local item_completes

local function recipe_completes(reach, found, depth)
  for _, key in ipairs(inputs_of(found)) do
    local kind, name = yield.split(key)
    if not item_completes(reach, kind, name, depth + 1) then return false end
  end
  return true
end

item_completes = function(reach, kind, name, depth)
  local key = yield.key(kind, name)
  local known = reach.known[key]
  if known ~= nil then return known end
  if reach.available(kind, name) then
    reach.known[key] = true
    return true
  end
  if reach.visiting[key] then
    reach.tainted = true
    return false
  end
  if depth > MAX_DEPTH or reach.count >= MAX_ITEMS then
    reach.capped = true
    return true
  end
  reach.count, reach.visiting[key] = reach.count + 1, true
  local outer = reach.tainted
  reach.tainted = false
  local ok = false
  for _, found in ipairs(rank.ranked(reach.memo, kind, name)) do
    if recipe_completes(reach, found, depth) then ok = true break end
  end
  reach.visiting[key] = nil
  if ok or not reach.tainted then reach.known[key] = ok end
  reach.tainted = outer or reach.tainted
  return ok
end

--- A reach for one call: `memo` is recipe_pick's, `available(kind, name)`
--- raw_sources' rule.available. reach.completes(found) answers for one
--- candidate recipe, reach.available for one item.
function M.new(memo, available)
  local reach = { memo = memo, available = available, known = {}, visiting = {}, count = 0, tainted = false }
  function reach.completes(found) return recipe_completes(reach, found, 1) end
  return reach
end

return M
