-- AI Agent Bridge - scripts/tools/rockets.lua
-- Author: bits-orio
-- License: MIT
--
-- rockets: how many rockets one force has launched, and what went up in them.
-- Both are counters the engine keeps on the force, so this costs nothing however
-- long the save has been running.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaForce.html:
--   LuaForce::rockets_launched  RW uint32, "The number of rockets launched."
--   LuaForce::items_launched    R dictionary[string -> ItemCountType], "All of
--                               the items that have been launched in rockets."

local force_lookup = require("scripts.tools.force_lookup")
local bounded      = require("scripts.tools.bounded")

local DEFAULT_ITEMS = 10
local MAX_ITEMS = 25
local MAX_FORCES = bounded.MAX_FORCES

local M = {}

M.manifest = {
  rockets = {
    desc = "Rockets one force has launched, and the items sent up, largest count first. distinct_items beside shown says if there are more kinds. all=true answers every force in one call, counts only, no items and limit ignored: use it for any each-team or every-force question instead of one call per force.",
    params = {
      limit = "integer item rows, default " .. DEFAULT_ITEMS .. ", max " .. MAX_ITEMS,
      all   = "boolean default false: one row per force with players or a launch, no items; force ignored",
    },
  },
}

-- all=true is the every-team question in one call. The rows are forces that
-- have players or have launched at least one rocket; an empty slot has
-- neither. No item list here: fourteen forces of item rows would blow the
-- reply budget, so items stay a single-force answer and distinct_items is
-- what tells the model there is more to ask for.
local function rockets_all()
  local rows = {}
  for _, force in pairs(game.forces) do
    local launched = force.rockets_launched or 0
    if #force.players > 0 or launched > 0 then
      local distinct_items = 0
      for _ in pairs(force.items_launched or {}) do distinct_items = distinct_items + 1 end
      rows[#rows + 1] = {
        force = force.name,
        rockets_launched = launched,
        distinct_items = distinct_items,
      }
    end
  end
  -- Largest first, not alphabetical. bounded.cut keeps the first rows, so a
  -- sweep sorted by name discards the biggest launcher before the smallest on
  -- any server with more forces than the cap, on the one tool whose entire
  -- purpose is naming the biggest.
  table.sort(rows, function(x, y)
    if x.rockets_launched ~= y.rockets_launched then return x.rockets_launched > y.rockets_launched end
    return x.force < y.force
  end)
  local shown = bounded.cut(rows, MAX_FORCES)
  return { total = #rows, shown = #shown, forces = shown }
end

local function rockets(a)
  if a.all == true then
    return rockets_all()
  end
  local force = force_lookup.require_force(a.force)

  local rows = {}
  for name, count in pairs(force.items_launched) do
    rows[#rows + 1] = { name = name, count = count }
  end
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.name < y.name
  end)

  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_ITEMS, MAX_ITEMS))
  return {
    force = force.name,
    rockets_launched = force.rockets_launched,
    distinct_items = #rows,
    shown = #shown,
    items = shown,
  }
end

M.functions = { rockets = rockets }

return M
