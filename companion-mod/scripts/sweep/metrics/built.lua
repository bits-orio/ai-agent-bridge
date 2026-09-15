-- AI Agent Bridge - scripts/sweep/metrics/built.lua
-- Author: bits-orio
-- License: MIT
--
-- The built sweep metric: how many entities each force has built. Delegates
-- entirely to scripts/tools/built.lua's own `built` function with all=true,
-- the tool that already sums LuaForce::get_entity_build_count_statistics
-- over every surface. Force axis only, the same reason kills.lua's own
-- metric gives: built{all=true} always sums every surface into one row.

local built_tool = require("scripts.tools.built")
local axes         = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load, the same reason
-- entities.lua gives for its own delegate wrapper, so a test can stand a
-- stub into this seam.
local function BUILT(a) return built_tool.functions.built(a) end

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
}

function M.read(_ctx, _axis, _a)
  local reply = BUILT({ all = true })
  if reply.total and reply.shown and reply.shown < reply.total then
    return nil, {
      shown = reply.shown, total = reply.total,
      reason = "built showed " .. reply.shown .. " of " .. reply.total ..
               " rows, so a ranking over them would name whoever survived the cut",
    }
  end
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.built }
  end
  return rows
end

return M
