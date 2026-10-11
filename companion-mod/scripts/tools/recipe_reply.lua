-- AI Agent Bridge - scripts/tools/recipe_reply.lua
-- Author: bits-orio
-- License: MIT
--
-- The wire shape of a recipe_needs answer, built from a finished walk:
-- targets, rows, raw, the belt, alternatives and approx, every double through
-- bounded.round and the rows bounded by bytes. Rows, raw, alternatives or
-- approx that come out empty are left out, never sent as {}.
--
-- Rows are every crafted or stopped item, largest flow first, so a cut keeps
-- the plates and drops the smallest trickles. Raw rows and targets are never
-- cut. A raw row the game hands out, mined or pumped, says raw = "mined", and
-- the recipes that could make it anyway, crushing or melting, stay in
-- alternatives whatever the cut. The whole reply stays under REPLY_BUDGET:
-- the service hands the model at most max_tool_result_bytes of a result,
-- 4096 unless the operator changes it, and cuts the rest mid-JSON. A reply
-- cut here instead stays whole JSON, keeps its targets and raw rows, and
-- says what it left out with total beside shown.
--
-- The cut is found by encoding the finished reply for n rows, each with its
-- alternatives, and searching for the largest n that fits. It used to set
-- every row's alternatives aside before fitting any row, an upper bound that
-- left all twelve science packs on an endgame force with 0 of 65 rows shown
-- and 1174 bytes unused.
--
-- productivity says what the figures count: "research", or
-- "research+machine" once a row's machine adds productivity of its own
-- (recipe_machine.lua). A target the 80-item cap stopped says capped, not
-- raw: it has a recipe, the walk only stopped short of it.

local bounded      = require("scripts.tools.bounded")
local machine_pick = require("scripts.tools.machine_pick")
local yield        = require("scripts.tools.recipe_yield")

local REPLY_BUDGET = 4000
local SECONDS_PER_MINUTE = 60
local OMITTED_WHEN_EMPTY = { "approx", "raw", "alternatives", "rows" }

local M = {}

--- A double for the wire: two decimals from 1 up, more below so a small
--- machine count or trickle never rounds to a flat 0.
local function figure(value)
  local size = math.abs(value)
  local places = (size >= 1 and 2) or (size >= 0.01 and 3) or 4
  return bounded.round(value, places)
end

--- Belts of one flow, or nil for a fluid or when the game has no belt.
local function belts(kind, per_min, belt)
  if kind ~= "item" or not belt then return nil end
  return figure(per_min / SECONDS_PER_MINUTE / belt.per_sec)
end

local function by_flow(a, b)
  if a.per_min ~= b.per_min then return a.per_min > b.per_min end
  if a.name ~= b.name then return a.name < b.name end
  return a.kind < b.kind
end

--- The machine one crafted item runs on, written onto `out`.
local function machine_fields(out, ctx, pick, crafts_per_min)
  local machine = machine_pick.for_recipe(ctx, pick.proto)
  if not machine then return end
  out.machine = machine.name
  out.machines = figure(machine_pick.count(pick.proto, machine, crafts_per_min))
  if not machine.unlocked then out.unlocked = false end
end

local function row_of(node, ctx, belt)
  local row = { item = node.name, per_min = figure(node.per_min), belts = belts(node.kind, node.per_min, belt) }
  if node.kind == "fluid" then row.fluid = true end
  if node.role == "stop" then
    row.stop = true
  else
    row.recipe = node.pick.name
    machine_fields(row, ctx, node.pick, node.crafts)
  end
  return row
end

