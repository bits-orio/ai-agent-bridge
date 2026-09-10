-- research_queue: what a force is researching now and what is lined up behind
-- it. tech_status, the other half of the research pair, lives in
-- scripts/tools/tech_status.lua.
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
--   LuaTechnology::name, research_unit_count
--   LuaTechnology::level          RW uint32, "The current level of this
--                                 technology." One LuaTechnology per name, so
--                                 every queued repeat of a level-based
--                                 technology reads the SAME current level.

local force_lookup  = require("scripts.tools.force_lookup")
local tech_progress = require("scripts.tools.tech_progress")
local bounded       = require("scripts.tools.bounded")

local DEFAULT_QUEUE = 10
local MAX_QUEUE = 25

local M = {}

M.manifest = {
  research_queue = {
    desc = "What one force is researching and what is queued behind it, in engine order: technology name, level, research units, progress. Use this rather than current_research for what comes next or how long the plan is. A level-based technology queued more than once gets one row per level, each naming the level it will research; units and progress belong to the first row only, so never sum units over the repeats. queued beside shown says whether the queue was longer than came back.",
    params = {
      limit = "integer how many queue rows to return, default " .. DEFAULT_QUEUE .. ", at most " .. MAX_QUEUE,
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

-- One row of the queue. `earlier` is how many entries of this same name came
-- before it, which is what makes a repeated technology readable: queueing mining
-- productivity three times is three entries resolving to one LuaTechnology, so
-- level, research_unit_count and the force's progress all describe the FIRST of
-- them. The level each row will research is the current level plus the repeats
-- ahead of it; units and progress are reported for the first row alone, because
-- the later levels' costs live in the prototype's count formula and nothing is
-- in progress on them yet.
local function queue_row(force, tech, name, position, earlier, current_name)
  local first = earlier == 0
  return {
    position = position,
    tech = name,
    level = tech and (tech.level + earlier) or nil,
    units = (first and tech) and tech.research_unit_count or nil,
    progress = first and bounded.round(tech_progress.of(force, tech, current_name), 2) or nil,
  }
end

local function research_queue(a)
  local force = force_lookup.require_force(a.force)
  local current = force.current_research
  local current_name = current and current.name or nil

  local rows = {}
  local seen = {}
  for position, entry in ipairs(force.research_queue or {}) do
    local name = entry_name(entry)
    local tech = name and force.technologies[name] or nil
    local earlier = (name and seen[name]) or 0
    if name then seen[name] = earlier + 1 end
    rows[#rows + 1] = queue_row(force, tech, name, position, earlier, current_name)
  end

  -- Already in the order the engine will research them, so cut without sorting:
  -- position 1 is what is running now and the tail is what a player would cancel.
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_QUEUE, MAX_QUEUE))
  return {
    force = force.name,
    researching = current_name,
    progress = current and bounded.round(force.research_progress, 2) or nil,
    queued = #rows,
    shown = #shown,
    queue = shown,
  }
end

M.functions = { research_queue = research_queue }

return M
