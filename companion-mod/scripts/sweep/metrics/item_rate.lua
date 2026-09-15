-- AI Agent Bridge - scripts/sweep/metrics/item_rate.lua
-- Author: bits-orio
-- License: MIT
--
-- The item_rate sweep metric: how fast one item is being made, right now,
-- for each force. Delegates entirely to scripts/tools/production.lua's own
-- `item_rate` function, one call per (force, surface) pair, over every force
-- and every surface in scope, exactly the read entities.lua's read_cells
-- already does for a per-cell metric. A rate, never a total: item_made owns
-- "how much has been made".
--
-- Two axes, both built from the same per-cell reads, regrouped by
-- scripts/sweep/axes.lua the way metrics/entities.lua already does: default
-- "force+surface" keeps one row per pair, "force" folds every surface's rate
-- into that force's row by summing them, since axes.force(cell) reads only
-- the force half of a cell and ignores which surface it came from.

local production_tool = require("scripts.tools.production")
local flow = require("scripts.tools.flow")
local axes             = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function ITEM_RATE(a) return production_tool.functions.item_rate(a) end

-- item_rate's own manifest picks no default for `window`; flow.require_window
-- errors on a missing one (scripts/tools/flow.lua). production.lua's own desc
-- names one_minute as "the shortest that covers the question, one_minute for
-- right now", so that is what a sweep call leaving `window` out gets.
local DEFAULT_WINDOW = "one_minute"

local M = {
  axes = { "force+surface", "force" },
  default_axis = "force+surface",
  subject = "item prototype name, e.g. iron-plate",
  subject_kind = "item",
  unit = "per_min",
  places = 2,
  costly = false,
}

-- One call per (force, surface) pair whichever axis is asked for: the
-- "force" axis still reads every surface to sum them, it only regroups the
-- rows afterward. predict = forces x surfaces per the phase5 contract.
M.max_passes = 600
function M.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function M.check(a)
  if type(a.subject) ~= "string" or a.subject == "" then
    return { reason = "item_rate needs a subject: name an item prototype, e.g. iron-plate" }
  end
  if not prototypes.item[a.subject] then
    return { subject = a.subject, reason = "no item prototype named " .. a.subject .. ": use the internal name, for example iron-plate" }
  end
  -- A wrong window used to escape as a provider_error from the delegate; a
  -- found=false card is what the tool's own description promises the model.
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
      local reply = ITEM_RATE({ force = force.name, surface = surface.name, item = a.subject, window = window })
      if reply.found == false then
        return nil, { subject = a.subject, reason = reply.reason }
      end
      -- item_rate answers one row, never a list to cut; this guard is the
      -- same forward defence item_made.lua carries, kept for the same
      -- reason: a provider tool this delegate wraps may grow a cut later
      -- without this file changing at all.
      if reply.total and reply.shown and reply.shown < reply.total then
        return nil, {
          subject = a.subject, shown = reply.shown, total = reply.total,
          reason = "item_rate showed " .. reply.shown .. " of " .. reply.total ..
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