--- Crafted and stopped items, largest flow first, as wire rows.
local function rows_of(walk, ctx, belt)
  local kept = {}
  for _, node in ipairs(walk.nodes) do
    if node.role ~= "raw" then kept[#kept + 1] = node end
  end
  table.sort(kept, by_flow)
  local rows = {}
  for i, node in ipairs(kept) do rows[i] = row_of(node, ctx, belt) end
  return rows, kept
end

--- Raw inputs, largest first: items the game hands out, items with no recipe
--- the force can use, items a cap left unexpanded, and the share of an item
--- charged at a cycle.
local function raw_of(walk, belt)
  local list = {}
  for _, node in ipairs(walk.nodes) do
    if node.role == "raw" then
      list[#list + 1] = { kind = node.kind, name = node.name, per_min = node.per_min,
                          recipe_locked = node.locked or nil, capped = node.capped,
                          mined = node.mined ~= nil or nil }
    end
  end
  for key, per_min in pairs(walk.looped) do
    local kind, name = yield.split(key)
    list[#list + 1] = { kind = kind, name = name, per_min = per_min, cycle = true }
  end
  table.sort(list, by_flow)
  local raw = {}
  for i, one in ipairs(list) do
    raw[i] = { item = one.name, per_min = figure(one.per_min), belts = belts(one.kind, one.per_min, belt),
               fluid = one.kind == "fluid" or nil, recipe_locked = one.recipe_locked,
               capped = one.capped, cycle = one.cycle, raw = one.mined and "mined" or nil }
  end
  return raw
end

--- One asked-for target: its rate, and the machines making it alone. In
--- machines mode that count works back out to the one given.
local function target_of(target, node, ctx)
  local out = { item = target.name, per_min = figure(target.per_min) }
  if target.kind == "fluid" then out.fluid = true end
  if target.reason then
    out.machines, out.reason = figure(target.machines), target.reason
  elseif node.role == "craft" then
    machine_fields(out, ctx, node.pick, target.per_min / node.pick.net)
  elseif node.role == "stop" then
    out.stop = true
  elseif node.capped then
    out.capped = true
  else
    out.raw, out.recipe_locked = true, node.locked or nil
  end
  return out
end

local function targets_of(targets, walk, ctx)
  local by_key = {}
  for _, node in ipairs(walk.nodes) do by_key[node.key] = node end
  local out = {}
  for i, target in ipairs(targets) do
    out[i] = target_of(target, by_key[yield.key(target.kind, target.name)] or {}, ctx)
  end
  return out
end

--- Other recipes for the crafted items among the first `n` kept nodes, and
--- every recipe that could make a raw item the game hands out.
local function alternatives_of(walk, kept, n)
  local out = {}
  for i = 1, math.min(n, #kept) do
    local node = kept[i]
    if node.role == "craft" and #node.pick.alternatives > 0 then
      out[node.name] = node.pick.alternatives
    end
  end
  for _, node in ipairs(walk.nodes) do
    if node.mined and #node.mined.alternatives > 0 then
      out[node.name] = node.mined.alternatives
    end
  end
  return out
end

local function approx_of(set)
  local out = {}
  for reason in pairs(set) do out[#out + 1] = reason end
  table.sort(out)
  return out
end

local function belt_wire(belt)
  if not belt then return nil end
  local out = { name = belt.name, per_sec = figure(belt.per_sec) }
  if not belt.unlocked then out.unlocked = false end
  return out
end

--- Leaves out every list or map that came out empty. The engine's JSON
--- writer prints an empty table as {}, an object where a list belongs, and
--- an absent approx already says the figures are exact.
local function without_empties(reply)
  for _, key in ipairs(OMITTED_WHEN_EMPTY) do
    if next(reply[key]) == nil then reply[key] = nil end
  end
  return reply
end

--- What the figures count: research alone, or a machine's own as well.
local function productivity_of(walk)
  for _, node in ipairs(walk.nodes) do
    if node.role == "craft" and node.pick.machine_bonus then return "research+machine" end
  end
  return "research"
end

--- The finished reply with the first `n` rows and their alternatives.
local function shaped(parts, n)
  local rows = bounded.cut(parts.rows, n)
  return without_empties({
    found = true, force = parts.force, mode = parts.mode, belt = parts.belt,
    productivity = parts.productivity, approx = parts.approx,
    targets = parts.targets, raw = parts.raw,
    alternatives = alternatives_of(parts.walk, parts.kept, #rows),
    rows = rows, total = #parts.rows, shown = #rows,
  })
end

--- The most rows that fit REPLY_BUDGET with their alternatives, by binary
--- search over the encoded reply, which only grows with each row; 0 when
--- even the targets and raw rows alone do not fit.
local function rows_that_fit(parts)
  local function fits(n) return #helpers.table_to_json(shaped(parts, n)) <= REPLY_BUDGET end
  if fits(#parts.rows) then return #parts.rows end
  local low, high = 0, #parts.rows - 1
  while low < high do
    local mid = math.floor((low + high + 1) / 2)
    if fits(mid) then low = mid else high = mid - 1 end
  end
  return low
end

--- The reply for one finished walk. `input` is {force, mode, belt, ctx, walk,
--- targets}, targets as recipe_needs resolved them.
function M.answer(input)
  local walk, ctx, belt = input.walk, input.ctx, input.belt
  local rows, kept = rows_of(walk, ctx, belt)
  local parts = {
    force = input.force.name, mode = input.mode, belt = belt_wire(belt), productivity = productivity_of(walk),
    approx = approx_of(walk.approx), targets = targets_of(input.targets, walk, ctx), raw = raw_of(walk, belt),
    walk = walk, kept = kept, rows = rows,
  }
  return shaped(parts, rows_that_fit(parts))
end

M.REPLY_BUDGET = REPLY_BUDGET

return M
