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
local pages = require("scripts.sweep.pages")
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

--- One column of kills{all=true}, as the rows a sweep ranks. The column is
--- the only difference between kills and enemies_killed, so both read here.
function M.read_field(field)
  -- Every page of kills{all=true}: a ranking over the first page alone
  -- would name whoever survived the byte cut (scripts/sweep/pages.lua).
  local forces, refusal = pages.collect(KILLS, { all = true }, "forces", "kills")
  if not forces then return nil, refusal end
  local rows = {}
  for _, row in ipairs(forces) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row[field] }
  end
  return rows
end

function M.read(_ctx, _axis, _a)
  return M.read_field("kills")
end

return M
