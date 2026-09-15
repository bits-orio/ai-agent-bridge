-- AI Agent Bridge - scripts/sweep/metrics/rockets.lua
-- Author: bits-orio
-- License: MIT
--
-- The rockets sweep metric: how many rockets each force has launched.
-- Delegates entirely to scripts/tools/rockets.lua's own `rockets` function
-- with all=true, the tool that already reads LuaForce::rockets_launched, an
-- engine-kept counter (rockets.lua's own header comment). No surface exists
-- for a rocket launch, so this is the one axis that will ever make sense
-- here: force only.

local rockets_tool = require("scripts.tools.rockets")
local axes          = require("scripts.sweep.axes")

local ROCKETS = rockets_tool.functions.rockets

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
}

function M.read(_ctx, _axis, _a)
  local reply = ROCKETS({ all = true })
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.rockets_launched }
  end
  return rows
end

return M
