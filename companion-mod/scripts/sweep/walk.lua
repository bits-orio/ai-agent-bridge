-- AI Agent Bridge - scripts/sweep/walk.lua
-- Author: bits-orio
-- License: MIT
--
-- The generic driver every sweep metric rides: pick the axis, run the
-- metric's own check, resolve which forces and surfaces are in scope, let
-- the metric predict its own cost against that scope,
-- refuse before a single pass runs if the estimate is over budget, then hand
-- the metric its scope to read through whichever existing tool it delegates
-- to. Mirrors the discipline entity_count.lua's own named-surface path
-- already uses on itself (predict the passes, refuse rather than
-- half-answer), generalised here so any metric with a per-cell read gets it
-- for free instead of reimplementing it.
--
-- A refusal from any stage below (unknown metric, unsupported axis, a
-- metric's own check, an over-budget prediction, or the read itself) is
-- built the same way: `refuse` stamps sweep_v=1 and found=false and returns
-- immediately. Nothing after a refusal ever runs, so a refused sweep never
-- spends a pass (phase5 contract: "refused BEFORE any pass runs. Never
-- half-count").

local registry = require("scripts.sweep.registry")
local envelope = require("scripts.sweep.envelope")

local M = {}

--- Forces a sweep answers for: the ones a team mod has actually put players
--- on. The same rule entity_count.lua's own sweep_forces() and
--- current_research's all=true already follow (scripts/tools/entity_count.lua,
--- scripts/tools/basics.lua), so an empty slot never contributes a row of
--- zeroes to a sweep grouped by force, and a metric with a per-cell read
--- never spends a pass on a force that owns nothing anywhere.
local function live_forces()
  local out = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then out[#out + 1] = force end
  end
  table.sort(out, function(x, y) return x.name < y.name end)
  return out
end

--- Every valid surface, name order, the same walk list_surfaces already
--- builds. An axis that has nothing to say about some of them (the platform
--- axis, on a planet) answers nil for that cell and the cell contributes no
--- row; see scripts/sweep/axes.lua. No metric narrows this list itself.
local function live_surfaces()
  local out = {}
  for _, surface in pairs(game.surfaces) do
    if surface.valid then out[#out + 1] = surface end
  end
  table.sort(out, function(x, y) return x.name < y.name end)
  return out
end

local function refuse(fields)
  fields.sweep_v = 1
  fields.found = false
  return fields
end

--- Runs one sweep call end to end. `a` is the tool's own argument table:
--- metric, axis, subject, limit, whatever the model sent.
function M.run(a)
  a = a or {}

  local metric_name = a.metric
  local metric = registry.get(metric_name)
  if not metric then
    return refuse({
      metric = metric_name,
      reason = "unknown sweep metric" ..
               (metric_name and (": " .. tostring(metric_name)) or ": none given"),
      metrics = registry.cards(),
    })
  end

  local axis = a.axis
  if axis == nil or axis == "" then axis = metric.default_axis end
  if not registry.supports(metric, axis) then
    return refuse({
      metric = metric_name, axis = axis,
      reason = "sweep metric " .. metric_name .. " does not group by " .. tostring(axis) ..
               ": it groups by " .. table.concat(metric.axes, ", "),
      metrics = registry.cards(),
    })
  end

  if metric.check then
    local problem = metric.check(a, axis)
    if problem then
      problem.metric = metric_name
      problem.axis = axis
      return refuse(problem)
    end
  end

  local ctx = { forces = live_forces(), surfaces = live_surfaces() }

  if metric.predict and metric.max_passes then
    local passes = metric.predict(ctx, axis, a)
    if passes > metric.max_passes then
      return refuse({
        metric = metric_name, axis = axis,
        reason = "sweeping " .. metric_name .. " by " .. axis .. " would be " .. passes ..
                 " passes over the map, past the " .. metric.max_passes .. " one call may spend: " ..
                 "ask about fewer forces or surfaces, or use the force axis for the engine's own totals",
        passes = passes, max_passes = metric.max_passes,
      })
    end
  end

  local rows, refusal = metric.read(ctx, axis, a)
  if refusal then
    refusal.metric = metric_name
    refusal.axis = axis
    return refuse(refusal)
  end

  return envelope.build(metric_name, axis, metric, rows, a)
end

return M
