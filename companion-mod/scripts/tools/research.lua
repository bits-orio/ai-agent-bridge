-- research_queue and tech_status: what a force is researching now, what is
-- lined up behind it, and where one named technology stands.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaForce.html and
-- https://lua-api.factorio.com/2.0.77/classes/LuaTechnology.html:
--   LuaForce::research_queue      RW array[TechnologyID], "The research queue
--                                 of this force."
--   LuaForce::current_research    R LuaTechnology?
--   LuaForce::research_progress   RW double, "Progress of current research, as
--                                 a number in range [0, 1]."
--   LuaForce::technologies        R LuaCustomTable[string -> LuaTechnology]
--   LuaTechnology::name, researched, enabled, level, research_unit_count
--   LuaTechnology::prerequisites  R dictionary[string -> LuaTechnology]
--   LuaTechnology::saved_progress RW double, "Saved technology progress
--                                 fraction as a value in range [0, 1)."

local force_lookup = require("scripts.tools.force_lookup")
local bounded      = require("scripts.tools.bounded")

local DEFAULT_QUEUE = 10
local MAX_QUEUE = 25
local DEFAULT_PREREQS = 10
local MAX_PREREQS = 25

local M = {}

M.manifest = {
  research_queue = {
    desc = "What one force is researching and what is queued behind it, in the order the engine will work through it: technology name, level, research units needed, progress. Use this rather than current_research whenever the question is about what comes next or how long the plan is. The reply reports queued beside shown, so raise limit when the queue is longer than came back.",
    params = {
      limit = "integer how many queue rows to return, default " .. DEFAULT_QUEUE .. ", at most " .. MAX_QUEUE,
    },
  },
  tech_status = {
    desc = "Where one named technology stands for one force: researched, enabled, available (every prerequisite researched), current level, research units, progress, and which prerequisites are still missing. Use it to answer \"can we research X yet\" and \"what is blocking X\". An unknown name comes back as found = false rather than an error, so guess the prototype name and read the reply.",
    params = {
      tech  = "string! technology prototype name, for example logistics-2 or mining-productivity-1",
      limit = "integer how many prerequisite rows to return, default " .. DEFAULT_PREREQS .. ", at most " .. MAX_PREREQS,
    },
  },
}

-- research_queue is typed array[TechnologyID], and TechnologyID is a union of a
-- LuaTechnology, a LuaTechnologyPrototype and a plain prototype name. Read the
-- name off whichever of the three came back instead of assuming one of them.
local function entry_name(entry)
  if type(entry) == "string" then return entry end
  if entry == nil then return nil end
  local name = entry.name
  if type(name) == "string" then return name end
  return nil
end

-- The running technology's progress lives on the force; everything else keeps
-- the fraction it had when it was last worked on, on the technology itself.
local function progress_of(force, tech, current_name)
  if not tech then return nil end
  if current_name and tech.name == current_name then return force.research_progress end
  return tech.saved_progress
end

local function research_queue(a)
  local force = force_lookup.require_force(a.force)
  local current = force.current_research
  local current_name = current and current.name or nil

  local rows = {}
  for position, entry in ipairs(force.research_queue or {}) do
    local name = entry_name(entry)
    local tech = name and force.technologies[name] or nil
    rows[#rows + 1] = {
      position = position,
      tech = name,
      level = tech and tech.level or nil,
      units = tech and tech.research_unit_count or nil,
      progress = progress_of(force, tech, current_name),
    }
  end

  -- Already in the order the engine will research them, so cut without sorting:
  -- position 1 is what is running now and the tail is what a player would cancel.
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_QUEUE, MAX_QUEUE))
  return {
    force = force.name,
    researching = current_name,
    progress = current and force.research_progress or nil,
    queued = #rows,
    shown = #shown,
    queue = shown,
  }
end

local function tech_status(a)
  local force = force_lookup.require_force(a.force)
  if type(a.tech) ~= "string" or a.tech == "" then error("tech is required") end

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
    progress = progress_of(force, tech, current and current.name or nil),
    prerequisites_total = #rows,
    prerequisites_missing = missing,
    prerequisites_shown = #shown,
    prerequisites = shown,
  }
end

M.functions = { research_queue = research_queue, tech_status = tech_status }

return M
