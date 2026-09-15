-- AI Agent Bridge - scripts/sweep/registry.lua
-- Author: bits-orio
-- License: MIT
--
-- Merges every sweep metric module the way scripts/tools/engine.lua merges
-- tool modules: one place a metric gets added, so the tool's own vocabulary
-- (metric_line, used in its manifest desc) and the found=false card set
-- (cards) can never drift from what is actually registered. Ship only the
-- four metrics the phase5 contract names; more are Stage 5.

local entities_metric = require("scripts.sweep.metrics.entities")
local rockets_metric  = require("scripts.sweep.metrics.rockets")
local research_metric = require("scripts.sweep.metrics.research")
local players_metric  = require("scripts.sweep.metrics.players")

local METRICS = {
  entities = entities_metric,
  rockets  = rockets_metric,
  research = research_metric,
  players  = players_metric,
}

-- A metric with no read is a sweep entry that can never answer: fail at
-- load, where it is obvious, the same discipline engine.lua applies to a
-- manifest entry with no function behind it (scripts/tools/engine.lua, "A
-- manifest entry with no function would advertise a tool that always
-- answers no_tool"). The delegation this registry exists to enforce only
-- means anything if every entry actually has somewhere to read from.
for name, metric in pairs(METRICS) do
  if type(metric.read) ~= "function" then
    error("[ai-agent-bridge] sweep metric " .. name .. " has no read function", 0)
  end
  if type(metric.axes) ~= "table" or #metric.axes == 0 then
    error("[ai-agent-bridge] sweep metric " .. name .. " declares no axes", 0)
  end
  if type(metric.default_axis) ~= "string" then
    error("[ai-agent-bridge] sweep metric " .. name .. " declares no default_axis", 0)
  end
end

--- Every registered name, sorted, so anything built from it (metric_line,
--- cards) is byte-stable across two identical games rather than however
--- pairs() happened to walk the table.
local function sorted_names()
  local out = {}
  for name in pairs(METRICS) do out[#out + 1] = name end
  table.sort(out)
  return out
end

local M = {}

--- The metric named `name`, or nil. `name` may be anything a caller sent,
--- not necessarily a string.
function M.get(name)
  if type(name) ~= "string" then return nil end
  return METRICS[name]
end

--- Whether `metric` (a value M.get returned) sweeps by `axis`.
function M.supports(metric, axis)
  for _, one in ipairs(metric.axes) do
    if one == axis then return true end
  end
  return false
end

--- The generated vocabulary line for the tool's own manifest description:
--- every registered metric, what axes it groups by, and whether it needs a
--- subject. tool.lua builds `desc` by calling this, so the description
--- cannot name a metric that is not registered or omit one that is.
function M.metric_line()
  local parts = {}
  for _, name in ipairs(sorted_names()) do
    local metric = METRICS[name]
    local bit = name .. " (" .. table.concat(metric.axes, "/") .. ")"
    if metric.subject then bit = bit .. ", needs subject" end
    parts[#parts + 1] = bit
  end
  return table.concat(parts, "; ")
end

--- One card per metric: what a found=false reply carries for an unrecognised
--- metric or an axis a metric does not sweep, so a wrong guess recovers in
--- one round with a complete answer instead of a one-line provider_error.
function M.cards()
  local out = {}
  for _, name in ipairs(sorted_names()) do
    local metric = METRICS[name]
    out[#out + 1] = {
      metric = name,
      axes = metric.axes,
      default_axis = metric.default_axis,
      subject = metric.subject or nil,
      subject_kind = metric.subject_kind or nil,
      unit = metric.unit,
      costly = metric.costly == true,
    }
  end
  return out
end

return M
