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
--     each {name, quality, count}.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local bounded        = require("scripts.tools.bounded")

local DEFAULT_NETWORKS = 5
local MAX_NETWORKS = 10
local CONTENTS_PER_NETWORK = 10

local M = {}

M.manifest = {
  logistics_summary = {
    desc = "The logistic networks one force has on one surface: robot totals, how many robots are idle and available, how many logistic cells the network covers, and its ten largest item stacks by count. Use it for \"do we have enough bots\", \"what is sitting in the chests\" and \"how many networks are there\". Busiest network first. An unknown surface comes back as found = false rather than an error.",
    params = {
      surface = "string! surface name from list_surfaces, for example nauvis",
      limit   = "integer how many networks to return, default " .. DEFAULT_NETWORKS .. ", at most " .. MAX_NETWORKS,
    },
  },
}

-- The ten biggest stacks, largest first. Quality rides along only when it is
-- something other than normal, because on most saves it is normal everywhere
-- and a repeated "normal" on every row is reply bytes spent saying nothing.
local function largest_contents(network)
  local rows = network.get_contents()
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    if x.name ~= y.name then return x.name < y.name end
    return tostring(x.quality) < tostring(y.quality)
  end)
  local top = {}
  for i = 1, math.min(CONTENTS_PER_NETWORK, #rows) do
    local row = rows[i]
    top[i] = {
      name = row.name,
      count = row.count,
      quality = (row.quality ~= "normal") and row.quality or nil,
    }
  end
  return top, #rows
end

local function network_row(network)
  local contents, distinct = largest_contents(network)
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
