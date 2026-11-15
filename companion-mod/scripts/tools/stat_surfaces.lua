-- AI Agent Bridge - scripts/tools/stat_surfaces.lua
-- Author: bits-orio
-- License: MIT
--
-- Which surfaces a force's flow statistics hold an item or a fluid on at
-- all: the cheap test that keeps a rate read off every surface where the
-- answer is already known to be zero. A force's statistics are per surface,
-- and a force makes most things on one or two of them, so reading a rate on
-- all forty surfaces of a big save spent almost every read on zeroes.
--
-- The test is the lifetime counters, get_input_count and get_output_count:
-- one O(1) lookup each, per quality for an item (a bare name is the normal
-- quality alone, scripts/tools/flow.lua). An item never made or used on a
-- surface has a rate of zero there in every window, so leaving that surface
-- out changes no figure.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaForce::get_item_production_statistics(surface),
--     ::get_fluid_production_statistics(surface) -> LuaFlowStatistics.
--   LuaFlowStatistics::get_input_count(id), ::get_output_count(id), id a
--     FlowStatisticsID: ItemWithQualityID for items, FluidID for fluids.

local M = {}

--- How much of `item`, at every quality, one force ever made and used on
--- `surface`, as one number: what ranks the busiest surfaces.
function M.item_lifetime(force, surface, item, qualities)
  local stats = force.get_item_production_statistics(surface)
  local total = 0
  for _, quality in ipairs(qualities) do
    local id = { name = item, quality = quality }
    total = total + stats.get_input_count(id) + stats.get_output_count(id)
  end
  return total
end

--- Whether one force ever made or used `item`, at any quality, on `surface`.
function M.item_seen(force, surface, item, qualities)
  return M.item_lifetime(force, surface, item, qualities) > 0
end

--- Everything one force ever made on `surface`, summed over every item: an
--- engine-built dictionary per call, so read once per surface at most.
function M.made_lifetime(force, surface)
  local total = 0
  for _, count in pairs(force.get_item_production_statistics(surface).input_counts or {}) do total = total + count end
  return total
end

--- Up to `max` of `surfaces` with weight(surface) > 0, heaviest first, and
--- how many had any weight at all, so a reply can say when it was cut.
function M.busiest(surfaces, weight, max)
  local ranked = {}
  for _, surface in ipairs(surfaces) do
    local w = weight(surface)
    if w > 0 then ranked[#ranked + 1] = { surface = surface, weight = w } end
  end
  table.sort(ranked, function(x, y)
    if x.weight ~= y.weight then return x.weight > y.weight end
    return x.surface.name < y.surface.name
  end)
  local out = {}
  for i = 1, math.min(max, #ranked) do out[i] = ranked[i].surface end
  return out, #ranked
end

--- Whether one force ever made or used `fluid` on `surface`.
function M.fluid_seen(force, surface, fluid)
  local stats = force.get_fluid_production_statistics(surface)
  return stats.get_input_count(fluid) > 0 or stats.get_output_count(fluid) > 0
end

--- The surfaces of `surfaces` where seen(force, surface) holds, in order.
function M.where(force, surfaces, seen)
  local out = {}
  for _, surface in ipairs(surfaces) do
    if seen(force, surface) then out[#out + 1] = surface end
  end
  return out
end

return M
