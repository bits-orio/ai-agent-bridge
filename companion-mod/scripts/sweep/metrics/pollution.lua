-- AI Agent Bridge - scripts/sweep/metrics/pollution.lua
-- Author: bits-orio
-- License: MIT
--
-- The pollution sweep metric: total pollution standing on each surface right
-- now. Delegates entirely to scripts/tools/environment.lua's own `pollution`
-- function, one call per surface. Pollution belongs to the surface, not to
-- any one force (LuaSurface::get_total_pollution takes no force at all,
-- environment.lua's own header), so the force this metric's calls carry is
-- only there because the tool's own manifest requires one and echoes it back
-- unused; service/internal/catalog/params.go injects a real force onto every
-- tool call, sweep included ("ForceParam is the reserved argument the
-- service injects on every tool"), so a.force is always the asking agent's
-- own force here.
--
-- Surface axis only: there is no force to group by, and platform is already
-- one of the surfaces this walks.

local environment_tool = require("scripts.tools.environment")
local axes               = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function POLLUTION(a) return environment_tool.functions.pollution(a) end

local M = {
  axes = { "surface" },
  default_axis = "surface",
  subject = nil,
  unit = "pollution",
  places = 2,
  -- get_total_pollution "iterates over all the chunks containing pollution"
  -- (environment.lua's own quoted docs): the one engine call in this metric
  -- that is not O(1), so its card says so the way entities.lua's does.
  costly = true,
}

-- One call per surface. 600 is the same generous ceiling every other metric
-- in this file uses; a real server's surface count is nowhere near it.
M.max_passes = 600
function M.predict(ctx, _axis, _a) return #ctx.surfaces end

function M.check(a)
  if type(a.force) ~= "string" or a.force == "" or not game.forces[a.force] then
    return { reason = "pollution needs a force to ask as, and none was on this call" }
  end
  return nil
end

function M.read(ctx, _axis, a)
  local rows = {}
  for _, surface in ipairs(ctx.surfaces) do
    local reply = POLLUTION({ force = a.force, surface = surface.name })
    if reply.found == false then
      return nil, { reason = reply.reason }
    end
    if reply.total and reply.shown and reply.shown < reply.total then
      return nil, {
        shown = reply.shown, total = reply.total,
        reason = "pollution showed " .. reply.shown .. " of " .. reply.total ..
                 " rows, so a ranking over them would name whoever survived the cut",
      }
    end
    local value = tonumber(reply.total_pollution)
    if value then
      rows[#rows + 1] = { name = axes.surface({ surface = surface.name }), value = value }
    end
  end
  return rows
end

return M
