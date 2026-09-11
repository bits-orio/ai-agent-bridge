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

local M = {}

M.manifest = {
  rockets = {
    desc = "Rockets one force has launched, and the items sent up, largest count first. distinct_items beside shown says if there are more kinds.",
    params = {
      limit = "integer item rows, default " .. DEFAULT_ITEMS .. ", max " .. MAX_ITEMS,
    },
  },
}

local function rockets(a)
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
