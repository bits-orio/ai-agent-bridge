-- AI Agent Bridge - scripts/sweep/metrics/researched.lua
-- Author: bits-orio
-- License: MIT
--
-- The researched sweep metric: how many technologies each force has
-- finished, the "who is furthest ahead in research" number. The research
-- metric beside it ranks progress through the current technology alone,
-- which reads a team 90% through automation-2 as ahead of one 10% into
-- space science.
--
-- The engine keeps no per-force count of researched technologies, so this
-- walks force.technologies and counts .researched: one read per technology
-- per force, a few hundred on Space Age, bounded by the forces walk.lua
-- hands it (the ones with players). A levelled technology counts once
-- however many levels are done, the way the technology screen shows it.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaForce::technologies, LuaCustomTable[string -> LuaTechnology], walked
--   with pairs; LuaTechnology::researched (boolean).

local axes = require("scripts.sweep.axes")

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count of researched technologies",
  places = 0,
  costly = false,
}

local function researched_count(force)
  local n = 0
  for _, tech in pairs(force.technologies or {}) do
    if tech.researched then n = n + 1 end
  end
  return n
end

function M.read(ctx, _axis, _a)
  local rows = {}
  for _, force in ipairs(ctx.forces) do
    rows[#rows + 1] = { name = axes.force({ force = force.name }), value = researched_count(force) }
  end
  return rows
end

return M
