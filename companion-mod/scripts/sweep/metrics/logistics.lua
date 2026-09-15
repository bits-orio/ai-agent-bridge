-- AI Agent Bridge - scripts/sweep/metrics/logistics.lua
-- Author: bits-orio
-- License: MIT
--
-- Two sweep metrics, one file, because they share a delegate call: `robots`
-- (how many logistic robots each force has) and `networks` (how many
-- logistic networks each force has). Both delegate entirely to
-- scripts/tools/logistics.lua's own `logistics_summary` function, one call
-- per (force, surface) pair, with `contents = false` so the one expensive
-- thing that tool does, get_contents() per network, never runs: a sweep
-- counts robots and networks, never what is sitting in a chest.
--
-- Two different fields off the same reply need two different refusal rules,
-- which is the reason this is two metrics rather than one metric with a
-- subject picking a field:
--
--   `robots` sums logistic_robots over reply.networks, the list
--   logistics_summary bounds to MAX_NETWORKS and reports total/shown
--   beside. Summing a cut list undercounts, so a cut refuses this metric
--   whole, the same rule metrics/entities.lua and metrics/players.lua
--   already follow.
--   `networks` reads reply.total directly, which logistics_summary computes
--   as #order BEFORE it ever cuts anything (scripts/tools/logistics.lua:
--   "Rank on the cheap reads first, cut, and only then ask the surviving
--   networks for their contents"), so it is whole however many networks got
--   left out of the `networks` list itself. No refusal needed for a value
--   that was never a sum over a truncated sample.
--
-- Both axes are "force", summed over every surface, and "force+surface",
-- one row per pair, the same regroup-by-axes.lua shape metrics/entities.lua
-- and metrics/item_rate.lua already use.

local logistics_tool = require("scripts.tools.logistics")
local axes             = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function LOGISTICS_SUMMARY(a) return logistics_tool.functions.logistics_summary(a) end

local function summarize(force_name, surface_name)
  return LOGISTICS_SUMMARY({ force = force_name, surface = surface_name, contents = false })
end

-- forces x surfaces, the same ceiling item_rate.lua and evolution.lua use in
-- this same stage: one logistics_summary call per pair either way, the axis
-- only decides how the rows get folded afterward.
local MAX_PASSES = 600

local ROBOTS = {
  axes = { "force", "force+surface" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
  max_passes = MAX_PASSES,
}
function ROBOTS.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function ROBOTS.read(ctx, axis, a)
  local by_cell, order = {}, {}
  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      local reply = summarize(force.name, surface.name)
      if reply.found == false then
        return nil, { reason = reply.reason }
      end
      -- The whole-surface total the tool sums before it cuts its rows, never
      -- the rows: logistics_summary caps at a handful of networks on purpose
      -- and a mall surface holds dozens, so summing the rows undercounted and
      -- refusing on the cut made this metric unable to answer on any real
      -- base, with a refusal that told the model to name a surface the sweep
      -- tool has no parameter for. The total is whole however many rows were
      -- shown, the same way networks reads reply.total below.
      local robots = tonumber(reply.logistic_robots_total)
      if robots == nil then
        return nil, { reason = "logistics_summary sent no robot total: the companion is older than this metric" }
      end
      local cell = axes[axis]({ force = force.name, surface = surface.name })
      if cell then
        if not by_cell[cell] then order[#order + 1] = cell end
        by_cell[cell] = (by_cell[cell] or 0) + robots
      end
    end
  end
  local rows = {}
  for _, cell in ipairs(order) do rows[#rows + 1] = { name = cell, value = by_cell[cell] } end
  return rows
end

local NETWORKS = {
  axes = { "force", "force+surface" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
  max_passes = MAX_PASSES,
}
function NETWORKS.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function NETWORKS.read(ctx, axis, _a)
  local by_cell, order = {}, {}
  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      local reply = summarize(force.name, surface.name)
      if reply.found == false then
        return nil, { reason = reply.reason }
      end
      -- reply.total is whole even when reply.shown is cut (see this file's
      -- header), so no shown-below-total refusal belongs here: refusing on
      -- it would throw away an answer that was never truncated.
      local value = tonumber(reply.total)
      local cell = axes[axis]({ force = force.name, surface = surface.name })
      if cell and value then
        if not by_cell[cell] then order[#order + 1] = cell end
        by_cell[cell] = (by_cell[cell] or 0) + value
      end
    end
  end
  local rows = {}
  for _, cell in ipairs(order) do rows[#rows + 1] = { name = cell, value = by_cell[cell] } end
  return rows
end

return { robots = ROBOTS, networks = NETWORKS }
