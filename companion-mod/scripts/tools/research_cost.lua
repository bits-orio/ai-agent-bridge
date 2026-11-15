-- AI Agent Bridge - scripts/tools/research_cost.lua
-- Author: bits-orio
-- License: MIT
--
-- research_cost: "how much science does my queue need", "how long until X".
-- For one technology, or the force's research queue, the units still to do,
-- what one unit costs, the packs needed in all and, where it can be said
-- honestly, how long that takes at the rate the force is consuming them now.
--
-- Units left: the running technology's progress is the force's
-- research_progress, every other one's is its saved_progress
-- (scripts/tools/tech_progress.lua). An infinite technology costs what the
-- engine says its current level costs (research_unit_count); the count
-- formula is never evaluated here, so a different level is refused.
--
-- The estimate: each pack's remaining count over the force's consumption of
-- that pack in the last ten minutes, summed over the surfaces its statistics
-- hold the pack on. A pack with no consumption has no estimate, and the
-- whole estimate is withheld then, because dividing by zero would promise a
-- finish that is never coming.
--
-- API names verified against runtime-api.json 2.1.23: LuaForce::research_queue,
-- ::technologies, ::get_item_production_statistics; LuaTechnology::name, level,
-- research_unit_count, research_unit_energy (ticks, as tech_status.lua
-- reads it), research_unit_ingredients, researched, saved_progress, prototype;
-- LuaTechnologyPrototype::research_trigger (ResearchTrigger.type);
-- LuaFlowStatistics::get_flow_count category "output" is consumption for
-- item statistics (LuaFlowStatistics class text).

local force_lookup  = require("scripts.tools.force_lookup")
local tech_progress = require("scripts.tools.tech_progress")
local stat_surfaces = require("scripts.tools.stat_surfaces")
local research      = require("scripts.tools.research")
local bounded       = require("scripts.tools.bounded")
local flow          = require("scripts.tools.flow")

local DEFAULT_QUEUE    = 10
local TICKS_PER_SECOND = 60
local RATE_WINDOW      = "ten_minutes"

local M = {}

M.manifest = {
  research_cost = {
    desc = "Science the research queue (or one tech) still needs: units left, packs per unit, packs in all, minutes_at_current_rate and bottleneck, or no_current_consumption.",
    params = {
      tech  = "string technology name; omit for the queue, up to " .. DEFAULT_QUEUE,
      level = "integer infinite technology level; only the current one is known",
    },
  },
}

local function trigger_of(tech)
  local trigger = tech.prototype and tech.prototype.research_trigger
  return trigger and trigger.type or nil
end

--- One technology's row, and its packs as {name, count} still to be consumed.
local function tech_row(force, tech, current_name, queued)
  local row = { tech = tech.name, level = tech.level, queued = queued > 1 and queued or nil }
  local trigger = trigger_of(tech)
  if trigger then
    row.research_trigger = trigger
    return row, {}
  end
  local progress = tech.researched and 1 or (tech_progress.of(force, tech, current_name) or 0)
  -- A float product like 20 * 0.75 can land a hair above its integer; the
  -- epsilon keeps ceil from calling 15.000000000000002 sixteen.
  local remaining = math.ceil(tech.research_unit_count * (1 - progress) - 1e-6)
  row.units, row.remaining = tech.research_unit_count, math.max(0, remaining)
  row.progress = bounded.round(progress, 2)
  if tech.research_unit_energy then row.unit_time = bounded.round(tech.research_unit_energy / TICKS_PER_SECOND, 2) end
  row.ingredients, row.packs = {}, {}
  for i, one in ipairs(tech.research_unit_ingredients or {}) do
    row.ingredients[i] = { name = one.name, amount = one.amount }
    row.packs[#row.packs + 1] = { name = one.name, count = one.amount * row.remaining }
  end
  local packs = row.packs
  row.packs = nil
  return row, packs
end

--- The technologies to cost: the one asked for, or the queue with a repeated
--- levelled technology listed once (one LuaTechnology reads one level).
local function selection(a, force)
  if a.tech ~= nil then
    local tech = type(a.tech) == "string" and force.technologies[a.tech] or nil
    if not tech then
      return nil, { found = false, force = force.name, tech = tostring(a.tech),
                    reason = "this force has no technology by that name: names are prototype names such as logistics-2" }
    end
    if a.level ~= nil and tonumber(a.level) ~= tech.level then
      return nil, { found = false, force = force.name, tech = tech.name,
                    reason = "only the current level (" .. tech.level .. ") has a cost the engine reports; formulas are not evaluated" }
    end
    return { { tech = tech, queued = 1 } }
  end
  local list, at = {}, {}
  for _, entry in ipairs(force.research_queue or {}) do
    local name = research.entry_name(entry)
    local tech = name and force.technologies[name]
    if tech then
      if at[name] then list[at[name]].queued = list[at[name]].queued + 1
      elseif #list < DEFAULT_QUEUE then
        list[#list + 1] = { tech = tech, queued = 1 }
        at[name] = #list
      end
    end
  end
  return list
end

--- Items per minute the force consumes of `pack` over the rate window.
local function consumption(force, pack, qualities, surfaces)
  local window, total = flow.require_window(RATE_WINDOW), 0
  for _, surface in ipairs(stat_surfaces.where(force, surfaces, function(f, s) return stat_surfaces.item_seen(f, s, pack, qualities) end)) do
    local stats = force.get_item_production_statistics(surface)
    total = total + flow.item_flow(stats, { item = pack, qualities = qualities, window = window }, "output")
  end
  return total
end

--- Pack totals as rows with rates and minutes, plus the bottleneck estimate.
local function pack_rows(force, needed)
  local qualities, surfaces = flow.quality_names(), flow.surfaces_for({})
  local rows, worst, blocked = {}, nil, false
  for name, count in pairs(needed) do
    local row = { pack = name, needed = count }
    local rate = consumption(force, name, qualities, surfaces)
    if rate > 0 then
      row.per_minute = bounded.round(rate, 2)
      row.minutes = bounded.round(count / rate, 2)
      if not worst or count / rate > worst.minutes then worst = { pack = name, minutes = count / rate } end
    elseif count > 0 then
      row.no_current_consumption, blocked = true, true
    end
    rows[#rows + 1] = row
  end
  table.sort(rows, function(x, y)
    if x.needed ~= y.needed then return x.needed > y.needed end
    return x.pack < y.pack
  end)
  return rows, worst, blocked
end

local function research_cost(a)
  local force = force_lookup.require_force(a.force)
  local chosen, refusal = selection(a, force)
  if not chosen then return refusal end
  local current = force.current_research
  local current_name = current and current.name or nil
  local rows, needed = {}, {}
  for i, one in ipairs(chosen) do
    local packs
    rows[i], packs = tech_row(force, one.tech, current_name, one.queued)
    for _, p in ipairs(packs) do needed[p.name] = (needed[p.name] or 0) + p.count end
  end
  local packs, worst, blocked = pack_rows(force, needed)
  local function build(shown)
    return {
      found = true, force = force.name, queued = #chosen, technologies = shown, packs = packs,
      estimate = blocked and "no_current_consumption" or nil,
      bottleneck = (not blocked and worst) and worst.pack or nil,
      minutes_at_current_rate = (not blocked and worst) and bounded.round(worst.minutes, 2) or nil,
    }
  end
  return build(bounded.fit_reply(rows, build))
end

M.functions = { research_cost = research_cost }

return M
