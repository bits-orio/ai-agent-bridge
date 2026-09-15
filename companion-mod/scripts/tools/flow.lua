-- AI Agent Bridge - scripts/tools/flow.lua
-- Author: bits-orio
-- License: MIT
--
-- Shared window resolution for every tool that reads LuaFlowStatistics, the
-- quality summing all three of them need, and the sample arithmetic
-- production_since needs. Surfaces are not resolved here: every surface-taking
-- tool goes through scripts/tools/surface_lookup.lua, which answers a miss with
-- found = false rather than an error.
--
-- Verified against the official LuaFlowStatistics docs (fetched 2026-09-10),
-- not from memory:
--   get_flow_count{name, category, precision_index, sample_index?, count?}
--   "Each precision level contains 300 samples of data so at a precision of
--    1 minute, each sample contains data averaged across 60s / 300 = 0.2s
--    = 12 ticks."
--   sample_index "must be between 1 and 300 where 1 is the most recent sample
--    and 300 is the oldest."
--   count "If true, the count of items/fluids/entities is returned instead of
--    the per-time-frame value."
--   "All return values are normalized to be per-tick for electric networks and
--    per-minute for all other types."
-- and, for the quality summing below:
--   get_flow_count's name is a FlowStatisticsID, which for item statistics is
--   an ItemWithQualityID. A bare prototype name is the NORMAL quality alone
--   ("The prototype name. Normal quality will be used."), so reading one item
--   means one call per quality with the ItemIDAndQualityIDPair form
--   {name = item, quality = quality}. LuaPrototypes::quality is the
--   LuaCustomTable[string -> LuaQualityPrototype] of the qualities a game has.

local SAMPLES_PER_WINDOW = 300
local TICKS_PER_SECOND = 60

-- Every precision the engine offers, in seconds. Built into WINDOWS below by
-- looking each name up in defines, so a Factorio build that lacks one of them
-- simply does not offer it rather than registering a window whose precision is
-- nil and failing later inside get_flow_count.
local SECONDS = {
  five_seconds            = 5,
  one_minute              = 60,
  ten_minutes             = 600,
  one_hour                = 3600,
  ten_hours               = 36000,
  fifty_hours             = 180000,
  two_hundred_fifty_hours = 900000,
  one_thousand_hours      = 3600000,
}

