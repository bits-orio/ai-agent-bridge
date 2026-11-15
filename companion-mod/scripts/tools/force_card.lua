-- AI Agent Bridge - scripts/tools/force_card.lua
-- Author: bits-orio
-- License: MIT
--
-- force_card: one compact summary of one force, so "how is this force doing"
-- or "pros and cons of my force" is one read instead of five. It only
-- gathers: every figure comes from a helper the single-purpose tools already
-- use (production's ranking, logistics' robot sums, kills' enemy count), so
-- the card can never disagree with the tool it summarises.
--
-- Cheap on purpose: engine counters only, no entity walk, and at most
-- MAX_SURFACES surfaces read for each of the three per-surface figures. A
-- cut is said with surfaces_cut. A key with no value is left out, never nil.
--
-- API names verified against runtime-api.json 2.1.23: LuaForce::players,
-- connected_players, technologies (LuaTechnology::researched),
-- current_research, research_progress, research_queue, rockets_launched,
-- logistic_networks; LuaGameScript::ticks_played.

local force_lookup  = require("scripts.tools.force_lookup")
local flow          = require("scripts.tools.flow")
local bounded       = require("scripts.tools.bounded")
local rates_all     = require("scripts.tools.rates_all")
local production    = require("scripts.tools.production")
local logistics     = require("scripts.tools.logistics")
local kills         = require("scripts.tools.kills")
local tech_progress = require("scripts.tools.tech_progress")

local WINDOW = "one_hour"
local TOP_ITEMS = 5
local MAX_SURFACES = rates_all.MAX_SURFACES

local M = {}

M.manifest = {
  force_card = {
    desc = "One summary of one force: players, research, rockets, robots, enemies killed and top items made per minute. Use it first for any 'how is this force doing' or pros and cons question, before separate tools.",
  },
}

local function count_researched(force)
  local n = 0
  for _, tech in pairs(force.technologies) do
    if tech.researched then n = n + 1 end
  end
  return n
end

--- Robots over the force's networks on its MAX_SURFACES best-stocked
--- surfaces, and whether more surfaces held networks than were read.
local function robots_of(force)
  local rows = {}
  for surface_name in pairs(force.logistic_networks) do
    local logistic, construction = logistics.robot_totals(force, surface_name)
    rows[#rows + 1] = { name = surface_name, logistic = logistic, construction = construction }
  end
  table.sort(rows, function(x, y)
    local xr, yr = x.logistic + x.construction, y.logistic + y.construction
    if xr ~= yr then return xr > yr end
    return x.name < y.name
  end)
  local logistic, construction = 0, 0
  for _, row in ipairs(bounded.cut(rows, MAX_SURFACES)) do
    logistic, construction = logistic + row.logistic, construction + row.construction
  end
  return logistic, construction, #rows > MAX_SURFACES
end

local function top_items_of(force, surfaces, window)
  local ranked = production.top_rows(rates_all.item_rates(force, surfaces, window), TOP_ITEMS)
  local out = {}
  for i, row in ipairs(ranked) do out[i] = { item = row.item, per_min = row.produced_per_min } end
  return out
end

local function force_card(a)
  local force = force_lookup.require_force(a.force)
  local window = flow.require_window(WINDOW)
  local covered, with_stats = rates_all.surfaces_with_output(force)
  local logistic, construction, robots_cut = robots_of(force)
  local current = force.current_research
  local current_name = current and current.name or nil

  local reply = {
    found = true,
    force = force.name,
    players_total = #force.players,
    players_online = #force.connected_players,
    researched = count_researched(force),
    current_research = current_name,
    research_progress = current and bounded.round(tech_progress.of(force, current, current_name), 2) or nil,
    queue = #(force.research_queue or {}),
    rockets = force.rockets_launched or 0,
    robots_total = logistic + construction,
    logistic_robots = logistic,
    construction_robots = construction,
    enemies_killed = kills.enemies_killed(force, covered),
    surfaces = with_stats,
    top_items = top_items_of(force, covered, window),
    window = WINDOW,
  }
  if with_stats > #covered or robots_cut then reply.surfaces_cut = true end
  -- window_age.stamp would add two more keys the card does not carry; the
  -- flag is the one part of it a reader needs, so it is set here the same way.
  if game.ticks_played < window.ticks then reply.partial_window = true end
  return reply
end

M.functions = { force_card = force_card }

return M
