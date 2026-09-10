-- item_rate and top_items: both read LuaFlowStatistics for one force and
-- surface. get_flow_count{name, category="input"|"output", precision_index}
-- returns a value already normalised to per-minute for item statistics
-- (verified against the local Factorio 2.0.77 runtime-api.json, not memory
-- -- see LuaFlowStatistics::get_flow_count in the official docs). "input"
-- is the production side (items flowing onto the network), "output" is
-- consumption -- same convention remlab-bridge and RedMew's production HUD
-- use.

local force_lookup = require("scripts.tools.force_lookup")

local WINDOWS = {
  one_minute  = defines.flow_precision_index.one_minute,
  ten_minutes = defines.flow_precision_index.ten_minutes,
  one_hour    = defines.flow_precision_index.one_hour,
  ten_hours   = defines.flow_precision_index.ten_hours,
}

local MAX_TOP_N = 50

local function require_window(name)
  local precision = WINDOWS[name]
  if not precision then
    error("unknown window: " .. tostring(name) .. " (want one_minute, ten_minutes, one_hour or ten_hours)")
  end
  return precision
end

local function require_surface(name)
  if type(name) ~= "string" or not game.surfaces[name] then
    error("unknown surface: " .. tostring(name))
  end
  return name
end

local M = {}

M.manifest = {
  item_rate = {
    desc = "Production and consumption rate of one item for one force on one surface, in items per minute.",
    params = {
      surface = "string! surface name, e.g. nauvis",
      item    = "string! item prototype name",
      window  = "string! one_minute, ten_minutes, one_hour or ten_hours",
    },
  },
  top_items = {
    desc = "The N most-produced items for one force on one surface over a window, ranked by production rate.",
    params = {
      surface = "string! surface name, e.g. nauvis",
      window  = "string! one_minute, ten_minutes, one_hour or ten_hours",
      n       = "integer! how many items to return, capped at " .. MAX_TOP_N,
    },
  },
}

function M.item_rate(a)
  local force = force_lookup.require_force(a.force)
  local surface = require_surface(a.surface)
  local precision = require_window(a.window)
  if type(a.item) ~= "string" or a.item == "" then error("item is required") end

  local stats = force.get_item_production_statistics(surface)
  local produced = stats.get_flow_count{ name = a.item, category = "input",  precision_index = precision }
  local consumed = stats.get_flow_count{ name = a.item, category = "output", precision_index = precision }
  return {
    force = force.name, surface = surface, item = a.item, window = a.window,
    produced_per_min = produced, consumed_per_min = consumed, net_per_min = produced - consumed,
  }
end

function M.top_items(a)
  local force = force_lookup.require_force(a.force)
  local surface = require_surface(a.surface)
  local precision = require_window(a.window)
  local n = math.floor(tonumber(a.n) or 10)
  if n < 1 then n = 1 end
  if n > MAX_TOP_N then n = MAX_TOP_N end

  local stats = force.get_item_production_statistics(surface)
  local seen = {}
  for name in pairs(stats.input_counts or {}) do seen[name] = true end
  for name in pairs(stats.output_counts or {}) do seen[name] = true end

  local rows = {}
  for name in pairs(seen) do
    local rate = stats.get_flow_count{ name = name, category = "input", precision_index = precision }
    if rate > 0 then
      rows[#rows + 1] = { item = name, produced_per_min = rate }
    end
  end
  table.sort(rows, function(x, y) return x.produced_per_min > y.produced_per_min end)

  local top = {}
  for i = 1, math.min(n, #rows) do top[i] = rows[i] end
  return { force = force.name, surface = surface, window = a.window, items = top }
end

return M
