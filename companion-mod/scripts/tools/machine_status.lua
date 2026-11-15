-- AI Agent Bridge - scripts/tools/machine_status.lua
-- Author: bits-orio
-- License: MIT
--
-- machine_status: "why is X slow", "which miners are empty", "what is idle".
-- Walks one force's entities of one name or type on one surface, groups them
-- by their engine status and gives a count and a few [gps=] examples per
-- status. Read-only, bounded by SCAN_CAP, and says so when it stopped early.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaEntity::status (defines.entity_status, optional), ::position, ::valid
--   defines.entity_status (name -> number; reversed once per call here)
--   LuaSurface::find_entities_filtered / ::count_entities_filtered with
--     EntitySearchFilters name, type, force, limit, position, radius.

local force_lookup   = require("scripts.tools.force_lookup")
local surface_lookup = require("scripts.tools.surface_lookup")
local name_lookup    = require("scripts.tools.name_lookup")
local entity_view    = require("scripts.tools.entity_view")
local anchor         = require("scripts.tools.anchor")
local bounded        = require("scripts.tools.bounded")

local SCAN_CAP       = 4000
local DEFAULT_SHOWN  = 3
local MAX_SHOWN      = 5
local DEFAULT_RADIUS = 256
local MAX_RADIUS     = 1024

local M = {}

M.manifest = {
  machine_status = {
    desc = "Why machines are slow or idle: one force's entities of a name or type on a surface, counted by status with example gps tags.",
    tier = "2",
    params = {
      surface = "string! surface name or index",
      name    = "string prototype name; name or type is required",
      type    = "string entity type, e.g. mining-drill, lab",
      near    = "string player name: walk around them",
      radius  = "integer tiles around near, default " .. DEFAULT_RADIUS .. ", max " .. MAX_RADIUS,
      limit   = "integer examples per status, default " .. DEFAULT_SHOWN .. ", max " .. MAX_SHOWN,
      offset  = bounded.OFFSET_PARAM,
    },
  },
}

--- defines.entity_status is name -> number; a status read off an entity is the
--- number. Built per call, once, so a build that adds a status needs no edit.
local function status_names()
  local out = {}
  for key, value in pairs(defines.entity_status) do out[value] = key end
  return out
end

--- The engine filter for this call, or nil plus a found=false reply.
local function filter_of(a, force, surface, base)
  local f = { force = force.name, limit = SCAN_CAP }
  if type(a.name) == "string" and a.name ~= "" then
    local used, extra = name_lookup.resolve(a.name, "entity")
    if not used then
      base.name, base.reason, base.suggestions = a.name, "no entity prototype by that name: use the internal name, for example assembling-machine-2", extra
      return nil, base
    end
    f.name = used
    f.resolved_from = extra
  end
  if type(a.type) == "string" and a.type ~= "" then f.type = a.type end
  if not (f.name or f.type) then error("give a name or a type", 0) end
  if a.near ~= nil then
    local position, reason = anchor.position(a.near, surface)
    if not position then
      base.reason = reason
      return nil, base
    end
    f.position = position
    f.radius = anchor.radius(a.radius, DEFAULT_RADIUS, MAX_RADIUS)
  end
  return f
end

--- Groups entities by status name: { status = {count, examples} }.
local function group(entities, surface, limit)
  local names, by = status_names(), {}
  for _, entity in ipairs(entities) do
    if entity.valid then
      local key = entity.status ~= nil and names[entity.status] or "unknown"
      local slot = by[key] or { status = key, count = 0, examples = {} }
      by[key] = slot
      slot.count = slot.count + 1
      if #slot.examples < limit then
        local _, _, tag = entity_view.gps(entity.position, surface.name)
        slot.examples[#slot.examples + 1] = tag
      end
    end
  end
  local rows = {}
  for _, slot in pairs(by) do rows[#rows + 1] = slot end
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.status < y.status
  end)
  return rows
end

local function machine_status(a)
  local force = force_lookup.require_force(a.force)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then
    miss.force = force.name
    return miss
  end
  local f, refused = filter_of(a, force, surface, { found = false, force = force.name, surface = surface.name })
  if not f then return refused end
  local entities = surface.find_entities_filtered(f)
  local truncated = #entities >= SCAN_CAP
  local rows = group(entities, surface, bounded.limit(a.limit, DEFAULT_SHOWN, MAX_SHOWN))
  -- The tail past the cap is unread, so say how many matched in all: a count
  -- is cheaper than the walk, which is why it is only asked for here.
  local matching
  if truncated then
    local count_filter = {}
    for k, v in pairs(f) do count_filter[k] = v end
    count_filter.limit = nil
    matching = surface.count_entities_filtered(count_filter)
  end
  return bounded.page(rows, a.offset, function(page)
    return {
      found = true, force = force.name, surface = surface.name,
      name = f.name, type = f.type, resolved_from = f.resolved_from or nil,
      scanned = #entities, truncated = truncated, matching = matching,
      statuses = page,
    }
  end)
end

M.functions = { machine_status = machine_status }

return M
