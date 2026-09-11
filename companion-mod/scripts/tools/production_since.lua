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

local M = {}

M.manifest = {
  production_since = {
    desc = "How much of one item one force has made: produced, consumed and net since a tick, over all qualities. Leave surface out to count every surface the force has made it on, the right call for a force's total, with a per-surface breakdown; name a surface to count that one. since_tick 0 counts the whole game, exactly; a death's tick counts since then; a force's own start tick from a clock tool counts its whole run. The answer to how much or how many, never a rate. Unknown surface: found=false.",
    params = {
      surface    = "string surface name or index, e.g. nauvis; omit for every surface",
      item       = "string! item prototype name",
      since_tick = "integer! tick to count from, 0 for the whole game, not after the current tick",
    },
  },
}

local MAX_BREAKDOWN = 5

local function sum_samples(stats, spec, category, samples)
  local total = 0
  for index = 1, samples do
    spec.sample = index
    total = total + flow.item_flow(stats, spec, category)
  end
  return total
end

--- The engine's cumulative counters for one item on one surface, summed
--- over every quality: exact, one lookup per quality, and what "since the
--- start of the game" means.
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
    local all = {}
    for _, surface in pairs(game.surfaces) do
      if surface.valid then all[#all + 1] = surface end
    end
    table.sort(all, function(x, y) return x.name < y.name end)
    return all, nil
  end
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return nil, miss
  end
  return { surface }, nil
end

local function production_since(a)
  local force = force_lookup.require_force(a.force)
  if type(a.item) ~= "string" or a.item == "" then error("item is required", 0) end
  local surfaces, miss = surfaces_for(a, force)
  if not surfaces then return miss end

  local since = tonumber(a.since_tick)
  if not since then error("since_tick is required", 0) end
  since = math.floor(since)
  if since < 0 then since = 0 end

  local elapsed = game.tick - since
  if elapsed < 0 then error("since_tick is in the future: " .. since .. " > " .. game.tick, 0) end
  if elapsed < 1 then elapsed = 1 end

  local qualities = flow.quality_names()
  local exact = since == 0
  local window_name, window, ticks_per_sample, samples
  if not exact then
    window_name, window = flow.window_covering(elapsed)
    ticks_per_sample = window.ticks / flow.SAMPLES_PER_WINDOW
    samples = math.ceil(elapsed / ticks_per_sample)
    if samples < 1 then samples = 1 end
    if samples > flow.SAMPLES_PER_WINDOW then samples = flow.SAMPLES_PER_WINDOW end
  end

  -- One row per surface the force has ever made or used the item on. The
  -- lifetime counters decide that first, so the sampled read, up to 300
  -- lookups per quality, runs only where there is something to count.
  local rows, produced, consumed = {}, 0, 0
  for _, surface in ipairs(surfaces) do
    local stats = force.get_item_production_statistics(surface)
    local life_in, life_out = lifetime(stats, a.item, qualities)
    if life_in > 0 or life_out > 0 or #surfaces == 1 then
      local here_in, here_out = life_in, life_out
      if not exact then
        local spec = { item = a.item, qualities = qualities, window = window }
        here_in = sum_samples(stats, spec, "input", samples)
        here_out = sum_samples(stats, spec, "output", samples)
      end
      produced, consumed = produced + here_in, consumed + here_out
      rows[#rows + 1] = { surface = surface.name, produced = here_in, consumed = here_out }
    end
  end
  table.sort(rows, function(x, y) return x.produced > y.produced end)
  local shown = bounded.cut(rows, MAX_BREAKDOWN)
  for _, row in ipairs(shown) do
    row.produced = bounded.round(row.produced, 2)
    row.consumed = bounded.round(row.consumed, 2)
  end

  -- The three numbers the question asked for come first, ahead of the method
  -- fields, so a reader who sees only the head of this reply still sees the
  -- answer. Every other engine tool leads with its headline figure the same way.
  local out = {
    found = true,
    force = force.name, item = a.item,
    surface = #surfaces == 1 and surfaces[1].name or "all",
    produced = bounded.round(produced, 2),
    consumed = bounded.round(consumed, 2),
    net = bounded.round(produced - consumed, 2),
    all_qualities = true,
    since_tick = since, now_tick = game.tick,
    elapsed_ticks = elapsed,
    surfaces_counted = #rows, surfaces = shown,
  }
  if exact then
    out.method = "lifetime counters, exact"
    out.covers_full_period = true
  else
    local covered = math.floor(samples * ticks_per_sample)
    out.method = "flow samples"
    out.covered_ticks = covered
    out.covers_full_period = covered >= elapsed
    out.window = window_name
    out.samples = samples
  end
  return out
end

M.functions = { production_since = production_since }

return M
