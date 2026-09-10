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
    desc = "The logistic networks one force has on one surface, busiest first: robot totals, how many are idle, how many cells the network covers, and its eight largest item stacks by count, each summed over every quality. A stack held at more than one quality carries a qualities breakdown; one held at a single quality other than normal says which. Answers \"do we have enough bots\" and \"what is in the chests\". An unknown surface comes back as found = false.",
    params = {
      surface = "string! surface name or index from list_surfaces, for example nauvis",
      limit   = "integer how many networks to return, default " .. DEFAULT_NETWORKS .. ", at most " .. MAX_NETWORKS,
    },
  },
}

local function network_row(network)
  local contents, distinct = contents_of.largest(network, CONTENTS_PER_NETWORK)
  return {
    id = network.network_id,
    cells = #network.cells,
    logistic_robots = network.all_logistic_robots,
    logistic_robots_available = network.available_logistic_robots,
    construction_robots = network.all_construction_robots,
    construction_robots_available = network.available_construction_robots,
    distinct_items = distinct,
    contents_shown = #contents,
    contents = contents,
  }
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
  local order = {}
  for _, network in pairs(force.logistic_networks[surface.name] or {}) do
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
  for i, entry in ipairs(picked) do rows[i] = network_row(entry.network) end

  return {
    found = true,
    force = force.name,
    surface = surface.name,
    total = #order,
    shown = #rows,
    networks = rows,
  }
end

M.functions = { logistics_summary = logistics_summary }

return M
