-- AI Agent Bridge - scripts/tools/recipe_walk.lua
-- Author: bits-orio
-- License: MIT
--
-- The walk behind recipe_needs: from every target down to raw inputs, summed
-- into one table keyed by item.
--
-- Two passes. Discovery goes depth first from each target in turn, picks one
-- recipe per item through recipe_pick, and records the order items finish in.
-- Demand then runs over that order reversed, which puts every item after
-- everything that consumes it, so an item's total is complete before it is
-- passed down. A tree that reaches iron plate by twenty paths costs one visit
-- to iron plate, never one per path: a deep modded tree stays linear.
--
-- An item is one of three things: crafted (it has a recipe and is expanded),
-- stopped (the caller listed it in stop_at, kept as a row and not expanded) or
-- raw (no recipe the force can use, mined or pumped with no recipe named
-- after it, or a cap was reached first).
--
-- An item met again while it is still on the current path is a cycle: that
-- input is charged as raw at that point and `cycle` goes into approx. Hard
-- caps: 15 deep and 80 distinct items. Past either an item is not expanded,
-- it is charged as raw, and `cap` goes into approx. A recipe with more than
-- one net output adds `byproducts`: v1 charges the whole recipe to the item
-- it was picked for and does not balance the rest. A pick a tie between
-- equal recipes decided adds `tie` (recipe_pick.lua). A raw item counted as
-- mined or pumped on any planet, because no surface said what is mined
-- where the player is, adds `any_planet` when a recipe the force can use
-- could make it: it may really be crafted there, and that recipe is in
-- alternatives. Ore no recipe makes is raw wherever it is, so it adds
-- nothing.
--
-- Before either pass the tree is asked of the engine a layer at a time:
-- every item one step further down goes to recipe_pick in one prefetch, a
-- filter or two however many items the layer holds, where the walk on its
-- own would spend a filter on each. That only fills what the picker
-- remembers for the call; the walk picks exactly what it would without it.

local yield = require("scripts.tools.recipe_yield")

local MAX_DEPTH = 15
local MAX_ITEMS = 80

local M = {}

--- The net inputs of one craft, sorted so the walk is the same on every peer.
local function inputs_of(io)
  local out = {}
  for key, amount in pairs(io) do
    if amount < 0 then
      local kind, name = yield.split(key)
      out[#out + 1] = { key = key, kind = kind, name = name, per_craft = -amount }
    end
  end
  table.sort(out, function(a, b) return a.key < b.key end)
  return out
end

--- Ranks the tree under `targets` a layer at a time, as deep as the walk
--- picks and no wider once MAX_ITEMS are met. An item in `stop` is never
--- picked, so it is never asked about.
local function prefetch(walk, targets)
  local seen, layer = {}, {}
  local function meet(kind, name, into)
    local key = yield.key(kind, name)
    if seen[key] or walk.stop[key] then return end
    seen[key] = true
    into[#into + 1] = { kind = kind, name = name }
  end
  for _, target in ipairs(targets) do meet(target.kind, target.name, layer) end
  local depth, met = 1, #layer
  while #layer > 0 do
    walk.picker.prefetch(layer)
    if depth > MAX_DEPTH or met > MAX_ITEMS then return end
    local below = {}
    for _, one in ipairs(layer) do
      local pick = walk.picker.pick(one.kind, one.name)
      for _, input in ipairs(pick and inputs_of(pick.io) or {}) do meet(input.kind, input.name, below) end
    end
    layer, depth, met = below, depth + 1, met + #below
  end
end

--- Decides what one newly met item is, and picks its recipe when it has one.
local function classify(walk, node, depth)
  if walk.stop[node.key] then node.role = "stop" return end
  local pick, locked, mined = walk.picker.pick(node.kind, node.name)
  if not pick then
    node.role, node.locked, node.mined = "raw", locked, mined
    if mined and mined.anywhere and #mined.alternatives > 0 then walk.approx.any_planet = true end
    return
  end
  if depth > MAX_DEPTH or walk.count > MAX_ITEMS then
    node.role, node.capped = "raw", true
    walk.approx.cap = true
    return
  end
  node.role, node.pick, node.inputs = "craft", pick, inputs_of(pick.io)
  if pick.byproducts then walk.approx.byproducts = true end
  if pick.tie then walk.approx.tie = true end
end

local function discover(walk, kind, name, depth)
  local key = yield.key(kind, name)
  if walk.nodes[key] then return end
  local node = { key = key, kind = kind, name = name }
  walk.nodes[key] = node
  walk.count = walk.count + 1
  classify(walk, node, depth)
  if node.role == "craft" then
    walk.on_path[key] = true
    for _, input in ipairs(node.inputs) do
      if walk.on_path[input.key] then
        input.cycle = true
        walk.approx.cycle = true
      else
        discover(walk, input.kind, input.name, depth + 1)
      end
    end
    walk.on_path[key] = nil
  end
  walk.order[#walk.order + 1] = key
end

--- Passes demand down in reverse finishing order. A cycle's share goes to
--- `looped` instead, where the reply lists it as raw.
local function settle(walk, targets)
  local demand = {}
  for _, target in ipairs(targets) do
    local key = yield.key(target.kind, target.name)
    demand[key] = (demand[key] or 0) + target.per_min
  end
  for i = #walk.order, 1, -1 do
    local node = walk.nodes[walk.order[i]]
    node.per_min = demand[node.key] or 0
    if node.role == "craft" then
      node.crafts = node.per_min / node.pick.net
      for _, input in ipairs(node.inputs) do
        local into = input.cycle and walk.looped or demand
        into[input.key] = (into[input.key] or 0) + node.crafts * input.per_craft
      end
    end
  end
end

--- Walks `targets`, a list of {kind, name, per_min}, for one force.
--- `picker` is recipe_pick.memo(force); `stop` is a set of keys not to expand.
--- Returns {nodes, looped, approx}: nodes in finishing order, each with role,
--- per_min, when crafted pick and crafts, and when mined or pumped `mined`,
--- {alternatives}; looped, per_min by key charged as raw at a cycle; approx,
--- a set of reasons the figures are rough.
function M.run(targets, picker, stop)
  local walk = { picker = picker, stop = stop or {}, nodes = {}, order = {}, on_path = {},
                 count = 0, approx = {}, looped = {} }
  prefetch(walk, targets)
  for _, target in ipairs(targets) do
    discover(walk, target.kind, target.name, 1)
  end
  settle(walk, targets)
  local nodes = {}
  for i, key in ipairs(walk.order) do nodes[i] = walk.nodes[key] end
  return { nodes = nodes, looped = walk.looped, approx = walk.approx }
end

M.MAX_DEPTH = MAX_DEPTH
M.MAX_ITEMS = MAX_ITEMS

return M
