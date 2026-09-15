-- AI Agent Bridge - scripts/sweep/metrics/evolution.lua
-- Author: bits-orio
-- License: MIT
--
-- The evolution sweep metric: enemy evolution factor per force per surface.
-- Delegates entirely to scripts/tools/environment.lua's own `evolution`
-- function, one call per (force, surface) pair.
--
-- Unlike pollution, evolution genuinely differs by force: LuaForce::
-- get_evolution_factor takes an optional surface but is still read off one
-- specific LuaForce (environment.lua's own quoted docs), so there is no
-- force-free reading the way a surface's pollution total is. Two axes:
-- "force+surface", the default, walks every live force against every
-- surface; "surface" narrows to the one force asking, a.force (the service
-- injects a real one onto every tool call, params.go's own "ForceParam is
-- the reserved argument the service injects on every tool"), across every
-- surface, which is what "how does MY evolution compare planet to planet"
-- means and the only way a single surface-keyed row can mean one number
-- rather than an arbitrary pick among several forces'.

local environment_tool = require("scripts.tools.environment")
local axes               = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function EVOLUTION(a) return environment_tool.functions.evolution(a) end

local M = {
  axes = { "force+surface", "surface" },
  default_axis = "force+surface",
  subject = nil,
  unit = "factor",
  places = 4,
  costly = false,
}

-- Both axes cost the same forces x surfaces worth of calls at most (the
-- surface axis only ever asks about one force, but the walk budget is set
-- against the wider default axis so neither branch needs its own number).
M.max_passes = 600
function M.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function M.check(a, axis)
  if axis == "surface" and (type(a.force) ~= "string" or a.force == "" or not game.forces[a.force]) then
    return { reason = "evolution's surface axis reads one force across every surface, and none was on this call" }
  end
  return nil
end

--- One evolution_factor reading, or a refusal built the same way every other
--- delegate call in this file is: found=false passed straight through, and a
--- forward-defensive shown-below-total guard for a delegate that may one day
--- grow a cut, the same discipline every metric in this stage keeps.
local function read_one(force_name, surface_name)
  local reply = EVOLUTION({ force = force_name, surface = surface_name })
  if reply.found == false then
    return nil, { reason = reply.reason }
  end
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      shown = reply.shown, total = reply.total,
      reason = "evolution showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  return tonumber(reply.evolution_factor)
end

function M.read(ctx, axis, a)
  local rows = {}
  if axis == "surface" then
    for _, surface in ipairs(ctx.surfaces) do
      local value, refusal = read_one(a.force, surface.name)
      if refusal then return nil, refusal end
      if value then
        rows[#rows + 1] = { name = axes.surface({ surface = surface.name }), value = value }
      end
    end
    return rows
  end

  for _, force in ipairs(ctx.forces) do
    for _, surface in ipairs(ctx.surfaces) do
      local value, refusal = read_one(force.name, surface.name)
      if refusal then return nil, refusal end
      if value then
        rows[#rows + 1] = {
          name = axes["force+surface"]({ force = force.name, surface = surface.name }),
          value = value,
        }
      end
    end
  end
  return rows
end

return M
