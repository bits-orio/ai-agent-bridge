-- AI Agent Bridge - scripts/tools/entity_count_all.lua
-- Author: bits-orio
-- License: MIT
--
-- entity_count with all=true: every force with players in one call, by the
-- engine's own per-force counter when no surface is named, by one pass per
-- force on a named surface, or per force and surface with per_surface.
-- `names` is the list of prototype names counted together (entity_count.lua
-- resolves name, names or type into it); a row's count is their sum, and
-- by_name splits it when there is more than one.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaForce::get_entity_count(name) -> uint32, documented O(1);
--   LuaSurface::count_entities_filtered(filter) -> uint32, filter.name an
--   EntityID or an array of them, filter.force a ForceSet.

local bounded         = require("scripts.tools.bounded")
local surface_lookup  = require("scripts.tools.surface_lookup")
local platform_lookup = require("scripts.tools.platform_lookup")

-- One count_entities_filtered is a C++ pass over a surface, not a Lua walk, but
-- a sweep multiplies them: every force against every surface.
--
-- The sweep does not add game time, it removes round trips. A force owns
-- entities on its own surface and almost nowhere else, so the passes that find
-- nothing are cheap and only a handful do real work, and those same passes were
-- always going to run. Question 64 on 2026-09-15 asked how many solar panels
-- each team had and cost fourteen entity_count calls in one round: fourteen
-- RCON trips for engine work that one trip can carry.
--
-- So MAX_PASSES guards the pathological shape rather than the ordinary one. The
-- measured server runs 23 surfaces against 14 populated forces, 322 passes; a
-- save that has accumulated a hundred space platforms (surfaces.lua says they
-- do) against twenty teams is 2000, and that one refuses and says which
-- argument makes it affordable. docs/design/phase4-spec.md, "Bounding a walk"
-- and "Refusal as a Lua contract".
local MAX_PASSES = 600

local M = {}

--- Which forces a sweep answers for: the ones a team mod has actually put
--- players on, the same rule current_research's own sweep follows. An empty
--- slot owns nothing anywhere, so a row of zeroes for it is noise in every
--- row of the answer.
local function sweep_forces()
  local out = {}
  for _, force in pairs(game.forces) do
    if #force.players > 0 then out[#out + 1] = force end
  end
  table.sort(out, function(x, y) return x.name < y.name end)
  return out
end

--- all=true is the every-team question in one call. The reply carries no
--- top-level force field: that absence is what tells a sweep from the
--- single-force shape, which carries one, and both carry rows. Sorted,
--- largest first, and paged, shared by every sweep path so the wire shape
--- cannot drift between them. surfaces_counted is nil on the engine-counter
--- path: that path asks no surface anything, so reporting a number there
--- would be inventing one.
local function finish_rows(rows, a, head)
  table.sort(rows, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    if x.force ~= y.force then return x.force < y.force end
    return (x.surface or "") < (y.surface or "")
  end)
  return bounded.page(rows, a.offset, function(page)
    local reply = { found = true, forces = page }
    for key, value in pairs(head) do reply[key] = value end
    return reply
  end, bounded.MAX_FORCES)
end

