-- AI Agent Bridge - scripts/sweep/metrics/item_lifetime.lua
-- Author: bits-orio
-- License: MIT
--
-- One builder for the two lifetime item metrics, item_made (produced) and
-- item_used (consumed): both read production_since with since_tick = 0,
-- the engine's lifetime counters summed over every surface and quality, one
-- call per force, and differ only in which side of the reply they rank.
-- One source, so the two cannot drift on names, checks or the cut guard.

local production_since_tool = require("scripts.tools.production_since")
local axes                  = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load: the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam without editing this file.
local function PRODUCTION_SINCE(a) return production_since_tool.functions.production_since(a) end

local M = {}

--- A metric named `name` ranking each force's lifetime `field` of one item,
--- "produced" or "consumed" off the production_since reply.
function M.metric(name, field)
  local m = {
    axes = { "force" },
    default_axis = "force",
    subject = "item prototype name, e.g. iron-plate",
    subject_kind = "item",
    unit = "count",
    places = 0,
    -- since_tick = 0 reads the engine's own lifetime counters, O(1) per
    -- quality per surface; no map is walked.
    costly = false,
    -- One call per live force: far cheaper than the force-and-surface walk
    -- the 600 was set against, so it is headroom, not a tight fit.
    max_passes = 600,
  }
  function m.predict(ctx, _axis, _a) return #ctx.forces end

  function m.check(a)
    if type(a.subject) ~= "string" or a.subject == "" then
      return { reason = name .. " needs a subject: name an item prototype, e.g. iron-plate" }
    end
    -- Checked before any force is asked: a name that is not an item would
    -- otherwise sweep every force and answer a confident zero for each.
    if not prototypes.item[a.subject] then
      return { subject = a.subject, reason = "no item prototype named " .. a.subject .. ": use the internal name, for example iron-plate" }
    end
    return nil
  end

  --- One force's figure, or nil and a refusal. production_since rounds its
  --- figures through bounded.round, which can send a short decimal string.
  local function read_force(force, a)
    local reply = PRODUCTION_SINCE({ force = force.name, item = a.subject, since_tick = 0 })
    if reply.found == false then return nil, { subject = a.subject, reason = reply.reason } end
    if reply.total and reply.shown and reply.shown < reply.total then
      return nil, {
        subject = a.subject, shown = reply.shown, total = reply.total,
        reason = "production_since showed " .. reply.shown .. " of " .. reply.total ..
                 " rows, so a ranking over them would name whoever survived the cut",
      }
    end
    return tonumber(reply[field])
  end

  function m.read(ctx, _axis, a)
    local rows = {}
    for _, force in ipairs(ctx.forces) do
      local value, refusal = read_force(force, a)
      if refusal then return nil, refusal end
      if value then rows[#rows + 1] = { name = axes.force({ force = force.name }), value = value } end
    end
    return rows
  end
  return m
end

return M
