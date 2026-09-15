-- AI Agent Bridge - scripts/sweep/metrics/research.lua
-- Author: bits-orio
-- License: MIT
--
-- The research sweep metric: which force is furthest through its current
-- research, as a whole-number percent, 0 to 100. Delegates the sweep itself,
-- which forces are in play and what each is researching, to
-- scripts/tools/basics.lua's own `current_research` function with all=true;
-- no code here decides which forces have research running, current_research
-- already does.
--
-- What it does NOT take on trust from that reply is the progress number
-- itself. current_research's own row already rounds it through
-- bounded.round for display (basics.lua's research_row), which turns
-- anything but a whole number into a short decimal STRING, "0.25" rather
-- than 0.25. envelope.lua sorts on the raw value before it rounds anything
-- for the wire, and Lua's `>` throws comparing a number to a string, so
-- taking the already-rounded field here would either sort text
-- lexicographically ("0.10" before "0.9") or crash outright the first time
-- one force is researching and another is not (a bare 0 next to a rounded
-- string). Reading LuaForce::research_progress directly, the same field
-- current_research itself reads before rounding it, keeps the value a real
-- number all the way to envelope.lua's own rounding step. The sweep it
-- belongs to is still read exactly once.
--
-- Reported as a percent rather than the [0, 1] fraction current_research
-- itself uses, and rounded to a whole number (places = 0), for a second
-- reason beside readability: bounded.round only ever returns a Lua NUMBER
-- when it rounds to zero places, since a value rounded to the nearest whole
-- number is always already whole (bounded.lua: "if rounded ==
-- math.floor(rounded) then return math.floor(rounded)"). Any places above
-- zero returns a short decimal STRING for anything but a whole number, which
-- is exactly right for a human-facing field but is not a JSON number any
-- more, and service/internal/catalog/sweep.go's rankSweep decodes every
-- row's value straight into a Go float64. A fractional research value at
-- places = 2 would silently drop that metric out of ranking, the reply
-- still correct but quietly missing the leader/margin fields every other
-- metric gets. Whole percents keep this metric inside that gate too.

local basics_tool = require("scripts.tools.basics")
local axes         = require("scripts.sweep.axes")

local CURRENT_RESEARCH = basics_tool.functions.current_research

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "percent",
  places = 0,
  costly = false,
}

function M.read(_ctx, _axis, _a)
  local reply = CURRENT_RESEARCH({ all = true })
  local rows = {}
  for _, row in ipairs(reply.forces or {}) do
    local force = game.forces[row.force]
    local raw_progress = (force and row.researching) and force.research_progress or 0
    rows[#rows + 1] = { name = axes.force({ force = row.force }), value = raw_progress * 100 }
  end
  return rows
end

return M
