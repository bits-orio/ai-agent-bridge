-- AI Agent Bridge - scripts/tools/production_since.lua
-- Author: bits-orio
-- License: MIT
--
-- production_since: how much of one item a force made and used since a given
-- tick. The service pairs it with a tick out of the event history, so
-- "how much iron have we made since I last died" is one tool call.
--
-- Method, and its approximation. The engine keeps 300 samples per precision
-- window, so each sample of the one_minute window covers 12 ticks, each sample
-- of the ten_minutes window 120 ticks, and so on (quoted from the docs in
-- scripts/tools/flow.lua). This picks the smallest window whose span covers
-- the elapsed ticks, then sums get_flow_count{..., sample_index = i,
-- count = true} over the most recent samples. Two consequences worth stating:
-- the sum is quantised up to a whole number of samples, so covered_ticks is
-- always greater than or equal to elapsed_ticks and the reply reports both;
-- and the newest sample is still filling, so a call two seconds after an event
-- reads a sample that also holds a few ticks from before it. Both errors
-- shrink as the period grows.
--
-- since_tick 0 reads the engine's lifetime counters instead. Statistics are
-- kept per surface, so those totals cover the surfaces that still exist: a
-- scrapped platform or deleted surface likely takes its counters with it
-- (review finding T7, to be settled on a live save), which is why the
-- description no longer says "exactly".
--
-- `items` asks for up to MAX_ITEMS items in one call, one row each: "how
-- much science have I consumed" was eleven calls, one per pack. The sampled
-- path reads 300 samples per quality per category per surface per item, so
-- a list on a sampled window is held to MAX_SAMPLE_READS reads in all.
--
-- Every sample is summed over every quality the game has, so the figure is the
-- force's whole production of that item rather than its normal-quality part
-- (scripts/tools/flow.lua carries the quoted documentation for why a bare item
-- name means normal quality alone). That is one get_flow_count per sample per
-- quality per category, so a 300-sample read on a five-quality game makes three
-- thousand of them. Each is a counter lookup inside the engine, and the
-- alternative is an answer that is quietly a fifth of the truth.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local flow           = require("scripts.tools.flow")
local bounded        = require("scripts.tools.bounded")
local name_lookup    = require("scripts.tools.name_lookup")

local MAX_BREAKDOWN = 5
local MAX_ITEMS = 12
local MAX_SAMPLE_READS = 60000

local M = {}

M.manifest = {
  production_since = {
    desc = "How much of an item one force has made: produced, consumed and net since a tick, over all qualities. Leave surface out for the force's total over every surface it has made it on, with a per-surface breakdown. since_tick 0 counts the whole game, from lifetime counters that cover surfaces that still exist; a death's tick counts since then. items takes up to " .. MAX_ITEMS .. " at once, one row each. How much or how many, never a rate. Unknown surface: found=false.",
    params = {
      surface    = "string surface name or index, e.g. nauvis; omit for every surface",
      item       = "string item prototype name; give this or items",
      items      = "list<string> up to " .. MAX_ITEMS .. " item prototype names, one row each",
      since_tick = "integer! tick to count from, 0 for the whole game, not after the current tick",
    },
  },
}

local function sum_samples(stats, spec, category, samples)
  local total = 0
  for index = 1, samples do
    spec.sample = index
    total = total + flow.item_flow(stats, spec, category)
  end
  return total
end

--- The engine's cumulative counters for one item on one surface, summed
--- over every quality: one lookup per quality, and what "since the start of
--- the game" means.
local function lifetime(stats, item, qualities)
  local produced, consumed = 0, 0
  for _, quality in ipairs(qualities) do
    local id = { name = item, quality = quality }
    produced = produced + stats.get_input_count(id)
    consumed = consumed + stats.get_output_count(id)
  end
  return produced, consumed
end

--- Which surfaces to count: the one asked for, or every surface in the game
--- when none was, so a force's total never depends on the model guessing
--- where that force lives.
local function surfaces_for(a, force)
  if a.surface == nil or a.surface == "" or a.surface == "all" then
    return flow.surfaces_for({}), nil
  end
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return nil, miss
  end
  return { surface }, nil
end

--- How the period is read: lifetime counters from tick 0, else the samples
--- of the smallest window that covers it.
local function plan_of(a)
  local since = tonumber(a.since_tick)
  if not since then error("since_tick is required", 0) end
  since = math.max(0, math.floor(since))
  local elapsed = game.tick - since
  if elapsed < 0 then error("since_tick is in the future: " .. since .. " > " .. game.tick, 0) end
  local plan = { since = since, elapsed = math.max(elapsed, 1), exact = since == 0, qualities = flow.quality_names() }
  if not plan.exact then
    plan.window_name, plan.window = flow.window_covering(plan.elapsed)
    plan.ticks_per_sample = plan.window.ticks / flow.SAMPLES_PER_WINDOW
    plan.samples = math.min(flow.SAMPLES_PER_WINDOW, math.max(1, math.ceil(plan.elapsed / plan.ticks_per_sample)))
  end
  return plan
end

