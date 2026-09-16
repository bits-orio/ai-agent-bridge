-- AI Agent Bridge - scripts/tools/built.lua
-- Author: bits-orio
-- License: MIT
--
-- built: how many entities one force has built, and how many it has mined
-- back (deconstructed), over every surface unless one is named.
-- LuaForce::get_entity_build_count_statistics(surface) reads a
-- LuaFlowStatistics the engine already keeps: input_counts are entities this
-- force has built, keyed by prototype; output_counts are entities it has
-- mined back, the same input/output convention kills.lua's own header
-- documents for this same class of statistics.
--
-- API names verified against /home/shobhitg/factorio/doc-html/runtime-api.json
-- (2.0.77):
--   LuaForce::get_entity_build_count_statistics(surface) -> LuaFlowStatistics,
--     `surface` a required SurfaceIdentification, not optional: same as
--     get_kill_count_statistics, so "every surface" means the same walk and
--     sum kills.lua and production_since.lua both already do (the shared
--     walk lives in scripts/tools/flow.lua's own surfaces_for/count_totals).
--   LuaFlowStatistics::input_counts, output_counts: dictionary[string ->
--     union[uint64, double]], "indexed by prototype name".

local force_lookup = require("scripts.tools.force_lookup")
local bounded       = require("scripts.tools.bounded")
local flow          = require("scripts.tools.flow")

local DEFAULT_TOP = 10
local MAX_TOP = 25
local MAX_BREAKDOWN = 5

local M = {}

M.manifest = {
  built = {
    desc = "How many entities one force has built and how many it has mined back, counted as events over the whole game so far, not what stands on the map now: how many of something a force HAS is entity_count, which built the most of something is entity_count with all=true. Over every surface unless one is named. top names the kinds built most, largest first; distinct_kinds says if there are more. all=true answers every force that has players in one call, built and mined only, no top list: use it for any each-team or every-force question instead of one call per force. Unknown surface: found=false.",
    tier = "1",
    params = {
      surface = "string surface name or index, e.g. nauvis; omit for every surface",
      limit   = "integer kinds-built rows, default " .. DEFAULT_TOP .. ", max " .. MAX_TOP,
      all     = "boolean default false: one row per force with players, built and mined only; force and surface ignored",
    },
  },
}

local function stats_of(force)
  return function(surface) return force.get_entity_build_count_statistics(surface) end
end

--- The built-prototype counts, largest first, name the tiebreak: the same
--- shape kills.lua's own top_rows takes.
local function top_rows(by_name, limit)
  local rows = {}
  for name, count in pairs(by_name) do rows[#rows + 1] = { name = name, count = count } end
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.name < y.name
  end)
  local shown = bounded.cut(rows, limit)
  for _, row in ipairs(shown) do row.count = bounded.round(row.count, 0) end
  return shown
end

local function distinct_of(by_name)
  local n = 0
  for _ in pairs(by_name) do n = n + 1 end
  return n
end

--- Sorts a {surface, input, output} breakdown largest-built-first, cuts it
--- to MAX_BREAKDOWN and renames it onto the wire as {surface, built, mined}:
--- production_since.lua's own MAX_BREAKDOWN=5 bound, mirrored rather than
--- imported since that file keeps its copy private to itself.
local function surface_breakdown(rows)
  table.sort(rows, function(x, y)
    if x.input ~= y.input then return x.input > y.input end
    return x.surface < y.surface
  end)
  local shown = bounded.cut(rows, MAX_BREAKDOWN)
  local out = {}
  for i, row in ipairs(shown) do
    out[i] = { surface = row.surface, built = bounded.round(row.input, 0), mined = bounded.round(row.output, 0) }
  end
  return out
end

-- all=true is the every-team question in one call. The rows are forces that
-- have players, the same rule current_research's own sweep follows: an empty
-- slot has built nothing anywhere.
local function built_all()
  local surfaces = flow.surfaces_for({})
  local rows = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then
      local input_total, output_total = flow.count_totals(surfaces, stats_of(force))
      -- Sorted on the raw number before rounding, the same discipline
      -- envelope.lua's own header explains: rounding for the wire must
      -- never touch which row is the leader before a cut.
      rows[#rows + 1] = { force = force.name, built = input_total, mined = output_total }
    end
  end
  table.sort(rows, function(x, y)
    if x.built ~= y.built then return x.built > y.built end
    return x.force < y.force
  end)
  local shown = bounded.fit(bounded.cut(rows, bounded.MAX_FORCES))
  for _, row in ipairs(shown) do
    row.built = bounded.round(row.built, 0)
    row.mined = bounded.round(row.mined, 0)
  end
  return { total = #rows, shown = #shown, forces = shown }
end

local function built(a)
  if a.all == true then return built_all() end
  local force = force_lookup.require_force(a.force)
  local surfaces, miss = flow.surfaces_for(a)
  if not surfaces then
    miss.force = force.name
    return miss
  end

  local input_total, output_total, input_by_name, _output_by_name, surface_rows =
    flow.count_totals(surfaces, stats_of(force))

  return {
    found = true,
    force = force.name,
    surface = #surfaces == 1 and surfaces[1].name or "all",
    built = bounded.round(input_total, 0),
    mined = bounded.round(output_total, 0),
    distinct_kinds = distinct_of(input_by_name),
    top = top_rows(input_by_name, bounded.limit(a.limit, DEFAULT_TOP, MAX_TOP)),
    surfaces_counted = #surface_rows,
    surfaces = surface_breakdown(surface_rows),
  }
end

M.functions = { built = built }

return M