local WINDOWS = {}
local ORDER = {}
for name, seconds in pairs(SECONDS) do
  local precision = defines.flow_precision_index[name]
  if precision then
    WINDOWS[name] = { precision = precision, ticks = seconds * TICKS_PER_SECOND }
    ORDER[#ORDER + 1] = name
  end
end
table.sort(ORDER, function(x, y) return SECONDS[x] < SECONDS[y] end)

local M = {}

M.SAMPLES_PER_WINDOW = SAMPLES_PER_WINDOW

-- "five_seconds, one_minute, ..." for tool manifests, so a description can
-- never drift out of step with what require_window actually accepts.
M.window_names = table.concat(ORDER, ", ")

--- Whether `name` is a window this game offers, without erroring. A sweep
--- metric checks its window argument with this so a wrong one comes back as
--- a found=false card the model can read, not as a provider_error.
function M.has_window(name)
  return type(name) == "string" and WINDOWS[name] ~= nil
end

--- Resolves a window name to {precision, ticks}, or error()s with the full
--- list of names, which scripts/probe.lua turns into a provider_error reply.
function M.require_window(name)
  local window = WINDOWS[name]
  if not window then
    error("unknown window: " .. tostring(name) .. " (want one of " .. M.window_names .. ")", 0)
  end
  return window
end

--- Every quality name this game has, sorted, as a fresh list. Read once per
--- tool call and passed down, because a 300-sample sum would otherwise rebuild
--- it 300 times. A game with no quality prototypes at all still reads its
--- normal-quality flow.
function M.quality_names()
  local names = {}
  for name in pairs(prototypes.quality or {}) do names[#names + 1] = name end
  table.sort(names)
  if #names == 0 then names[1] = "normal" end
  return names
end

--- One item's flow, summed over every quality in spec.qualities. spec is
--- {item, qualities, window, sample}: with no sample this is the window average
--- the engine normalises per minute, with one it is that sample's item count.
---
--- The summing is the whole point. A force that makes the same plate at five
--- qualities would otherwise report only the normal-quality part of its own
--- production, with nothing in the reply to say a filter had been applied.
function M.item_flow(stats, spec, category)
  local total = 0
  for _, quality in ipairs(spec.qualities) do
    total = total + stats.get_flow_count{
      name            = { name = spec.item, quality = quality },
      category        = category,
      precision_index = spec.window.precision,
      sample_index    = spec.sample,
      count           = spec.sample ~= nil,
    }
  end
  return total
end

--- The smallest window whose span covers elapsed_ticks, as name, window. Falls
--- back to the longest window the engine offers when nothing covers it, so a
--- very old tick still answers with the best history that exists.
function M.window_covering(elapsed_ticks)
  for _, name in ipairs(ORDER) do
    if WINDOWS[name].ticks >= elapsed_ticks then
      return name, WINDOWS[name]
    end
  end
  local longest = ORDER[#ORDER]
  return longest, WINDOWS[longest]
end

-- ── shared helpers for the count-style statistics (kills, build) ────────
--
-- scripts/tools/kills.lua and scripts/tools/built.lua both read a force's own
-- LuaFlowStatistics through a per-surface method (get_kill_count_statistics,
-- get_entity_build_count_statistics), and unlike get_evolution_factor neither
-- one takes an optional surface: verified against the 2.0.77 dump,
-- /home/shobhitg/factorio/doc-html/runtime-api.json, `surface` is a required
-- SurfaceIdentification on both. So "leave surface out to count every
-- surface" means the same walk-and-sum production_since.lua already does for
-- item counters, not a single call with surface omitted, and that walk
-- belongs here once rather than being written twice by two files that would
-- otherwise drift apart the way CONTEXT.md warns entity_count.lua's own walk
-- already did once.

local surface_lookup = require("scripts.tools.surface_lookup")

--- Every valid surface, name order: the same "no surface named means every
--- surface" rule production_since.lua's own surfaces_for() follows. Kept
--- private; callers reach it only through M.surfaces_for below.
local function every_surface()
  local all = {}
  for _, surface in pairs(game.surfaces) do
    if surface.valid then all[#all + 1] = surface end
  end
  table.sort(all, function(x, y) return x.name < y.name end)
  return all
end

--- Which surfaces a count-style tool counts over: the one named, or every
--- surface when none was. Returns the surfaces to walk, or nil plus a
--- ready-to-return miss reply on an unknown name; the caller still owns
--- adding its own `force` field to that miss, the same division
--- production.lua and entity_count.lua already keep with surface_lookup.
function M.surfaces_for(a)
  if a.surface == nil or a.surface == "" or a.surface == "all" then
    return every_surface(), nil
  end
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then return nil, miss end
  return { surface }, nil
end

--- Sums a force's own count-style LuaFlowStatistics over `surfaces`: the
--- input and output totals, each side's counts merged by prototype name
--- across every surface counted (so a kind killed on two surfaces is one
--- kind, not two), and one {surface, input, output} row per surface that had
--- anything, or every surface when there is only one to report on. Every
--- number stays a raw Lua number here; rounding and bounding for the wire
--- are each caller's own job, the same split production_since.lua keeps
--- between summing and formatting.
---
--- `get_stats(surface)` is the caller's own force.get_kill_count_statistics
--- or force.get_entity_build_count_statistics: this file names neither, only
--- that both hand back a LuaFlowStatistics (input_counts, output_counts).
function M.count_totals(surfaces, get_stats)
  local input_total, output_total = 0, 0
  local input_by_name, output_by_name = {}, {}
  local rows = {}
  for _, surface in ipairs(surfaces) do
    local stats = get_stats(surface)
    local here_in, here_out = 0, 0
    for name, count in pairs(stats.input_counts or {}) do
      here_in = here_in + count
      input_by_name[name] = (input_by_name[name] or 0) + count
    end
    for name, count in pairs(stats.output_counts or {}) do
      here_out = here_out + count
      output_by_name[name] = (output_by_name[name] or 0) + count
    end
    input_total = input_total + here_in
    output_total = output_total + here_out
    if here_in > 0 or here_out > 0 or #surfaces == 1 then
      rows[#rows + 1] = { surface = surface.name, input = here_in, output = here_out }
    end
  end
  return input_total, output_total, input_by_name, output_by_name, rows
end

return M