--- One item counted over `surfaces`: produced, consumed, and a row per
--- surface the force ever made or used it on. The lifetime counters decide
--- that first, so the sampled read, up to 300 lookups per quality, runs only
--- where there is something to count.
local function count_item(force, surfaces, item, plan)
  local rows, produced, consumed = {}, 0, 0
  for _, surface in ipairs(surfaces) do
    local stats = force.get_item_production_statistics(surface)
    local here_in, here_out = lifetime(stats, item, plan.qualities)
    if here_in > 0 or here_out > 0 or #surfaces == 1 then
      if not plan.exact then
        local spec = { item = item, qualities = plan.qualities, window = plan.window }
        here_in = sum_samples(stats, spec, "input", plan.samples)
        here_out = sum_samples(stats, spec, "output", plan.samples)
      end
      produced, consumed = produced + here_in, consumed + here_out
      rows[#rows + 1] = { surface = surface.name, produced = here_in, consumed = here_out }
    end
  end
  return produced, consumed, rows
end

local function breakdown(rows)
  table.sort(rows, function(x, y) return x.produced > y.produced end)
  local shown = bounded.cut(rows, MAX_BREAKDOWN)
  for _, row in ipairs(shown) do
    row.produced = bounded.round(row.produced, 2)
    row.consumed = bounded.round(row.consumed, 2)
  end
  return shown
end

--- The method fields every reply ends with.
local function method_of(out, plan)
  out.since_tick, out.now_tick, out.elapsed_ticks = plan.since, game.tick, plan.elapsed
  if plan.exact then
    out.method = "lifetime counters, surfaces that still exist"
    out.covers_full_period = true
  else
    local covered = math.floor(plan.samples * plan.ticks_per_sample)
    out.method = "flow samples"
    out.covered_ticks = covered
    out.covers_full_period = covered >= plan.elapsed
    out.window = plan.window_name
    out.samples = plan.samples
  end
  return out
end

--- The one-item reply. The three numbers the question asked for come first,
--- ahead of the method fields, so a reader who sees only the head of this
--- reply still sees the answer.
local function one_item(force, surfaces, item, plan)
  local produced, consumed, rows = count_item(force, surfaces, item, plan)
  return method_of({
    found = true, force = force.name, item = item,
    surface = #surfaces == 1 and surfaces[1].name or "all",
    produced = bounded.round(produced, 2), consumed = bounded.round(consumed, 2),
    net = bounded.round(produced - consumed, 2), all_qualities = true,
    surfaces_counted = #rows, surfaces = breakdown(rows),
  }, plan)
end

--- A list on a sampled window is refused before any sample is read when it
--- would pass MAX_SAMPLE_READS, rather than stall the game thread.
local function too_costly(items, surfaces, plan)
  if plan.exact then return nil end
  local reads = #items * #surfaces * plan.samples * #plan.qualities * 2
  if reads <= MAX_SAMPLE_READS then return nil end
  return "counting " .. #items .. " items since tick " .. plan.since .. " would read up to " .. reads ..
         " flow samples, past the " .. MAX_SAMPLE_READS .. " one call may: name a surface, ask for fewer items, or use since_tick 0"
end

--- The list reply: one row per item, the same figures one_item sends.
local function many_items(force, surfaces, items, plan)
  local problem = too_costly(items, surfaces, plan)
  if problem then error(problem, 0) end
  local rows = {}
  for i, item in ipairs(items) do
    local produced, consumed, per_surface = count_item(force, surfaces, item, plan)
    rows[i] = { item = item, produced = bounded.round(produced, 2), consumed = bounded.round(consumed, 2),
                net = bounded.round(produced - consumed, 2), surfaces_counted = #per_surface }
  end
  return method_of({
    found = true, force = force.name, surface = #surfaces == 1 and surfaces[1].name or "all",
    all_qualities = true, items = rows,
  }, plan)
end

--- item or items as a list of names, at least one, at most MAX_ITEMS.
local function names_of(a)
  if a.items ~= nil then
    local list = type(a.items) == "table" and a.items or { a.items }
    if #list == 0 then error("items is empty: name at least one item", 0) end
    if #list > MAX_ITEMS then error("at most " .. MAX_ITEMS .. " items in one call", 0) end
    return list, true
  end
  if type(a.item) ~= "string" or a.item == "" then error("item is required", 0) end
  return { a.item }, false
end

local function production_since(a)
  local force = force_lookup.require_force(a.force)
  local asked, listed = names_of(a)
  local surfaces, miss = surfaces_for(a, force)
  if not surfaces then return miss end
  local plan = plan_of(a)
  local items, resolved, missing = name_lookup.resolve_all(asked, "item")
  if missing and not listed then
    return { found = false, force = force.name, item = a.item, suggestions = missing[a.item],
             reason = "no item prototype by that name: use the internal name, for example iron-plate" }
  end
  if not listed then
    local out = one_item(force, surfaces, items[1], plan)
    out.resolved_from = resolved and resolved[1] or nil
    return out
  end
  local known = {}
  for i = 1, #asked do if items[i] then known[#known + 1] = items[i] end end
  local out = many_items(force, surfaces, known, plan)
  out.resolved_from, out.unknown = resolved, missing
  return out
end

M.functions = { production_since = production_since }

return M
