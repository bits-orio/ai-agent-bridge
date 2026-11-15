-- AI Agent Bridge - scripts/sweep/scope.lua
-- Author: bits-orio
-- License: MIT
--
-- The (force, surface) cells a per-cell sweep metric really reads, pruned
-- before its cost is predicted. walk.lua hands every metric every live
-- force and every surface; a metric that predicted forces x surfaces
-- refused 40 teams on 16 surfaces as 640 passes, though each team makes
-- things on one or two of them, and its refusal pointed at an axis that cost
-- the same. Each rule here is a cheap test that leaves out only cells whose
-- answer is already known to be nothing:
--
--   item, fluid: the force's lifetime counters for that item or fluid on
--     that surface are both zero (scripts/tools/stat_surfaces.lua);
--   networks:    the force has no logistic network on that surface, read
--     from force.logistic_networks, the engine's own grouping by surface;
--   planets:     the surface is a space platform, where no enemy evolves.
--
-- The cells are worked out once per call and kept on ctx, so the predict
-- that counts them and the read that walks them see the same list.

local flow            = require("scripts.tools.flow")
local stat_surfaces   = require("scripts.tools.stat_surfaces")

local M = {}

--- Every (force, surface) cell of ctx for which keep(force, surface) holds,
--- force order then surface order.
local function cells_of(ctx, keep)
  local out = {}
  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      if keep(force, surface) then out[#out + 1] = { force = force, surface = surface } end
    end
  end
  return out
end

local function memo(ctx, key, build)
  ctx.cells = ctx.cells or {}
  if not ctx.cells[key] then ctx.cells[key] = build() end
  return ctx.cells[key]
end

--- Cells where the force has ever made or used `item`.
function M.item(ctx, item)
  return memo(ctx, "item:" .. item, function()
    local qualities = flow.quality_names()
    return cells_of(ctx, function(force, surface) return stat_surfaces.item_seen(force, surface, item, qualities) end)
  end)
end

--- Cells where the force has ever made or used `fluid`.
function M.fluid(ctx, fluid)
  return memo(ctx, "fluid:" .. fluid, function()
    return cells_of(ctx, function(force, surface) return stat_surfaces.fluid_seen(force, surface, fluid) end)
  end)
end

--- Cells where the force has at least one logistic network. The dictionary
--- is read once per force: the engine builds it fresh on every access.
function M.networks(ctx)
  return memo(ctx, "networks", function()
    local by_force = {}
    for _, force in ipairs(ctx.forces) do by_force[force.name] = force.logistic_networks or {} end
    return cells_of(ctx, function(force, surface)
      return #(by_force[force.name][surface.name] or {}) > 0
    end)
  end)
end

--- Cells on surfaces that are not space platforms.
function M.planets(ctx)
  return memo(ctx, "planets", function()
    return cells_of(ctx, function(_force, surface) return surface.platform == nil end)
  end)
end

--- Rows keyed by axis label, summed, in first-seen order. On the force axis
--- every live force gets a row, 0 when no cell of it was read, so pruning a
--- force's empty surfaces never drops the force from the ranking.
function M.summed(ctx, axis, axes, values)
  local by_cell, order = {}, {}
  local function add(label, value)
    if not label then return end
    if not by_cell[label] then order[#order + 1] = label end
    by_cell[label] = (by_cell[label] or 0) + value
  end
  if axis == "force" then
    for _, force in ipairs(ctx.forces) do add(force.name, 0) end
  end
  for _, one in ipairs(values) do
    add(axes[axis]({ force = one.cell.force.name, surface = one.cell.surface.name }), one.value)
  end
  local rows = {}
  for _, label in ipairs(order) do rows[#rows + 1] = { name = label, value = by_cell[label] } end
  return rows
end

return M
