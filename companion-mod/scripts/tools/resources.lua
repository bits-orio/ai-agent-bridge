-- AI Agent Bridge - scripts/tools/resources.lua
-- Author: bits-orio
-- License: MIT
--
-- resources: "how much ore or oil is left near me". Sums the resource
-- entities in a circle around a player, per kind, and names the richest tile
-- of each. Always anchored on a player: a whole-surface resource sum is
-- unbounded, and the forces' own area is not something the engine tells us.
--
-- An infinite resource (oil, in vanilla) has no meaningful sum: each entity's
-- amount is a yield relative to normal_resource_amount, 100 percent at
-- normal. It reports the average yield percent instead. Measured on a fresh
-- 2.1.23 map: crude oil's normal_resource_amount is 300000 and its
-- minimum_resource_amount 60000 (the 20 percent floor an amount never goes
-- below, so it needs no clamp here); a well of amount 752669, 250.9 percent,
-- pumped 426.5 crude oil in its first 17.6 seconds, about the 10 * 2.509 a
-- second a pumpjack at mining speed 1 gives at that yield.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaSurface::find_entities_filtered with type = "resource", name,
--     position + radius, limit.
--   LuaEntity::amount (uint32, ResourceEntity), ::name, ::position, ::valid.
--   LuaEntityPrototype::infinite_resource, ::normal_resource_amount, read
--     from prototypes.entity[name].

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local name_lookup    = require("scripts.tools.name_lookup")
local entity_view    = require("scripts.tools.entity_view")
local anchor         = require("scripts.tools.anchor")
local bounded        = require("scripts.tools.bounded")

local SCAN_CAP       = 20000
local DEFAULT_RADIUS = 128
local MAX_RADIUS     = 512
local PERCENT        = 100

local M = {}

M.manifest = {
  resources = {
    desc = "Ore and oil left near a player, per resource: tiles, total, richest tile as gps; infinite ones give avg_yield_percent.",
    tier = "2",
    params = {
      surface  = "string! surface name or index",
      near     = "string! player name to measure around",
      resource = "string resource name, e.g. iron-ore; omit for all",
      radius   = "integer tiles, default " .. DEFAULT_RADIUS .. ", max " .. MAX_RADIUS,
      offset   = bounded.OFFSET_PARAM,
    },
  },
}

--- Per-kind tallies of the resource entities found.
local function tally(entities)
  local by, order = {}, {}
  for _, e in ipairs(entities) do
    if e.valid then
      local t = by[e.name]
      if not t then
        t = { name = e.name, entities = 0, total = 0, richest = e }
        by[e.name] = t
        order[#order + 1] = t
      end
      t.entities, t.total = t.entities + 1, t.total + e.amount
      if e.amount > t.richest.amount then t.richest = e end
    end
  end
  return order
end

local function row_of(t, surface)
  local proto = prototypes.entity[t.name]
  local _, _, tag = entity_view.gps(t.richest.position, surface.name)
  local row = { resource = t.name, entities = t.entities, richest = tag, richest_amount = t.richest.amount }
  if proto and proto.infinite_resource then
    row.infinite = true
    -- 100 percent is normal_resource_amount; without it there is no scale.
    local normal = proto.normal_resource_amount
    if normal and normal > 0 then
      row.avg_yield_percent = bounded.round(t.total / t.entities / normal * PERCENT, 1)
    end
  else
    row.total = t.total
  end
  return row
end

local function resources(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local base = { found = false, force = force.name, surface = surface.name }
  if a.near == nil or a.near == "" then
    base.reason = "near is required: pass a player name to measure the resources around"
    return base
  end
  local position, reason = anchor.position(a.near, surface)
  if not position then
    base.reason = reason
    return base
  end
  local filter = { type = "resource", position = position, limit = SCAN_CAP,
                   radius = anchor.radius(a.radius, DEFAULT_RADIUS, MAX_RADIUS) }
  if type(a.resource) == "string" and a.resource ~= "" then
    local used, extra = name_lookup.resolve(a.resource, "entity")
    if not used then
      base.resource, base.suggestions = a.resource, extra
      base.reason = "no resource prototype by that name: use the internal name, for example iron-ore"
      return base
    end
    filter.name = used
  end
  local entities = surface.find_entities_filtered(filter)
  local rows = {}
  for _, t in ipairs(tally(entities)) do rows[#rows + 1] = row_of(t, surface) end
  table.sort(rows, function(x, y) return x.resource < y.resource end)
  return bounded.page(rows, a.offset, function(page)
    return {
      found = true, surface = surface.name, near = a.near, radius = filter.radius,
      scanned = #entities, truncated = #entities >= SCAN_CAP,
      resources = page,
    }
  end)
end

M.functions = { resources = resources }

return M
