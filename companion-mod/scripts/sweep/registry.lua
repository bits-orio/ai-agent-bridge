-- AI Agent Bridge - scripts/sweep/registry.lua
-- Author: bits-orio
-- License: MIT
--
-- Merges every sweep metric module the way scripts/tools/engine.lua merges
-- tool modules: one place a metric gets added, so the tool's own vocabulary
-- (metric_line, used in its manifest desc) and the found=false card set
-- (cards) can never drift from what is actually registered. The four the
-- phase5 contract named shipped first; item_made, item_rate, pollution,
-- evolution, robots and networks are Stage 5 Unit B, each delegating to a
-- tool docs/design/phase5-sweep.md already lists as sweep-worthy. kills,
-- built, fluid_rate and trains are Stage 5 Unit A, each delegating to a tool
-- this same stage adds (scripts/tools/{kills,built,fluid_rate,trains}.lua).

local probe = require("scripts.probe")
local axes  = require("scripts.sweep.axes")

local entities_metric  = require("scripts.sweep.metrics.entities")
local rockets_metric   = require("scripts.sweep.metrics.rockets")
local research_metric  = require("scripts.sweep.metrics.research")
local players_metric   = require("scripts.sweep.metrics.players")
local item_made_metric = require("scripts.sweep.metrics.item_made")
local item_rate_metric = require("scripts.sweep.metrics.item_rate")
local pollution_metric = require("scripts.sweep.metrics.pollution")
local evolution_metric  = require("scripts.sweep.metrics.evolution")
local logistics_metric  = require("scripts.sweep.metrics.logistics")
local kills_metric      = require("scripts.sweep.metrics.kills")
local built_metric      = require("scripts.sweep.metrics.built")
local fluid_rate_metric = require("scripts.sweep.metrics.fluid_rate")
local trains_metric     = require("scripts.sweep.metrics.trains")

