-- tech_status: where one named technology stands for one force, and what is
-- blocking it. The other half of the research pair is
-- scripts/tools/research.lua.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaTechnology.html:
--   LuaForce::technologies        R LuaCustomTable[string -> LuaTechnology]
--   LuaTechnology::name, researched, enabled, level, research_unit_count
--   LuaTechnology::prerequisites  R dictionary[string -> LuaTechnology]

local force_lookup  = require("scripts.tools.force_lookup")
local tech_progress = require("scripts.tools.tech_progress")
local bounded       = require("scripts.tools.bounded")

local DEFAULT_PREREQS = 10
local MAX_PREREQS = 25

local M = {}

M.manifest = {
  tech_status = {
    desc = "One technology's state for one force: researched, enabled, available, level, units, progress, missing prerequisites (can we research X yet, what blocks X). Unknown name: found=false.",
    params = {
      tech  = "string! technology prototype name, e.g. logistics-2",
      limit = "integer prerequisite rows, default " .. DEFAULT_PREREQS .. ", max " .. MAX_PREREQS,
    },
  },
}

local function tech_status(a)
  local force = force_lookup.require_force(a.force)
  if type(a.tech) ~= "string" or a.tech == "" then error("tech is required", 0) end

  local tech = force.technologies[a.tech]
  if not tech then
    return {
      found = false, force = force.name, tech = a.tech,
      reason = "this force has no technology by that name: names are prototype names such as logistics-2",
    }
  end

  local missing = 0
  local rows = {}
  for name, prereq in pairs(tech.prerequisites) do
    if not prereq.researched then missing = missing + 1 end
    rows[#rows + 1] = { name = name, researched = prereq.researched }
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_PREREQS, MAX_PREREQS))

  local current = force.current_research
  return {
    found = true, force = force.name, tech = tech.name,
    researched = tech.researched,
    enabled = tech.enabled,
    available = tech.enabled and missing == 0,
    level = tech.level,
    units = tech.research_unit_count,
    progress = bounded.round(tech_progress.of(force, tech, current and current.name or nil), 2),
    prerequisites_total = #rows,
    prerequisites_missing = missing,
    prerequisites_shown = #shown,
    prerequisites = shown,
  }
end

M.functions = { tech_status = tech_status }

return M
