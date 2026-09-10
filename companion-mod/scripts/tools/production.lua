-- item_rate and top_items: both read LuaFlowStatistics for one force and one
-- surface. get_flow_count without a sample_index returns the average across
-- the whole precision window, already normalised to per-minute for item
-- statistics (see scripts/tools/flow.lua for the quoted documentation).
-- "input" is the production side (items flowing onto the network), "output"
-- is consumption.

local force_lookup = require("scripts.tools.force_lookup")
local flow         = require("scripts.tools.flow")

local MAX_TOP_N = 50

local M = {}

M.manifest = {
  item_rate = {
    desc = "Production and consumption rate of one item for one force on one surface, in items per minute.",
    params = {
      surface = "string! surface name, e.g. nauvis",
      item    = "string! item prototype name",
      window  = "string! one of " .. flow.window_names,
    },
  },
  top_items = {
    desc = "The N most-produced items for one force on one surface over a window, ranked by production rate.",
    params = {
      surface = "string! surface name, e.g. nauvis",
      window  = "string! one of " .. flow.window_names,
      n       = "integer! how many items to return, capped at " .. MAX_TOP_N,
    },
  },
}

local function item_rate(a)
  local force = force_lookup.require_force(a.force)
  local surface = flow.require_surface(a.surface)
  local window = flow.require_window(a.window)
  if type(a.item) ~= "string" or a.item == "" then error("item is required") end

  local stats = force.get_item_production_statistics(surface)
  local produced = stats.get_flow_count{ name = a.item, category = "input",  precision_index = window.precision }
  local consumed = stats.get_flow_count{ name = a.item, category = "output", precision_index = window.precision }
  return {
    force = force.name, surface = surface, item = a.item, window = a.window,
    produced_per_min = produced, consumed_per_min = consumed, net_per_min = produced - consumed,
  }
end

local function top_items(a)
  local force = force_lookup.require_force(a.force)
  local surface = flow.require_surface(a.surface)
  local window = flow.require_window(a.window)
  local n = math.floor(tonumber(a.n) or 10)
  if n < 1 then n = 1 end
  if n > MAX_TOP_N then n = MAX_TOP_N end

  local stats = force.get_item_production_statistics(surface)
  local seen = {}
  for name in pairs(stats.input_counts or {}) do seen[name] = true end
  for name in pairs(stats.output_counts or {}) do seen[name] = true end

  local rows = {}
  for name in pairs(seen) do
    local rate = stats.get_flow_count{ name = name, category = "input", precision_index = window.precision }
    if rate > 0 then
      rows[#rows + 1] = { item = name, produced_per_min = rate }
    end
  end
  table.sort(rows, function(x, y) return x.produced_per_min > y.produced_per_min end)

  local top = {}
  for i = 1, math.min(n, #rows) do top[i] = rows[i] end
  return { force = force.name, surface = surface, window = a.window, items = top }
end

M.functions = { item_rate = item_rate, top_items = top_items }

return M
