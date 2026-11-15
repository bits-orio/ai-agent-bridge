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
local pages = require("scripts.sweep.pages")
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
  -- The delegate runs one get_trains per force over every surface at once,
  -- and the engine builds a LuaTrain wrapper for every train it returns, so
  -- unlike kills and built this is not a counter read: its cost is one read
  -- per force plus one wrapper per train. The reads are what is predicted;
  -- the wrappers are bounded by the trains that exist and are built the
  -- same whichever surface filter is used, so no prediction could save them.
  costly = true,
  max_passes = 600,
  refusal_hint = "ask trains for one force",
}

function M.predict(ctx, _axis, _a) return #ctx.forces end

function M.read(_ctx, _axis, _a)
  -- Every page of trains{all=true}; each page re-reads the trains, so a
  -- server past one page (about fifty forces) pays the wrappers once a page.
  local forces, refusal = pages.collect(TRAINS, { all = true }, "forces", "trains")
  if not forces then return nil, refusal end
  local rows = {}
  for _, row in ipairs(forces) do
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = row.trains }
  end
  return rows
end

return M
