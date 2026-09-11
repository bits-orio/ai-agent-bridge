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
    desc = "How much of one item one force has made: produced, consumed and net on one surface since a tick, over all qualities. since_tick 0 counts the whole game; a death's tick counts since then; a force's own start tick from a clock tool counts its whole run. The answer to how much or how many, never a rate. covered_ticks rounds up to whole samples; compare it with elapsed_ticks. Unknown surface: found=false.",
    params = {
      surface    = "string! surface name or index, e.g. nauvis",
      item       = "string! item prototype name",
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

local function production_since(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  if type(a.item) ~= "string" or a.item == "" then error("item is required", 0) end

  local since = tonumber(a.since_tick)
  if not since then error("since_tick is required", 0) end
  since = math.floor(since)
  if since < 0 then since = 0 end

  local elapsed = game.tick - since
  if elapsed < 0 then error("since_tick is in the future: " .. since .. " > " .. game.tick, 0) end
  if elapsed < 1 then elapsed = 1 end

  local window_name, window = flow.window_covering(elapsed)
  local ticks_per_sample = window.ticks / flow.SAMPLES_PER_WINDOW
  local samples = math.ceil(elapsed / ticks_per_sample)
  if samples < 1 then samples = 1 end
  if samples > flow.SAMPLES_PER_WINDOW then samples = flow.SAMPLES_PER_WINDOW end

  local stats = force.get_item_production_statistics(surface)
  local spec = { item = a.item, qualities = flow.quality_names(), window = window }
  local produced = sum_samples(stats, spec, "input", samples)
  local consumed = sum_samples(stats, spec, "output", samples)
  local covered = math.floor(samples * ticks_per_sample)

  -- The three numbers the question asked for come first, ahead of the method
  -- fields, so a reader who sees only the head of this reply still sees the
  -- answer. Every other engine tool leads with its headline figure the same way.
  return {
    found = true,
    force = force.name, surface = surface.name, item = a.item,
    produced = bounded.round(produced, 2),
    consumed = bounded.round(consumed, 2),
    net = bounded.round(produced - consumed, 2),
    all_qualities = true,
    since_tick = since, now_tick = game.tick,
    elapsed_ticks = elapsed, covered_ticks = covered,
    covers_full_period = covered >= elapsed,
    window = window_name, samples = samples,
  }
end

M.functions = { production_since = production_since }

return M
