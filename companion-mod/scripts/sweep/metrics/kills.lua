-- AI Agent Bridge - scripts/sweep/metrics/kills.lua
-- Author: bits-orio
-- License: MIT
--
-- The kills sweep metric: how many enemies each force has killed. Delegates
-- entirely to scripts/tools/kills.lua's own `kills` function with all=true,
-- the tool that already sums LuaForce::get_kill_count_statistics over every
-- surface. Force axis only: kills{all=true} always sums every surface into
-- one row per force, so there is no surface left here to regroup by.

local kills_tool = require("scripts.tools.kills")
local axes         = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper (scripts/sweep/metrics/
-- entities.lua), so a test can stand a stub into this seam.
local function KILLS(a) return kills_tool.functions.kills(a) end

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
}

function M.read(_ctx, _axis, _a)
  local reply = KILLS({ all = true })
  -- kills{all=true} bounds itself the same way rockets{all=true} and
  -- entity_count{all=true} do (bounded.cut then bounded.fit), so this guard
  -- is normally forward-defensive; it still has to hold, the same rule every
  -- other metric in this catalog follows: a ranking over the survivors of a
  -- cut names whoever survived.
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      shown = reply.shown, total = reply.total,
      reason = "kills showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.kills }
  end
  return rows
end

return M
