-- AI Agent Bridge - scripts/tools/fluid_rate.lua
-- Author: bits-orio
-- License: MIT
--
-- fluid_rate: how fast one fluid is being made and used, mirroring
-- production.lua's item_rate exactly except for the one thing a fluid does
-- not have: quality. FluidID (concepts.FluidID) is a bare name, a
-- LuaFluidPrototype or a Fluid value, none of them quality-qualified, unlike
-- ItemWithQualityID. So this reads get_flow_count once per category with the
-- fluid name straight, never flow.item_flow's per-quality summing loop, which
-- would ask the engine for a "normal quality" of a fluid that has none.
--
-- API names verified against /home/shobhitg/factorio/doc-html/runtime-api.json
-- (2.0.77):
--   LuaForce::get_fluid_production_statistics(surface) -> LuaFlowStatistics,
--     `surface` a required SurfaceIdentification.
--   LuaFlowStatistics::get_flow_count{name, category, precision_index,
--     sample_index?, count?}: `name` is a FlowStatisticsID, which for fluid
--     production statistics is a FluidID (concepts.FlowStatisticsID: "Used
--     with fluid production statistics"), and FluidID's three forms (string,
--     LuaFluidPrototype, Fluid) carry no quality field at all
--     (concepts.FluidID).

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local flow           = require("scripts.tools.flow")
local bounded        = require("scripts.tools.bounded")

local M = {}

M.manifest = {
  fluid_rate = {
    desc = "How fast one fluid is being made and used: production and consumption rate for one force on one surface, in units per minute, averaged over the window. A rate, never a total. all=true answers every force that has players in one call, one row each, summed over every surface, largest producer first: use it for any each-team or every-force question instead of one call per force. Unknown surface: found=false.",
    tier = "1",
    params = {
      surface = "string surface name or index, e.g. nauvis; required unless all=true",
      fluid   = "string! fluid prototype name, e.g. crude-oil, water",
      window  = "string! the average's span, one of " .. flow.window_names .. "; the shortest that covers the question, one_minute for right now",
      all     = "boolean default false: one row per force with players, summed over every surface; force and surface ignored",
    },
  },
}

--- One rate reading off one force's own fluid production statistics: no
--- quality loop at all, a fluid has none, unlike production.lua's own
--- flow.item_flow which sums one for every quality the game has.
local function rate_for(force, surface, fluid, window)
  local stats = force.get_fluid_production_statistics(surface)
  local produced = stats.get_flow_count{ name = fluid, category = "input", precision_index = window.precision }
  local consumed = stats.get_flow_count{ name = fluid, category = "output", precision_index = window.precision }
  return produced, consumed
end

-- all=true is the every-team question in one call, summed over every
-- surface the same way item_rate's own sweep metric sums it (Unit B):
-- naming one surface makes no sense across forty teams that may not share
-- one, and this tool's own single-force path is what answers about one
-- surface specifically.
local function fluid_rate_all(a)
  if type(a.fluid) ~= "string" or a.fluid == "" then error("fluid is required", 0) end
  local window = flow.require_window(a.window)
  local surfaces = flow.surfaces_for({})

  local rows = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then
      local produced, consumed = 0, 0
      for _, surface in ipairs(surfaces) do
        local here_in, here_out = rate_for(force, surface, a.fluid, window)
        produced, consumed = produced + here_in, consumed + here_out
      end
      rows[#rows + 1] = { force = force.name, produced = produced, consumed = consumed }
    end
  end
  -- Sorted on the raw number before rounding, the same discipline
  -- envelope.lua's own header explains: rounding for the wire (which can
  -- turn a fraction into a short decimal STRING, bounded.round) must never
  -- touch which row is the leader before the cut.
  table.sort(rows, function(x, y)
    if x.produced ~= y.produced then return x.produced > y.produced end
    return x.force < y.force
  end)
  -- The wire shape is built BEFORE the byte bound measures it. The first
  -- version fitted rows keyed {force, produced, consumed} and renamed them to
  -- the three _per_min keys afterwards, so bounded.fit measured a row about
  -- half the size of the one actually sent; past roughly ninety forces the
  -- reply was refused whole with too_large and shipped no rows at all. kills,
  -- built and trains never had the problem because their rows already carry
  -- their final names when fit runs; this one does now too.
  local cut = bounded.cut(rows, bounded.MAX_FORCES)
  local wire = {}
  for i, row in ipairs(cut) do
    wire[i] = {
      force = row.force,
      produced_per_min = bounded.round(row.produced, 2),
      consumed_per_min = bounded.round(row.consumed, 2),
      net_per_min = bounded.round(row.produced - row.consumed, 2),
    }
  end
  local shown = bounded.fit(wire)
  return { total = #rows, shown = #shown, forces = shown }
end

local function fluid_rate(a)
  if a.all == true then return fluid_rate_all(a) end
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  if type(a.fluid) ~= "string" or a.fluid == "" then error("fluid is required", 0) end
  local window = flow.require_window(a.window)

  local produced, consumed = rate_for(force, surface, a.fluid, window)
  return {
    found = true,
    force = force.name, surface = surface.name, fluid = a.fluid, window = a.window,
    produced_per_min = bounded.round(produced, 2),
    consumed_per_min = bounded.round(consumed, 2),
    net_per_min = bounded.round(produced - consumed, 2),
  }
end

M.functions = { fluid_rate = fluid_rate }

return M
