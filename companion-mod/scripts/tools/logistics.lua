-- AI Agent Bridge - scripts/tools/logistics.lua
-- Author: bits-orio
-- License: MIT
--
-- logistics_summary: the logistic networks one force has on one surface, read
-- off the force's own list of them. The engine already groups its networks by
-- surface, so nothing here walks entities.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10),
-- https://lua-api.factorio.com/2.0.77/classes/LuaForce.html and
-- https://lua-api.factorio.com/2.0.77/classes/LuaLogisticNetwork.html:
--   LuaForce::logistic_networks   R dictionary[string -> array[
--                                 LuaLogisticNetwork]], "List of logistic
--                                 networks, grouped by surface."
--   LuaLogisticNetwork::network_id, cells,
--     all_logistic_robots, available_logistic_robots,
--     all_construction_robots, available_construction_robots
--   LuaLogisticNetwork::get_contents(member?) -> array[ItemWithQualityCount],
--     each {name, quality, count}. Aggregated by name in
--     scripts/tools/logistics_contents.lua.

local force_lookup      = require("scripts.tools.force_lookup")
local surface_lookup    = require("scripts.tools.surface_lookup")
local contents_of       = require("scripts.tools.logistics_contents")
local bounded           = require("scripts.tools.bounded")

-- Five networks of eight item rows each is what fits the reply cap with room to
-- spare. A mall surface can hold dozens of one-roboport networks, and the
-- question behind this tool is always about the big ones.
local DEFAULT_NETWORKS = 5
local MAX_NETWORKS = 5
local CONTENTS_PER_NETWORK = 8

local M = {}

M.manifest = {
  logistics_summary = {
    desc = "Logistic networks of one force on one surface, busiest first: robots, idle robots, cells, eight largest item stacks over all qualities (do we have enough bots, what is in the chests). Unknown surface: found=false.",
    params = {
      surface  = "string! surface name or index, e.g. nauvis",
      limit    = "integer networks, default " .. DEFAULT_NETWORKS .. ", max " .. MAX_NETWORKS,
      contents = "boolean include each network's item contents, default true; false skips the get_contents read entirely, for a caller that only wants robot or network counts",
    },
  },
}

-- `with_contents = false` skips contents_of.largest entirely, the one call
-- in this function that reaches get_contents(): an ItemWithQualityCount
-- array built for the WHOLE network (logistics_contents.lua's own header),
-- the one expensive thing this tool does. A sweep counting robots or
-- networks across every force and surface never reads what is in a chest,
-- so it asks for this. Every existing caller leaves `with_contents` nil,
-- which reads as true here, so nothing already calling this tool changes.
local function network_row(network, with_contents)
  local row = {
    id = network.network_id,
    cells = #network.cells,
    logistic_robots = network.all_logistic_robots,
    logistic_robots_available = network.available_logistic_robots,
    construction_robots = network.all_construction_robots,
    construction_robots_available = network.available_construction_robots,
  }
  if with_contents ~= false then
    local contents, distinct = contents_of.largest(network, CONTENTS_PER_NETWORK)
    row.distinct_items = distinct
    row.contents_shown = #contents
    row.contents = contents
  end
  return row
end

local function logistics_summary(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end

  -- Rank on the cheap reads first, cut, and only then ask the surviving
  -- networks for their contents. A mall surface can hold dozens of one-roboport
  -- networks, and get_contents on every one of them would be paid for rows the
  -- reply was never going to carry.
  -- The whole-surface totals are summed here, over every network and before
  -- the cut, from counters the network already keeps. A sweep that wants
  -- "robots per force" reads these and never the rows: the rows are cut to a
  -- handful on purpose, a mall surface can hold dozens of one-roboport
  -- networks, and a sum over the survivors of that cut is not a total.
  local order = {}
  local logistic_total = 0
  for _, network in pairs(force.logistic_networks[surface.name] or {}) do
    logistic_total = logistic_total + network.all_logistic_robots
    order[#order + 1] = {
      network = network,
      id = network.network_id,
      robots = network.all_logistic_robots + network.all_construction_robots,
    }
  end
  table.sort(order, function(x, y)
    if x.robots ~= y.robots then return x.robots > y.robots end
    return x.id < y.id
  end)

  local picked = bounded.cut(order, bounded.limit(a.limit, DEFAULT_NETWORKS, MAX_NETWORKS))
  local rows = {}
  for i, entry in ipairs(picked) do rows[i] = network_row(entry.network, a.contents) end

  return {
    found = true,
    force = force.name,
    surface = surface.name,
    total = #order,
    shown = #rows,
    logistic_robots_total = logistic_total,
    networks = rows,
  }
end

M.functions = { logistics_summary = logistics_summary }

return M
