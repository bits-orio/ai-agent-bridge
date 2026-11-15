-- AI Agent Bridge - scripts/tools/kills.lua
-- Author: bits-orio
-- License: MIT
--
-- kills: how many enemies one force has killed, and how many of its own it
-- has lost to them. LuaForce::get_kill_count_statistics(surface) reads a
-- LuaFlowStatistics the engine already keeps, cheap to sum however many
-- surfaces get counted: input_counts are kills this force made, keyed by the
-- killed prototype; output_counts are this force's own losses.
--
-- API names verified against /home/shobhitg/factorio/doc-html/runtime-api.json
-- (2.0.77):
--   LuaForce::get_kill_count_statistics(surface) -> LuaFlowStatistics,
--     `surface` a required SurfaceIdentification, not optional: unlike
--     LuaForce::get_evolution_factor there is no force-wide reading, so
--     "every surface" here always means walking and summing them, the same
--     production_since.lua already does for item counters (the shared walk
--     lives in scripts/tools/flow.lua's own surfaces_for/count_totals).
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
  kills = {
    desc = "What one force has destroyed and lost, over every surface unless one is named. kills counts everything destroyed, split into enemies (biters, spitters, spawners, worms, demolishers, pentapods), trees_and_rocks and other (asteroids, buildings); quote enemies for enemies killed. top names the kinds killed most. all=true: one row per force with players, kills, enemies and losses. Unknown surface: found=false.",
    tier = "1",
    params = {
      surface = "string surface name or index, e.g. nauvis; omit for every surface",
      limit   = "integer kinds-killed rows, default " .. DEFAULT_TOP .. ", max " .. MAX_TOP,
      all     = "boolean default false: one row per force with players, kills, enemies and losses; force and surface ignored",
      offset  = bounded.OFFSET_PARAM,
    },
  },
}

local function stats_of(force)
  return function(surface) return force.get_kill_count_statistics(surface) end
end

--- The killed-prototype counts, largest first, name the tiebreak: the same
--- shape rockets.lua's own item breakdown takes for what went up in a
--- rocket.
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

--- Sorts a {surface, input, output} breakdown largest-kills-first, cuts it to
--- MAX_BREAKDOWN and renames it onto the wire as {surface, kills, losses}:
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
    out[i] = { surface = row.surface, kills = bounded.round(row.input, 0), losses = bounded.round(row.output, 0) }
  end
  return out
end

-- What a killed prototype was, by its type. Kill statistics are keyed by
-- the killed entity's prototype name, and they count everything the force
-- destroyed: a tree felled by a car or a rock blown up counted as an enemy
-- killed. Enemy types verified against data/base and data/space-age 2.1.23
-- (unit, unit-spawner, turret for worms, segmented-unit for demolishers,
-- spider-unit for pentapods); the force's own turrets are ammo-turret,
-- electric-turret and fluid-turret, never plain turret.
local ENEMY_TYPES = { unit = true, ["unit-spawner"] = true, turret = true,
                      ["segmented-unit"] = true, ["spider-unit"] = true }
local SCENERY_TYPES = { tree = true, ["simple-entity"] = true, plant = true }

--- Kill counts by name split into enemies, trees_and_rocks and other.
local function classified(by_name)
  local out = { enemies = 0, trees_and_rocks = 0, other = 0 }
  for name, count in pairs(by_name) do
    local proto = prototypes.entity[name]
    local kind = proto and proto.type
    if ENEMY_TYPES[kind] then out.enemies = out.enemies + count
    elseif SCENERY_TYPES[kind] then out.trees_and_rocks = out.trees_and_rocks + count
    else out.other = out.other + count end
  end
  return out
end

-- all=true is the every-team question in one call. The rows are forces that
-- have players, the same rule current_research's own sweep follows: an empty
-- slot has killed nothing anywhere.
local function kills_all(a)
  local surfaces = flow.surfaces_for({})
  local rows = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then
      local input_total, output_total, input_by_name = flow.count_totals(surfaces, stats_of(force))
      -- Sorted on the raw number before rounding, the same discipline
      -- envelope.lua's own header explains: rounding for the wire must
      -- never touch which row is the leader before a cut.
      rows[#rows + 1] = { force = force.name, kills = input_total, losses = output_total,
                          enemies = classified(input_by_name).enemies }
    end
  end
  table.sort(rows, function(x, y)
    if x.kills ~= y.kills then return x.kills > y.kills end
    return x.force < y.force
  end)
  for _, row in ipairs(rows) do
    row.kills = bounded.round(row.kills, 0)
    row.enemies = bounded.round(row.enemies, 0)
    row.losses = bounded.round(row.losses, 0)
  end
  return bounded.page(rows, a.offset, function(page) return { forces = page } end, bounded.MAX_FORCES)
end

local function kills(a)
  if a.all == true then return kills_all(a) end
  local force = force_lookup.require_force(a.force)
  local surfaces, miss = flow.surfaces_for(a)
  if not surfaces then
    miss.force = force.name
    return miss
  end

  local input_total, output_total, input_by_name, _output_by_name, surface_rows =
    flow.count_totals(surfaces, stats_of(force))
  local split = classified(input_by_name)

  return {
    found = true,
    force = force.name,
    surface = #surfaces == 1 and surfaces[1].name or "all",
    kills = bounded.round(input_total, 0),
    enemies = bounded.round(split.enemies, 0),
    trees_and_rocks = bounded.round(split.trees_and_rocks, 0),
    other = bounded.round(split.other, 0),
    losses = bounded.round(output_total, 0),
    distinct_kinds = distinct_of(input_by_name),
    top = top_rows(input_by_name, bounded.limit(a.limit, DEFAULT_TOP, MAX_TOP)),
    surfaces_counted = #surface_rows,
    surfaces = surface_breakdown(surface_rows),
  }
end

--- Enemies one force has killed over `surfaces`, for a caller that wants the
--- one figure (force_card) and not the breakdown. Same classification kills
--- itself quotes, so the two can never disagree.
function M.enemies_killed(force, surfaces)
  local _, _, by_name = flow.count_totals(surfaces, stats_of(force))
  return classified(by_name).enemies
end

M.functions = { kills = kills }

return M
