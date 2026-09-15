-- AI Agent Bridge - scripts/sweep/metrics/trains.lua
-- Author: bits-orio
-- License: MIT
--
-- The trains sweep metric: how many trains each force has. Delegates
-- entirely to scripts/tools/trains.lua's own `trains` function with
-- all=true, the tool that already sums LuaTrainManager::get_trains over
-- every surface. Force axis only, the same reason kills.lua's own metric
-- gives: trains{all=true} always sums every surface into one row per force.

local trains_tool = require("scripts.tools.trains")
local axes          = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function TRAINS(a) return trains_tool.functions.trains(a) end

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  -- The delegate runs one get_trains per force per surface and the engine
  -- builds a LuaTrain wrapper for every train it returns, so unlike kills
  -- and built this is not a counter read. Declared as the walk it is, the
  -- same shape and bound entity_count and fluid_rate declare for theirs.
  costly = true,
  max_passes = 600,
}

function M.predict(ctx, _axis, _a) return #ctx.forces * #ctx.surfaces end

function M.read(_ctx, _axis, _a)
  local reply = TRAINS({ all = true })
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      shown = reply.shown, total = reply.total,
      reason = "trains showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.trains }
  end
  return rows
end

return M
