-- AI Agent Bridge - scripts/tools/trains.lua
-- Author: bits-orio
-- License: MIT
--
-- trains: how many trains one force has, and how many are moving or under
-- manual control right now, over every surface unless one is named.
--
-- LuaTrainManager::get_trains(TrainFilter) builds one LuaTrain wrapper per
-- matching train (verified: the return type is array[LuaTrain], each a real
-- object, not a count), so this is tier 1 honestly rather than a true O(1)
-- counter: cheap against an ordinary base, proportional to the train count on
-- one that runs thousands. moving and manual are read off each wrapper this
-- call already built (LuaTrain::speed, ::manual_mode) rather than asking the
-- engine twice more with TrainFilter's own is_moving/is_manual, which would
-- triple the wrapper cost for numbers this one call already hands over.
--
-- API names verified against /home/shobhitg/factorio/doc-html/runtime-api.json
-- (2.0.77):
--   LuaGameScript::train_manager -> LuaTrainManager, never nil (`optional:
--     false`).
--   LuaTrainManager::get_trains(filter) -> array[LuaTrain]; `filter` is a
--     required TrainFilter (`optional: false`), not an optional argument the
--     way most search filters in this catalog are.
--   TrainFilter (concepts.TrainFilter): force, surface, is_manual, is_moving,
--     has_passenger, min_stocks, max_stocks, stock, group, train_id, every
--     field itself optional.
--   LuaTrain::speed (double), "Current speed"; TrainFilter's own is_moving
--     "Checks if train is moving (has speed != 0) or not moving", the exact
--     test used below.
--   LuaTrain::manual_mode (boolean), "true" means player- or script-driven,
--     what TrainFilter's is_manual checks. LuaTrain carries no `force` or
--     `surface` attribute of its own (checked against its full attribute
--     list), which is why counting by either goes through the filter rather
--     than a field read after the fact.

local force_lookup = require("scripts.tools.force_lookup")
local bounded       = require("scripts.tools.bounded")
local flow          = require("scripts.tools.flow")

local MAX_BREAKDOWN = 5

local M = {}

M.manifest = {
  trains = {
    desc = "How many trains one force has, and how many are moving or in manual control right now, over every surface unless one is named. Builds one wrapper per train, so name a surface on a very large fleet. all=true answers every force that has players in one call, summed over every surface, most trains first: use it for any each-team or every-force question instead of one call per force. Unknown surface: found=false.",
    tier = "1",
    params = {
      surface = "string surface name or index, e.g. nauvis; omit for every surface",
      all     = "boolean default false: one row per force with players, summed over every surface; force and surface ignored",
    },
  },
}

--- One surface's trains for one force, counted rather than kept: the wrapper
--- array is walked once and dropped, so nothing here holds more than one
--- surface's worth of LuaTrain objects at a time.
local function counts_for(force, surface)
  local trains = game.train_manager.get_trains{ force = force.name, surface = surface.name }
  local moving, manual = 0, 0
  for _, train in ipairs(trains) do
    if train.speed ~= 0 then moving = moving + 1 end
    if train.manual_mode then manual = manual + 1 end
  end
  return #trains, moving, manual
end

-- all=true is the every-team question in one call. The rows are forces that
-- have players, the same rule current_research's own sweep follows: an empty
-- slot owns no trains anywhere.
local function trains_all()
  local surfaces = flow.surfaces_for({})
  local rows = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then
      local total, moving, manual = 0, 0, 0
      for _, surface in ipairs(surfaces) do
        local here_total, here_moving, here_manual = counts_for(force, surface)
        total, moving, manual = total + here_total, moving + here_moving, manual + here_manual
      end
      rows[#rows + 1] = { force = force.name, trains = total, moving = moving, manual = manual }
    end
  end
  table.sort(rows, function(x, y)
    if x.trains ~= y.trains then return x.trains > y.trains end
    return x.force < y.force
  end)
  local shown = bounded.fit(bounded.cut(rows, bounded.MAX_FORCES))
  return { total = #rows, shown = #shown, forces = shown }
end

local function trains(a)
  if a.all == true then return trains_all() end
  local force = force_lookup.require_force(a.force)
  local surfaces, miss = flow.surfaces_for(a)
  if not surfaces then
    miss.force = force.name
    return miss
  end

  local total, moving, manual = 0, 0, 0
  local surface_rows = {}
  for _, surface in ipairs(surfaces) do
    local here_total, here_moving, here_manual = counts_for(force, surface)
    total, moving, manual = total + here_total, moving + here_moving, manual + here_manual
    if here_total > 0 or #surfaces == 1 then
      surface_rows[#surface_rows + 1] =
        { surface = surface.name, trains = here_total, moving = here_moving, manual = here_manual }
    end
  end
  table.sort(surface_rows, function(x, y)
    if x.trains ~= y.trains then return x.trains > y.trains end
    return x.surface < y.surface
  end)
  local shown_surfaces = bounded.cut(surface_rows, MAX_BREAKDOWN)

  return {
    found = true,
    force = force.name,
    surface = #surfaces == 1 and surfaces[1].name or "all",
    trains = total, moving = moving, manual = manual,
    surfaces_counted = #surface_rows,
    surfaces = shown_surfaces,
  }
end

M.functions = { trains = trains }

return M