local METRICS = {
  entities   = entities_metric,
  rockets    = rockets_metric,
  research   = research_metric,
  players    = players_metric,
  item_made  = item_made_metric,
  item_rate  = item_rate_metric,
  pollution  = pollution_metric,
  evolution  = evolution_metric,
  robots     = logistics_metric.robots,
  networks   = logistics_metric.networks,
  kills      = kills_metric,
  built      = built_metric,
  fluid_rate = fluid_rate_metric,
  trains     = trains_metric,
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

-- ── metrics another mod declares ──────────────────────────────────────
--
-- A provider that wants one of its tools swept says so in its own
-- agent_tools_v1 manifest, with a `sweep` block beside the tool's desc and
-- params (docs/design/phase5-sweep.md, "Provider-declared metrics"):
--
--   standings = {
--     desc = "...", params = { ... },
--     sweep = {
--       axes = { "force" },          -- axes its rows can be grouped by
--       default_axis = "force",      -- optional, the first axis otherwise
--       rows = "forces",             -- the reply field holding the rows
--       name = "force",              -- the row field naming the cell, or a
--                                    -- table { force = "f", surface = "s" }
--       value = "score",             -- the row field holding the number
--       unit = "points",             -- optional
--       metric = "standings",        -- optional, the tool's name otherwise
--       args = { all = true },       -- optional, what the tool needs to sweep
--       subject_param = "item",      -- optional, which param takes a subject
--     },
--   }
--
-- Nothing here names a mod. Any multi-team mod, MTS or an OARC-like one,
-- becomes sweepable by declaring that block, and the companion needs no
-- release to learn about it. Discovery happens at call time, not at load:
-- another mod's remote interface is not guaranteed to exist while this
-- file is being required, and probe.lua already re-reads every provider on
-- every call rather than trusting a catalog (CONTEXT.md invariant 3).
--
-- A declared metric never shadows one of the companion's own, and the
-- companion's own interface is skipped so sweep cannot discover itself.

local OWN_IFACE = "ai-agent-bridge-tools"

local function valid_axis(name)
  for _, one in ipairs(axes.NAMES) do
    if one == name then return true end
  end
  return false
end

--- Whether a tool entry's sweep block declares enough to be swept at all. A
--- block that does not is ignored quietly: the tool is still a tool, it is
--- just not a metric, and the provider's own manifest is the place to say
--- what it meant.
local function usable_spec(spec)
  if type(spec) ~= "table" then return false end
  if type(spec.axes) ~= "table" or #spec.axes == 0 then return false end
  for _, axis in ipairs(spec.axes) do
    if not valid_axis(axis) then return false end
  end
  if spec.default_axis ~= nil and not valid_axis(spec.default_axis) then return false end
  if type(spec.rows) ~= "string" then return false end
  if type(spec.name) ~= "string" and type(spec.name) ~= "table" then return false end
  if type(spec.value) ~= "string" then return false end
  return true
end

--- The canonical cell one delegate row describes, in the shape axes.lua
--- names: a single name field labels the axis asked for; a table of fields
--- fills more than one, which is how a compound axis gets both halves.
local function cell_of(spec, axis, row)
  local cell = {}
  if type(spec.name) == "table" then
    for key, field in pairs(spec.name) do cell[key] = row[field] end
  else
    cell[axis] = row[spec.name]
  end
  return cell
end

--- A metric that delegates to another provider's tool over remote.call,
--- shaped exactly like a metric module so walk.lua cannot tell the two
--- apart. Its rows are refused whole when the delegate cut them, the rule
--- every companion metric follows: a ranking over the survivors of a cut
--- names whoever survived.
local function foreign_metric(iface, fn, entry)
  local spec = entry.sweep
  local m = {
    axes = spec.axes,
    default_axis = spec.default_axis or spec.axes[1],
    subject = spec.subject_param and (spec.subject or "what " .. fn .. " measures") or nil,
    subject_kind = spec.subject_kind,
    unit = spec.unit or "count",
    -- Two places unless the provider says otherwise: a fraction leaves the
    -- companion as a short decimal string (bounded.round), and the service's
    -- ranker reads those as numbers, so a provider's 12.5 is ranked as 12.5
    -- rather than rounded to 13 to stay rankable.
    places = spec.places or 2,
    costly = spec.costly == true,
    provider = iface,
    tool = fn,
  }

  function m.check(a)
    if spec.subject_param and (type(a.subject) ~= "string" or a.subject == "") then
      return { reason = fn .. " on " .. iface .. " needs a subject: " .. m.subject }
    end
    return nil
  end

  function m.read(_ctx, axis, a)
    local args = {}
    for key, value in pairs(spec.args or {}) do args[key] = value end
    if spec.subject_param and a.subject then args[spec.subject_param] = a.subject end
    if a.force then args.force = a.force end

    local reply = probe.call(iface, fn, args)
    if not reply.ok then
      return nil, { reason = iface .. "." .. fn .. " refused: " .. tostring(reply.m) }
    end
    local r = reply.r
    if type(r) ~= "table" then
      return nil, { reason = iface .. "." .. fn .. " returned no table to sweep" }
    end
    if r.total and r.shown and r.shown < r.total then
      return nil, {
        shown = r.shown, total = r.total,
        reason = iface .. "." .. fn .. " showed " .. r.shown .. " of " .. r.total ..
                 " rows, so a ranking over them would name whoever survived its cut",
      }
    end

    local by_cell, order = {}, {}
    local rows_in = type(r[spec.rows]) == "table" and r[spec.rows] or {}
    for _, row in ipairs(rows_in) do
      local cell = axes[axis](cell_of(spec, axis, row))
      -- Companion tools send rounded fractions as strings; a provider may too.
      local value = tonumber(row[spec.value])
      if cell and value then
        if not by_cell[cell] then order[#order + 1] = cell end
        by_cell[cell] = (by_cell[cell] or 0) + value
      end
    end
    local rows = {}
    for _, cell in ipairs(order) do rows[#rows + 1] = { name = cell, value = by_cell[cell] } end
    return rows
  end

  return m
end

--- Every metric other providers declare right now, keyed by metric name.
--- Providers and tools are walked in sorted order so a name two providers
--- both declare resolves the same way on two identical games.
local function foreign_metrics()
  local out = {}
  for _, iface in ipairs(probe.provider_names()) do
    if iface ~= OWN_IFACE then
      local manifest = probe.read(iface, nil)
      if manifest then
        local names = {}
        for fn in pairs(manifest.tools) do names[#names + 1] = fn end
        table.sort(names)
        for _, fn in ipairs(names) do
          local entry = manifest.tools[fn]
          if usable_spec(entry.sweep) then
            local metric_name = type(entry.sweep.metric) == "string" and entry.sweep.metric or fn
            if not METRICS[metric_name] and not out[metric_name] then
              out[metric_name] = foreign_metric(iface, fn, entry)
            end
          end
        end
      end
    end
  end
  return out
end

local M = {}

--- The metric named `name`, or nil. `name` may be anything a caller sent,
--- not necessarily a string. The companion's own metrics first; then
--- whatever another provider has declared.
function M.get(name)
  if type(name) ~= "string" then return nil end
  return METRICS[name] or foreign_metrics()[name]
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
  local foreign = foreign_metrics()
  local names = sorted_names()
  for name in pairs(foreign) do names[#names + 1] = name end
  table.sort(names)
  local out = {}
  for _, name in ipairs(names) do
    local metric = METRICS[name] or foreign[name]
    out[#out + 1] = {
      metric = name,
      axes = metric.axes,
      default_axis = metric.default_axis,
      subject = metric.subject or nil,
      subject_kind = metric.subject_kind or nil,
      unit = metric.unit,
      costly = metric.costly == true,
      provider = metric.provider or nil,
    }
  end
  return out
end

return M
