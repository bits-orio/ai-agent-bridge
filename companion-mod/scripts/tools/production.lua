-- item_rate and top_items: both read LuaFlowStatistics for one force and one
-- surface. get_flow_count without a sample_index returns the average across
-- the whole precision window, already normalised to per-minute for item
-- statistics (see scripts/tools/flow.lua for the quoted documentation).
-- "input" is the production side (items flowing onto the network), "output"
-- is consumption. Both sum every quality, so a force that makes one plate at
-- five qualities reads its whole output rather than the normal-quality part.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local flow           = require("scripts.tools.flow")
local bounded        = require("scripts.tools.bounded")

local MAX_TOP_N = 50

local M = {}

M.manifest = {
  item_rate = {
    desc = "Production and consumption rate of one item for one force on one surface, items per minute over all qualities. Unknown surface: found=false.",
    params = {
      surface = "string! surface name or index, e.g. nauvis",
      item    = "string! item prototype name",
      window  = "string! one of " .. flow.window_names,
    },
  },
  top_items = {
    desc = "The N most-produced items for one force on one surface over a window, by rate over all qualities. Unknown surface: found=false.",
    params = {
      surface = "string! surface name or index, e.g. nauvis",
      window  = "string! one of " .. flow.window_names,
      n       = "integer! items to return, max " .. MAX_TOP_N,
    },
  },
}

local function item_rate(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local window = flow.require_window(a.window)
  if type(a.item) ~= "string" or a.item == "" then error("item is required", 0) end

  local stats = force.get_item_production_statistics(surface)
  local spec = { item = a.item, qualities = flow.quality_names(), window = window }
  local produced = flow.item_flow(stats, spec, "input")
  local consumed = flow.item_flow(stats, spec, "output")
  return {
    found = true,
    force = force.name, surface = surface.name, item = a.item, window = a.window,
    all_qualities = true,
    produced_per_min = bounded.round(produced, 2),
    consumed_per_min = bounded.round(consumed, 2),
    net_per_min = bounded.round(produced - consumed, 2),
  }
end

local function top_items(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local window = flow.require_window(a.window)
  local n = bounded.limit(a.n, 10, MAX_TOP_N)

  local stats = force.get_item_production_statistics(surface)
  local seen = {}
  for name in pairs(stats.input_counts or {}) do seen[name] = true end
  for name in pairs(stats.output_counts or {}) do seen[name] = true end

  local spec = { qualities = flow.quality_names(), window = window }
  local rows = {}
  for name in pairs(seen) do
    spec.item = name
    local rate = flow.item_flow(stats, spec, "input")
    if rate > 0 then
      rows[#rows + 1] = { item = name, produced_per_min = bounded.round(rate, 2) }
    end
  end
  table.sort(rows, function(x, y)
    if x.produced_per_min ~= y.produced_per_min then return x.produced_per_min > y.produced_per_min end
    return x.item < y.item
  end)

  local top = bounded.cut(rows, n)
  return {
    found = true,
    force = force.name, surface = surface.name, window = a.window,
    all_qualities = true, items = top,
  }
end

M.functions = { item_rate = item_rate, top_items = top_items }

return M
