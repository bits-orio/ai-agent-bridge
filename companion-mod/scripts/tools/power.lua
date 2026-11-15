-- AI Agent Bridge - scripts/tools/power.lua
-- Author: bits-orio
-- License: MIT
--
-- power: "do we have enough power". For one force on one surface, finds its
-- electric networks through its poles and reports the largest ones:
-- production and consumption now, how much of the production is used,
-- accumulator charge and capacity, and the prototypes that produce and
-- consume most.
--
-- There is no engine read for "satisfaction" of a network. The statistics
-- hold energy actually delivered, so production and consumption read equal
-- whether or not the network is short (measured on a 254 MW network of a
-- real save, 2026-11-14): a ratio of the two says nothing, and none is sent.
-- A shortage shows two other ways: accumulators draining, and machines in
-- the low_power status, which machine_status counts.
--
-- Units. LuaFlowStatistics::get_flow_count: "All return values are normalized
-- to be per-tick for electric networks", and the API's energy unit is the
-- joule, so joules per tick times 60 ticks per second is watts. Accumulator
-- energy and electric_buffer_size are joules, reported as megajoules.
--
-- Which side is production, measured on 2.1.23 (a fresh map: one
-- electric-energy-interface feeding one pumpjack through a substation) and on
-- 2.1.24 (a live multi-force save): an electric network's output_counts holds
-- the producers, generators, solar panels and accumulators discharging, and
-- input_counts the consumers, machines, inserters, labs and accumulators
-- charging. So production is the "output" category and consumption "input",
-- the other way round from item statistics.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaEntity::electric_network_id (uint32?), ::electric_network_statistics
--   (electric poles only, LuaFlowStatistics), ::energy, ::electric_buffer_size
--   LuaFlowStatistics::input_counts, ::output_counts, ::get_flow_count
--   defines.flow_precision_index.five_seconds
--   LuaSurface::find_entities_filtered with type, force, limit.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local bounded        = require("scripts.tools.bounded")
local flow           = require("scripts.tools.flow")

local POLE_CAP       = 2000
local ACCUMULATOR_CAP = 2000
local PROTOTYPE_CAP  = 80
local TOP_SHOWN      = 3
local DEFAULT_SHOWN  = 3
local MAX_SHOWN      = 5
local TICKS_PER_SECOND = 60
local JOULES_PER_MJ  = 1e6

local M = {}

M.manifest = {
  power = {
    desc = "Electric networks on a surface, largest first: power delivered now (production_w, consumption_w), accumulator charge, top producers and consumers. Delivered power cannot show a shortage: for that, see empty accumulators here and low_power in machine_status.",
    tier = "2",
    params = {
      surface = "string! surface name or index",
      limit   = "integer networks shown, default " .. DEFAULT_SHOWN .. ", max " .. MAX_SHOWN,
    },
  },
}

--- Distinct networks seen through poles, one pole each to read statistics
--- from, ranked by how many poles were seen.
local function networks_of(poles)
  local by, list = {}, {}
  for _, pole in ipairs(poles) do
    local id = pole.valid and pole.electric_network_id
    if id then
      local net = by[id]
      if not net then
        net = { id = id, poles = 0, pole = pole }
        by[id] = net
        list[#list + 1] = net
      end
      net.poles = net.poles + 1
    end
  end
  table.sort(list, function(x, y)
    if x.poles ~= y.poles then return x.poles > y.poles end
    return x.id < y.id
  end)
  return list
end

--- Watts per prototype for one side ("input" or "output") of a network's
--- statistics, summed over qualities, largest first, and the side's total.
local function side_rates(stats, side, qualities)
  local counts = side == "input" and stats.input_counts or stats.output_counts
  local rows, total, seen = {}, 0, 0
  for name in pairs(counts or {}) do
    seen = seen + 1
    if seen > PROTOTYPE_CAP then break end
    local per_tick = 0
    for _, quality in ipairs(qualities) do
      per_tick = per_tick + stats.get_flow_count{
        name = { name = name, quality = quality }, category = side,
        precision_index = defines.flow_precision_index.five_seconds,
      }
    end
    local watts = per_tick * TICKS_PER_SECOND
    total = total + watts
    if watts > 0 then rows[#rows + 1] = { name = name, w = watts } end
  end
  table.sort(rows, function(x, y)
    if x.w ~= y.w then return x.w > y.w end
    return x.name < y.name
  end)
  return rows, total
end

local function top(rows)
  local out = {}
  for i, row in ipairs(bounded.cut(rows, TOP_SHOWN)) do out[i] = { name = row.name, w = bounded.round(row.w, 0) } end
  return out
end

--- Accumulator charge and capacity in joules per network id, and whether the
--- cap was hit.
local function accumulators(surface, force)
  local found = surface.find_entities_filtered{ type = "accumulator", force = force.name, limit = ACCUMULATOR_CAP }
  local by = {}
  for _, acc in ipairs(found) do
    local id = acc.valid and acc.electric_network_id
    if id then
      local slot = by[id] or { charge = 0, capacity = 0 }
      by[id] = slot
      slot.charge = slot.charge + acc.energy
      slot.capacity = slot.capacity + (acc.electric_buffer_size or 0)
    end
  end
  return by, #found >= ACCUMULATOR_CAP
end

local function network_row(net, acc, qualities)
  local stats = net.pole.electric_network_statistics
  local row = { network = net.id, poles = net.poles }
  if stats then
    local producers, production = side_rates(stats, "output", qualities)
    local consumers, consumption = side_rates(stats, "input", qualities)
    row.production_w, row.consumption_w = bounded.round(production, 0), bounded.round(consumption, 0)
    row.producers, row.consumers = top(producers), top(consumers)
  end
  if acc then
    row.accumulator_charge_mj = bounded.round(acc.charge / JOULES_PER_MJ, 2)
    row.accumulator_capacity_mj = bounded.round(acc.capacity / JOULES_PER_MJ, 2)
  end
  return row
end

local function power(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local poles = surface.find_entities_filtered{ type = "electric-pole", force = force.name, limit = POLE_CAP }
  local nets = networks_of(poles)
  if #nets == 0 then
    return { found = false, force = force.name, surface = surface.name,
             reason = "this force has no electric poles on that surface" }
  end
  local acc_by_id, acc_truncated = accumulators(surface, force)
  local qualities = flow.quality_names()
  local rows = {}
  for i, net in ipairs(bounded.cut(nets, bounded.limit(a.limit, DEFAULT_SHOWN, MAX_SHOWN))) do
    rows[i] = network_row(net, acc_by_id[net.id], qualities)
  end
  local function build(shown)
    return {
      found = true, force = force.name, surface = surface.name,
      window = "five_seconds", unit = "watts; accumulators in megajoules",
      networks_total = #nets, truncated = #poles >= POLE_CAP,
      accumulators_truncated = acc_truncated or nil,
      networks = shown,
    }
  end
  return build(bounded.fit_reply(rows, build))
end

M.functions = { power = power }

return M
