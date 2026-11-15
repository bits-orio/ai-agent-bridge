-- AI Agent Bridge - scripts/sweep/metrics/logistics.lua
-- Author: bits-orio
-- License: MIT
--
-- Four sweep metrics, one file, because they share a delegate call:
-- `robots` (every robot each force has, logistic and construction),
-- `logistic_robots`, `construction_robots` and `networks` (how many logistic
-- networks). All delegate to scripts/tools/logistics.lua's own
-- `logistics_summary`, one call per (force, surface) cell, with
-- `contents = false` so the one expensive thing that tool does,
-- get_contents() per network, never runs.
--
-- Both axes are "force", summed over every surface, and "force+surface",
-- one row per pair, regrouped by scripts/sweep/scope.lua.

local logistics_tool = require("scripts.tools.logistics")
local axes             = require("scripts.sweep.axes")
local scope            = require("scripts.sweep.scope")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function LOGISTICS_SUMMARY(a) return logistics_tool.functions.logistics_summary(a) end

local function summarize(force_name, surface_name)
  return LOGISTICS_SUMMARY({ force = force_name, surface = surface_name, contents = false })
end

-- One logistics_summary call per (force, surface) cell, only where the force
-- has a logistic network at all (scripts/sweep/scope.lua): a surface with
-- none reads 0 robots and 0 networks without asking. Predicted on that
-- pruned list.
local MAX_PASSES = 600
local HINT = "ask logistics_summary for one force and surface"

--- One metric reading `field` off each cell's logistics_summary reply.
--- Every field read is whole however the reply's network rows were cut:
--- the totals are summed over every network before the cut, and `total` is
--- the count before it, so no shown-below-total refusal belongs here.
local function metric(field, unit)
  local m = {
    axes = { "force", "force+surface" },
    default_axis = "force",
    subject = nil,
    unit = unit,
    places = 0,
    costly = false,
    max_passes = MAX_PASSES,
    refusal_hint = HINT,
  }
  function m.predict(ctx, _axis, _a) return #scope.networks(ctx) end
  function m.read(ctx, axis, _a)
    local values = {}
    for _, cell in ipairs(scope.networks(ctx)) do
      local reply = summarize(cell.force.name, cell.surface.name)
      if reply.found == false then return nil, { reason = reply.reason } end
      local value = tonumber(reply[field])
      if value == nil then
        return nil, { reason = "logistics_summary sent no " .. field .. ": the companion is older than this metric" }
      end
      values[#values + 1] = { cell = cell, value = value }
    end
    return scope.summed(ctx, axis, axes, values)
  end
  return m
end

-- robots is every robot, both kinds: it summed logistic robots alone, so a
-- force with 2,000 construction robots and 50 logistic ones ranked at 50.
return {
  robots              = metric("robots_total", "count of logistic and construction robots"),
  logistic_robots     = metric("logistic_robots_total", "count of logistic robots"),
  construction_robots = metric("construction_robots_total", "count of construction robots"),
  networks            = metric("total", "count of logistic networks"),
}
