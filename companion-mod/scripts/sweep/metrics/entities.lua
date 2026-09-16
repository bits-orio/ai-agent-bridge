-- AI Agent Bridge - scripts/sweep/metrics/entities.lua
-- Author: bits-orio
-- License: MIT
--
-- The entities sweep metric: how many of one entity prototype each force,
-- surface, platform or force-surface pair has. Every measurement is
-- delegated to scripts/tools/entity_count.lua, the tool that already owns
-- count_entities_filtered and get_entity_count. Nothing here calls either
-- engine method, walks a surface, or bounds a walk: there is exactly one
-- implementation of the count and one MAX_PASSES, and both live in the tool.
--
-- Two delegated shapes, chosen by axis:
--   axis "force": entity_count{all=true}, which reads
--     LuaForce::get_entity_count, documented O(1). No surface is asked
--     anything, so this costs nothing however many forces or surfaces exist.
--   every other axis: entity_count{all=true, per_surface=true}, one row per
--     force-and-surface that has any, which this metric only regroups under
--     the axis asked for. The tool bounds that walk itself and refuses when
--     it would be too many passes; the refusal comes back through read() as
--     a sweep refusal with the tool's own reason.
--
-- The first version of this file re-drove the force-by-surface walk itself,
-- beside an identical walk in entity_count.lua, with a second copy of
-- MAX_PASSES kept equal "on purpose". docs/design/phase5-sweep.md chose a
-- delegating registry precisely so that no measurement exists twice; the
-- gate on that build found the walk duplicated anyway. Delegating is what
-- makes the design true rather than described.

local entity_count_tool = require("scripts.tools.entity_count")
local axes              = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, so the seam between
-- this metric and its delegate is something a test can stand a stub into:
-- the fixture can never produce more force-surface rows than the tool's own
-- cut, so the truncation refusal below is only reachable that way.
local function ENTITY_COUNT(a) return entity_count_tool.functions.entity_count(a) end

local M = {
  axes = { "force", "surface", "platform", "force+surface" },
  default_axis = "force",
  subject = "entity prototype name, e.g. lab, assembling-machine-2",
  subject_kind = "entity",
  unit = "count",
  places = 0,
  -- Only sometimes true (the force axis is the O(1) counter above), but this
  -- metric is the only one of the four that can ever walk the map at all, so
  -- its card says so.
  costly = true,
}

function M.check(a)
  if type(a.subject) ~= "string" or a.subject == "" then
    return { reason = "entities needs a subject: name an entity prototype, e.g. lab, assembling-machine-2" }
  end
  if not prototypes.entity[a.subject] then
    return {
      subject = a.subject,
      reason = "no entity prototype by that name: use the internal name, for example assembling-machine-2",
    }
  end
  return nil
end

local function read_force(a)
  local reply = ENTITY_COUNT({ all = true, name = a.subject })
  if reply.found == false then
    return nil, { subject = a.subject, reason = reply.reason }
  end
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.count }
  end
  return rows
end

--- One row per (force, surface) cell that has any, from the tool's own
--- per_surface sweep, regrouped and summed under the axis asked for. A cell
--- that measured zero is already absent: the tool leaves those out, since a
--- force owns entities on almost none of the surfaces it is not standing on.
---
--- The tool cuts its rows to a count and a byte budget and reports total
--- beside shown. A cut list of cells ranked and published as a leader would
--- name whoever happened to survive the cut, so shown below total is refused
--- whole, the rule the players metric and the briefing's own pl key follow.
local function read_cells(_ctx, axis, a)
  local reply = ENTITY_COUNT({ all = true, per_surface = true, name = a.subject })
  if reply.found == false then
    return nil, { subject = a.subject, reason = reply.reason, passes = reply.passes, max_passes = reply.max_passes }
  end
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      subject = a.subject, shown = reply.shown, total = reply.total,
      reason = "entity_count showed " .. reply.shown .. " of " .. reply.total ..
               " force-and-surface rows, so a ranking over them would name whoever survived the cut: " ..
               "use the force axis for each force's total, or name a surface",
    }
  end
  local by_cell, order = {}, {}
  for _, row in ipairs(reply.forces or {}) do
    local cell = axes[axis]({
      force = row.force, surface = row.surface,
      platform = row.platform, owner = row.owner, location = row.location, state = row.state,
    })
    if cell then
      if not by_cell[cell] then order[#order + 1] = cell end
      by_cell[cell] = (by_cell[cell] or 0) + row.count
    end
  end
  local rows = {}
  for _, cell in ipairs(order) do rows[#rows + 1] = { name = cell, value = by_cell[cell] } end
  return rows
end

function M.read(ctx, axis, a)
  if axis == "force" then return read_force(a) end
  return read_cells(ctx, axis, a)
end

return M
