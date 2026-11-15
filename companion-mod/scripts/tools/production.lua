-- AI Agent Bridge - scripts/tools/production.lua
-- Author: bits-orio
-- License: MIT
--
-- item_rate and top_items: both read LuaFlowStatistics for one force.
-- get_flow_count without a sample_index returns the average across the whole
-- precision window, already normalised to per-minute for item statistics
-- (see scripts/tools/flow.lua for the quoted documentation). "input" is the
-- production side (items flowing onto the network), "output" is consumption.
-- Both sum every quality, so a force that makes one plate at five qualities
-- reads its whole output rather than the normal-quality part.
--
-- With a surface named, one surface is read. With none, every surface the
-- force's statistics hold anything on, the busiest MAX_SURFACES of them
-- (scripts/tools/rates_all.lua): the model no longer has to learn where a
-- force lives before asking how fast it makes something.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local flow           = require("scripts.tools.flow")
local bounded        = require("scripts.tools.bounded")
local name_lookup    = require("scripts.tools.name_lookup")
local rates_all      = require("scripts.tools.rates_all")
local window_age     = require("scripts.tools.window_age")

local MAX_TOP_N = 50
local WINDOW_PARAM = "string! averaging window, one of " .. flow.window_names .. "; one_minute for right now; partial_window means the map is younger than the window and the rate is understated, averaged over the whole window"
local SURFACE_PARAM = "string surface name or index; omit for every surface the force has made things on"

local M = {}

M.manifest = {
  item_rate = {
    desc = "How fast one force makes and uses one item: per minute over all qualities, averaged over the window, on one surface or, with surface left out, totalled over every surface it has the item on, with a per-surface breakdown. A rate, never a total: how much was made is production_since.",
    params = {
      surface = SURFACE_PARAM,
      item    = "string! item prototype name",
      window  = WINDOW_PARAM,
    },
  },
  top_items = {
    desc = "The N items one force makes fastest over a window, by rate over all qualities, on one surface or summed over every surface when surface is left out. Rates, not totals.",
    params = {
      surface = SURFACE_PARAM,
      window  = WINDOW_PARAM,
      n       = "integer! items to return, max " .. MAX_TOP_N,
    },
  },
}

--- The named surface, nil when none was named, or nil and the miss reply.
local function surface_of(a, force)
  if a.surface == nil or a.surface == "" or a.surface == "all" then return nil end
  local surface, miss = surface_lookup.find(a.surface)
  if surface then return surface end
  miss.force = force.name
  return nil, miss
end

--- The item to read, or nil and a found=false reply with suggestions.
local function item_of(a, force)
  if type(a.item) ~= "string" or a.item == "" then error("item is required", 0) end
  local item, extra = name_lookup.resolve(a.item, "item")
  if item then return item, extra end
  return nil, { found = false, force = force.name, item = a.item, suggestions = extra,
                reason = "no item prototype by that name: use the internal name, for example iron-plate" }
end

--- Nothing made in the window: the shortest of the next three longer ones
--- that saw any, so "are they making repair packs?" is not answered no for a
--- machine that made 48 in the hour and stalled a minute ago.
local function last_made(stats, item, window_name, qualities)
  for _, name in ipairs(flow.longer(window_name, 3)) do
    local earlier = flow.item_flow(stats, { item = item, qualities = qualities, window = flow.require_window(name) }, "input")
    if earlier > 0 then return { window = name, produced_per_min = bounded.round(earlier, 2) } end
  end
  return nil
end

local function one_surface_rate(force, surface, item, a)
  local window = flow.require_window(a.window)
  local stats = force.get_item_production_statistics(surface)
  local spec = { item = item, qualities = flow.quality_names(), window = window }
  local produced = flow.item_flow(stats, spec, "input")
  local consumed = flow.item_flow(stats, spec, "output")
  local reply = {
    found = true,
    force = force.name, surface = surface.name, item = item, window = a.window,
    all_qualities = true,
    produced_per_min = bounded.round(produced, 2),
    consumed_per_min = bounded.round(consumed, 2),
    net_per_min = bounded.round(produced - consumed, 2),
  }
  if produced == 0 then reply.last_made = last_made(stats, item, a.window, spec.qualities) end
  return window_age.stamp(reply, window)
end

local function item_rate(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_of(a, force)
  if miss then return miss end
  local window = flow.require_window(a.window)
  local item, extra = item_of(a, force)
  if not item then return extra end
  local reply
  if surface then
    reply = one_surface_rate(force, surface, item, a)
  else
    reply = window_age.stamp(rates_all.item_rate(force, item, a.window, window), window)
  end
  reply.resolved_from = extra
  return reply
end

--- Rows of {item, rate} sorted on the raw rate, rounded only on the rows
--- that go out: bounded.round hands back a number for a whole rate and a
--- short decimal STRING for a fraction, and Lua's `>` throws comparing the
--- two, so a sort on the rounded field failed on any busy server.
local function top_rows(rates, n)
  local rows = {}
  for item, rate in pairs(rates) do
    if rate > 0 then rows[#rows + 1] = { item = item, rate = rate } end
  end
  table.sort(rows, function(x, y)
    if x.rate ~= y.rate then return x.rate > y.rate end
    return x.item < y.item
  end)
  local top = {}
  for i, row in ipairs(bounded.cut(rows, n)) do
    top[i] = { item = row.item, produced_per_min = bounded.round(row.rate, 2) }
  end
  return top
end

local function top_items(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_of(a, force)
  if miss then return miss end
  local window = flow.require_window(a.window)
  local n = bounded.limit(a.n, 10, MAX_TOP_N)
  local reply = { found = true, force = force.name, window = a.window, all_qualities = true }
  local rates
  if surface then
    reply.surface = surface.name
    rates = rates_all.item_rates(force, { surface }, window)
  else
    local covered, with_stats = rates_all.surfaces_with_output(force)
    reply.surface, reply.surfaces_covered = "all", #covered
    if with_stats > #covered then reply.surfaces_with_stats = with_stats end
    rates = rates_all.item_rates(force, covered, window)
  end
  reply.items = top_rows(rates, n)
  return window_age.stamp(reply, window)
end

-- Shared with force_card, which wants the same ranking in a tighter shape.
M.top_rows = top_rows

M.functions = { item_rate = item_rate, top_items = top_items }

return M
