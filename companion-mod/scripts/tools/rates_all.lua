-- AI Agent Bridge - scripts/tools/rates_all.lua
-- Author: bits-orio
-- License: MIT
--
-- item_rate and top_items over more than one surface: what production.lua
-- does when no surface is named. Only surfaces the force's statistics hold
-- something on are read (scripts/tools/stat_surfaces.lua), and at most
-- MAX_SURFACES of those, the busiest by lifetime count, so a force with a
-- hundred platforms costs a bounded read and the reply says when it was cut.

local flow          = require("scripts.tools.flow")
local bounded       = require("scripts.tools.bounded")
local stat_surfaces = require("scripts.tools.stat_surfaces")

local MAX_SURFACES = 12
local MAX_BREAKDOWN = 5

local M = {}

local function every_surface()
  return flow.surfaces_for({})
end

--- One item's rate for one force over every surface it has the item on.
--- The reply item_rate sends with no surface: the total, the busiest
--- surfaces' own rates, surfaces_covered, and surfaces_with_item when the
--- cut left some out.
function M.item_rate(force, item, window_name, window)
  local qualities = flow.quality_names()
  local covered, with_item = stat_surfaces.busiest(every_surface(), function(surface)
    return stat_surfaces.item_lifetime(force, surface, item, qualities)
  end, MAX_SURFACES)
  local spec = { item = item, qualities = qualities, window = window }
  local rows, produced, consumed = {}, 0, 0
  for _, surface in ipairs(covered) do
    local stats = force.get_item_production_statistics(surface)
    local here_in, here_out = flow.item_flow(stats, spec, "input"), flow.item_flow(stats, spec, "output")
    produced, consumed = produced + here_in, consumed + here_out
    rows[#rows + 1] = { surface = surface.name, produced = here_in, consumed = here_out }
  end
  table.sort(rows, function(x, y)
    if x.produced ~= y.produced then return x.produced > y.produced end
    return x.surface < y.surface
  end)
  local breakdown = {}
  for i, row in ipairs(bounded.cut(rows, MAX_BREAKDOWN)) do
    breakdown[i] = { surface = row.surface, produced_per_min = bounded.round(row.produced, 2),
                     consumed_per_min = bounded.round(row.consumed, 2) }
  end
  return {
    found = true, force = force.name, surface = "all", item = item, window = window_name, all_qualities = true,
    produced_per_min = bounded.round(produced, 2), consumed_per_min = bounded.round(consumed, 2),
    net_per_min = bounded.round(produced - consumed, 2),
    surfaces_covered = #covered, surfaces_with_item = with_item > #covered and with_item or nil,
    surfaces = breakdown,
  }
end

--- The surfaces top_items reads with no surface named: the busiest
--- MAX_SURFACES the force has made anything on, and how many had anything.
function M.surfaces_with_output(force)
  return stat_surfaces.busiest(every_surface(), function(surface)
    return stat_surfaces.made_lifetime(force, surface)
  end, MAX_SURFACES)
end

--- Production rate per item name, summed over `surfaces`, for every item
--- the force's statistics hold on any of them.
function M.item_rates(force, surfaces, window)
  local qualities = flow.quality_names()
  local rates = {}
  for _, surface in ipairs(surfaces) do
    local stats = force.get_item_production_statistics(surface)
    local seen = {}
    for name in pairs(stats.input_counts or {}) do seen[name] = true end
    for name in pairs(stats.output_counts or {}) do seen[name] = true end
    for name in pairs(seen) do
      local rate = flow.item_flow(stats, { item = name, qualities = qualities, window = window }, "input")
      rates[name] = (rates[name] or 0) + rate
    end
  end
  return rates
end

M.MAX_SURFACES = MAX_SURFACES

return M