--- Every valid surface in the game, sorted by name.
local function every_surface()
  local all = {}
  for _, surface in pairs(game.surfaces) do
    if surface.valid then all[#all + 1] = surface end
  end
  table.sort(all, function(x, y) return x.name < y.name end)
  return all
end

--- The force-and-surface passes per_surface will really run: a force that
--- owns none of the entity anywhere is skipped after one O(1) read of the
--- engine's own per-force counter, and a platform surface is counted for
--- the force that owns it and no other, since a platform belongs to one
--- force. The refusal is decided on this list, after both prunings: deciding
--- on forces x surfaces refused 16 forces on 40 surfaces as 640 passes when
--- the walk below would have run a few dozen.
local function pass_plan(forces, surfaces, name)
  local plan = {}
  for _, force in ipairs(forces) do
    if force.get_entity_count(name) > 0 then
      for _, surface in ipairs(surfaces) do
        local platform = platform_lookup.live(surface)
        local owner = platform and platform.force and platform.force.name
        if not owner or owner == force.name then
          plan[#plan + 1] = { force = force, surface = surface, platform = platform }
        end
      end
    end
  end
  return plan
end

--- One per_surface row, or nil when the pass finds none. A platform is its
--- own surface with its hub at the origin, so the one ping that opens
--- remote view on the ship is right here, ready for the answer to carry.
local function surface_row(pass, name)
  local surface = pass.surface
  local count = surface.count_entities_filtered{ force = pass.force.name, name = name }
  if count == 0 then return nil end
  local row = { force = pass.force.name, surface = surface.name, count = count }
  platform_lookup.merge_into(row, surface)
  if pass.platform then row.gps = "[gps=0,0," .. surface.name .. "]" end
  return row
end

--- The per_surface answer: one row per force-and-surface that actually has
--- some of the entity, empty combinations left out rather than padding the
--- reply with zeroes. Platform rows carry the columns list_surfaces does, so
--- "which space ship has most thrusters, where is it" reads off one row.
local function entity_count_per_surface(a, forces, name)
  local surfaces = every_surface()
  local plan = pass_plan(forces, surfaces, name)
  if #plan > MAX_PASSES then
    return {
      found = false, name = name, surface = "all",
      force_count = #forces, surface_count = #surfaces,
      passes = #plan, max_passes = MAX_PASSES,
      reason = "counting every force that owns " .. name .. " on every surface it could be on would be " ..
               #plan .. " passes over the map, past the " .. MAX_PASSES .. " one call may spend: " ..
               "drop per_surface for the engine's own per-force totals, or name one surface",
    }
  end
  local rows = {}
  for _, pass in ipairs(plan) do rows[#rows + 1] = surface_row(pass, name) end
  return finish_rows(rows, a, { name = name, surface = "all", surfaces_counted = #surfaces })
end

--- A force's engine counter summed over `names`, and the nonzero ones by
--- name when there is more than one.
local function counted(force, names)
  local total, by_name = 0, nil
  for _, name in ipairs(names) do
    local n = force.get_entity_count(name)
    total = total + n
    if #names > 1 and n > 0 then
      by_name = by_name or {}
      by_name[name] = n
    end
  end
  return total, by_name
end

--- The passes a named surface costs: one per force per name it owns any of
--- anywhere, the O(1) counter deciding that first.
local function owned_passes(forces, names)
  local plan = {}
  for _, force in ipairs(forces) do
    for _, name in ipairs(names) do
      if force.get_entity_count(name) > 0 then plan[#plan + 1] = { force = force, name = name } end
    end
  end
  return plan
end

local function too_many(head, plan, forces, surfaces, why)
  head.found, head.force_count, head.surface_count = false, #forces, surfaces and #surfaces or nil
  head.passes, head.max_passes = #plan, MAX_PASSES
  head.reason = why .. " would be " .. #plan .. " passes over the map, past the " .. MAX_PASSES ..
                " one call may spend: leave surface out for the engine's own per-force totals"
  return head
end

--- all=true on one named surface: one pass per force per name it owns.
local function on_surface(a, names, head, forces)
  local surface, miss = surface_lookup.find(a.surface)
  if not surface then return miss end
  head.surface = surface.name
  local plan = owned_passes(forces, names)
  if #plan > MAX_PASSES then return too_many(head, plan, forces, { surface }, "counting every force on this surface") end
  local by_force = {}
  for _, pass in ipairs(plan) do
    local n = surface.count_entities_filtered{ force = pass.force.name, name = pass.name }
    by_force[pass.force.name] = (by_force[pass.force.name] or 0) + n
  end
  local rows = {}
  for _, force in ipairs(forces) do rows[#rows + 1] = { force = force.name, count = by_force[force.name] or 0 } end
  head.surfaces_counted = 1
  return finish_rows(rows, a, head)
end

--- all=true for `names`, already resolved; `head` holds name, names or
--- type for the reply.
function M.run(a, names, head)
  local forces = sweep_forces()
  -- No surface named means "how many does each force have", and the engine
  -- already knows: LuaForce::get_entity_count is documented O(1), so this
  -- answers at 40 forces exactly as fast as at 4.
  if a.surface == nil or a.surface == "" or a.surface == "all" then
    head.surface = "all"
    if a.per_surface == true then
      if #names > 1 then error("per_surface counts one name at a time: pass name, not names or type", 0) end
      return entity_count_per_surface(a, forces, names[1])
    end
    local rows = {}
    for _, force in ipairs(forces) do
      local count, by_name = counted(force, names)
      rows[#rows + 1] = { force = force.name, count = count, by_name = by_name }
    end
    return finish_rows(rows, a, head)
  end
  return on_surface(a, names, head, forces)
end

return M
