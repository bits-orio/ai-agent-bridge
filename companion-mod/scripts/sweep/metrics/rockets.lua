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

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function ROCKETS(a) return rockets_tool.functions.rockets(a) end

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
  -- rockets{all=true} cuts at bounded.MAX_FORCES, and a ranking over the
  -- survivors of a cut names whoever survived: the rule every other metric
  -- in this catalog follows, and the one this file, the oldest, had skipped.
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      shown = reply.shown, total = reply.total,
      reason = "rockets showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.rockets_launched }
  end
  return rows
end

return M
