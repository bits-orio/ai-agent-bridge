-- AI Agent Bridge - scripts/sweep/metrics/fluid_rate.lua
-- Author: bits-orio
-- License: MIT
--
-- The fluid_rate sweep metric: how fast one fluid is being made, right now,
-- for each force. Unlike kills, built and trains, this cannot delegate to
-- the tool's own all=true: that path sums every surface into one row per
-- force (scripts/tools/fluid_rate.lua's own all=true, the same "omit
-- surface to sum every surface per force" rule item_rate's metric already
-- follows in Unit B), which throws away exactly the per-surface breakdown
-- the force+surface axis needs. So this reads the single-force `fluid_rate`
-- function once per (force, surface) pair instead, the same shape
-- metrics/item_rate.lua already uses for the item version of this same
-- question, and regroups by axis with scripts/sweep/axes.lua the same way.

local fluid_rate_tool = require("scripts.tools.fluid_rate")
local flow = require("scripts.tools.flow")
local axes              = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function FLUID_RATE(a) return fluid_rate_tool.functions.fluid_rate(a) end

-- fluid_rate's own manifest picks no default for `window` either;
-- flow.require_window errors on a missing one (scripts/tools/flow.lua).
-- item_rate.lua's own header names the same default for the same reason.
local DEFAULT_WINDOW = "one_minute"

local M = {
  axes = { "force", "force+surface" },
  default_axis = "force",
  subject = "fluid prototype name, e.g. crude-oil, water",
  subject_kind = "fluid",
  unit = "per_min",
  places = 2,
  costly = false,
}

-- One call per (force, surface) pair whichever axis is asked for: the same
-- predict item_rate.lua declares for the identical shape of walk.
M.max_passes = 600
function M.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function M.check(a)
  if type(a.subject) ~= "string" or a.subject == "" then
    return { reason = "fluid_rate needs a subject: name a fluid prototype, e.g. crude-oil" }
  end
  if not prototypes.fluid[a.subject] then
    return { subject = a.subject, reason = "no fluid prototype named " .. a.subject .. ": use the internal name, for example crude-oil" }
  end
  if a.window ~= nil and a.window ~= "" and not flow.has_window(a.window) then
    return { window = a.window, reason = "unknown window " .. tostring(a.window) .. ": one of " .. flow.window_names }
  end
  return nil
end

function M.read(ctx, axis, a)
  local window = (type(a.window) == "string" and a.window ~= "") and a.window or DEFAULT_WINDOW
  local by_cell, order = {}, {}
  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      local reply = FLUID_RATE({ force = force.name, surface = surface.name, fluid = a.subject, window = window })
      if reply.found == false then
        return nil, { subject = a.subject, reason = reply.reason }
      end
      -- fluid_rate answers one row, never a list to cut; this guard is the
      -- same forward defence item_made.lua and item_rate.lua carry, kept
      -- for the same reason: a delegate may one day grow a cut without this
      -- file changing at all.
      if reply.total and reply.shown and reply.shown < reply.total then
        return nil, {
          subject = a.subject, shown = reply.shown, total = reply.total,
          reason = "fluid_rate showed " .. reply.shown .. " of " .. reply.total ..
                   " rows, so a ranking over them would name whoever survived the cut",
        }
      end
      local value = tonumber(reply.produced_per_min)
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

return M
