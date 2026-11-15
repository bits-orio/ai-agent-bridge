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
local scope             = require("scripts.sweep.scope")

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

-- One fluid_rate call per (force, surface) cell, only on cells where the
-- force has ever made or used the fluid (scripts/sweep/scope.lua), the same
-- pruning item_rate.lua applies, and predicted on that pruned list.
M.max_passes = 600
function M.predict(ctx, _axis, a) return #scope.fluid(ctx, a.subject) end

M.refusal_hint = "ask fluid_rate with all=true for each force's total over every surface in one call"

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

--- One cell's rate, or nil and a refusal when the delegate refused or cut.
local function read_cell(cell, a, window)
  local reply = FLUID_RATE({ force = cell.force.name, surface = cell.surface.name, fluid = a.subject, window = window })
  if reply.found == false then
    return nil, { subject = a.subject, reason = reply.reason }
  end
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      subject = a.subject, shown = reply.shown, total = reply.total,
      reason = "fluid_rate showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  return tonumber(reply.produced_per_min) or 0
end

function M.read(ctx, axis, a)
  local window = (type(a.window) == "string" and a.window ~= "") and a.window or DEFAULT_WINDOW
  local values = {}
  for _, cell in ipairs(scope.fluid(ctx, a.subject)) do
    local value, refusal = read_cell(cell, a, window)
    if refusal then return nil, refusal end
    values[#values + 1] = { cell = cell, value = value }
  end
  return scope.summed(ctx, axis, axes, values)
end

return M
