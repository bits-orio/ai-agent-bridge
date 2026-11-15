-- AI Agent Bridge - scripts/tools/tech_status.lua
-- Author: bits-orio
-- License: MIT
--
-- tech_status: where one named technology stands for one force, what is
-- blocking it, and what it costs; or, with all=true, where it stands for
-- every force with players. The other half of the research pair is
-- scripts/tools/research.lua.
--
-- What it costs: a technology researched by labs names its science packs
-- per unit (research_unit_ingredients), how many units (research_unit_count)
-- and the energy of one unit (research_unit_energy), so "how many red packs
-- for logistics-2" is a product the model can be handed rather than guess.
-- research_unit_energy is in ticks, so a unit takes energy / 60 seconds at
-- lab speed 1: data's TechnologyUnit::time is in seconds, and automation's
-- 10 s unit reads 600 (measured on a fresh 2.1.23 map: automation 600,
-- logistics 900 for its 15 s, steel-processing 300 for 5 s). A technology unlocked by
-- a trigger instead (craft an item, build an entity) names the trigger's
-- type and sends no units, which read as "0 units" before.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaForce::technologies        R LuaCustomTable[string -> LuaTechnology]
--   LuaTechnology::name, researched, enabled, level, research_unit_count,
--     research_unit_energy (double), research_unit_ingredients
--     (array[ResearchIngredient], each {name, amount}), prototype
--   LuaTechnology::prerequisites  R dictionary[string -> LuaTechnology]
--   LuaTechnologyPrototype::research_trigger (ResearchTrigger, optional),
--     whose `type` is craft-item, mine-entity, craft-fluid,
--     send-item-to-orbit, capture-spawner, build-entity,
--     create-space-platform or scripted.

local force_lookup  = require("scripts.tools.force_lookup")
local tech_progress = require("scripts.tools.tech_progress")
local bounded       = require("scripts.tools.bounded")

local DEFAULT_PREREQS = 10
local MAX_PREREQS = 25
local TICKS_PER_SECOND = 60

local M = {}

M.manifest = {
  tech_status = {
    desc = "One technology for one force: researched, available (can start now), level, progress, missing prerequisites, and its cost: units, unit_time seconds per unit, ingredients (packs per unit), or research_trigger for a technology unlocked by doing something. all=true: researched, level and progress for every force with players.",
    params = {
      tech   = "string! technology prototype name, e.g. logistics-2",
      limit  = "integer prerequisite rows, default " .. DEFAULT_PREREQS .. ", max " .. MAX_PREREQS,
      all    = "boolean default false: one row per force with players; force ignored",
      offset = bounded.OFFSET_PARAM,
    },
  },
}

local function not_found(force_name, tech_name)
  return {
    found = false, force = force_name, tech = tech_name,
    reason = "this force has no technology by that name: names are prototype names such as logistics-2",
  }
end

--- The trigger type of a technology a trigger unlocks, or nil.
local function trigger_of(tech)
  local proto = tech.prototype
  local trigger = proto and proto.research_trigger
  return trigger and trigger.type or nil
end

--- What one unit of a lab-researched technology takes.
local function ingredients_of(tech)
  local out = {}
  for i, one in ipairs(tech.research_unit_ingredients or {}) do out[i] = { name = one.name, amount = one.amount } end
  return out
end

local function prerequisite_rows(tech, limit)
  local missing, rows = 0, {}
  for name, prereq in pairs(tech.prerequisites) do
    if not prereq.researched then missing = missing + 1 end
    rows[#rows + 1] = { name = name, researched = prereq.researched }
  end
  bounded.by_name(rows)
  return rows, missing, bounded.cut(rows, bounded.limit(limit, DEFAULT_PREREQS, MAX_PREREQS))
end

--- Writes units, unit_time and ingredients, or research_trigger, onto `out`.
local function cost_into(out, tech)
  local trigger = trigger_of(tech)
  if trigger then
    out.research_trigger = trigger
    return
  end
  out.units = tech.research_unit_count
  if tech.research_unit_energy then out.unit_time = bounded.round(tech.research_unit_energy / TICKS_PER_SECOND, 2) end
  local ingredients = ingredients_of(tech)
  if #ingredients > 0 then out.ingredients = ingredients end
end

local function progress_of(force, tech)
  local current = force.current_research
  return bounded.round(tech_progress.of(force, tech, current and current.name or nil), 2)
end

local function require_tech(a)
  if type(a.tech) ~= "string" or a.tech == "" then error("tech is required", 0) end
end

local function one_force(a)
  local force = force_lookup.require_force(a.force)
  require_tech(a)
  local tech = force.technologies[a.tech]
  if not tech then return not_found(force.name, a.tech) end
  local rows, missing, shown = prerequisite_rows(tech, a.limit)
  local out = {
    found = true, force = force.name, tech = tech.name,
    researched = tech.researched,
    enabled = tech.enabled,
    -- Available means it could be started now: a researched technology
    -- cannot, though every prerequisite of it is met.
    available = tech.enabled and missing == 0 and not tech.researched,
    level = tech.level,
    progress = progress_of(force, tech),
    prerequisites_total = #rows,
    prerequisites_missing = missing,
    prerequisites_shown = #shown,
    prerequisites = shown,
  }
  cost_into(out, tech)
  return out
end

--- all=true: one row per force with players, the same rule current_research
--- follows, sorted by force name and paged to the reply budget.
local function every_force(a)
  local rows = {}
  for _, force in pairs(game.forces) do
    local tech = #force.players > 0 and force.technologies[a.tech] or nil
    if tech then
      rows[#rows + 1] = { force = force.name, researched = tech.researched, level = tech.level,
                          progress = progress_of(force, tech) }
    end
  end
  if #rows == 0 and not prototypes.technology[a.tech] then return not_found(nil, a.tech) end
  table.sort(rows, function(x, y) return x.force < y.force end)
  return bounded.page(rows, a.offset, function(page) return { found = true, tech = a.tech, forces = page } end,
                      bounded.MAX_FORCES)
end

local function tech_status(a)
  if a.all == true then
    require_tech(a)
    return every_force(a)
  end
  return one_force(a)
end

M.functions = { tech_status = tech_status }

return M
