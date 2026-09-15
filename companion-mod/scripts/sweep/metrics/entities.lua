-- AI Agent Bridge - scripts/sweep/metrics/entities.lua
-- Author: bits-orio
-- License: MIT
--
-- The entities sweep metric: how many of one entity prototype each force,
-- surface, platform or force-surface pair has. Delegates every count to
-- scripts/tools/entity_count.lua's own `entity_count` function, the tool
-- that already owns count_entities_filtered and get_entity_count; nothing
-- here calls either engine method itself, so there is exactly one
-- implementation of the measurement no matter how many axes read it.
--
-- Two different delegated shapes, chosen by axis:
--   axis "force": one call, entity_count{all=true}, which reads
--     LuaForce::get_entity_count, documented O(1) (entity_count.lua's own
--     header comment). No surface is asked about anything, so this costs
--     nothing however many forces or surfaces the save holds.
--   axis "surface" / "platform" / "force+surface": entity_count has no
--     equivalent one-call sweep for these (its own per_surface path, when it
--     has one, still keys rows by force first). So this metric drives its
--     own per-cell walk, force by force and surface by surface, calling the
--     SAME single-force single-surface entity_count for each cell walk.lua's
--     prefilter and predict already bounded. Every one of those calls is a
--     direct Lua function call inside this one tool invocation, not a
--     separate RPC round trip, so however many cells it walks the model
--     still spent exactly one call.

local entity_count_tool = require("scripts.tools.entity_count")
local platform          = require("scripts.sweep.platform")
local axes              = require("scripts.sweep.axes")

local ENTITY_COUNT = entity_count_tool.functions.entity_count

-- Mirrors entity_count.lua's own MAX_PASSES (companion-mod/scripts/tools/
-- entity_count.lua, "measured server runs 23 surfaces against 14 populated
-- forces, 322 passes"). That constant is local to entity_count.lua and not
-- exported, so this is a second number rather than a shared one; it is kept
-- equal on purpose and this comment is the tripwire if the two are ever
-- meant to diverge.
local MAX_PASSES = 600

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
  max_passes = MAX_PASSES,
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

--- Narrows the surface scope to live platforms only, for the platform axis.
--- Every other axis walks every surface, unchanged.
function M.prefilter(ctx, axis)
  if axis ~= "platform" then return ctx.surfaces end
  local out = {}
  for _, surface in ipairs(ctx.surfaces) do
    if platform.name_of(surface) then out[#out + 1] = surface end
  end
  return out
end

--- The force axis reads the engine's own per-force counter in one call, no
--- surface loop, so it costs nothing regardless of how many surfaces exist.
--- Every other axis is one pass per force per surface still in scope.
function M.predict(ctx, axis)
  if axis == "force" then return 0 end
  return #ctx.forces * #ctx.surfaces
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

--- One row per (force, surface) cell whose count is non-zero, grouped and
--- summed by the requested axis's label. A cell that measured zero is left
--- out entirely: a force owns entities on almost none of the surfaces it is
--- not standing on, and those passes are cheap precisely because they find
--- nothing (entity_count.lua's own header comment), so they are not worth a
--- row of zero in a "which has the most" answer.
local function read_cells(ctx, axis, a)
  local by_cell, order = {}, {}
  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      local reply = ENTITY_COUNT({ force = force.name, surface = surface.name, name = a.subject })
      if reply.found and reply.count and reply.count > 0 then
        local cell = axes[axis]({
          force = force.name, surface = surface.name,
          platform = platform.name_of(surface),
        })
        if cell then
          if not by_cell[cell] then order[#order + 1] = cell end
          by_cell[cell] = (by_cell[cell] or 0) + reply.count
        end
      end
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
