-- AI Agent Bridge - scripts/tools/platform_lookup.lua
-- Author: bits-orio
-- License: MIT
--
-- The four platform columns shared by list_surfaces and entity_count's
-- per_surface breakdown, so a platform surface reads the same way in both
-- tools: which team owns it, where it is stopped, what it is doing. One
-- place, so the two cannot drift on what a platform row looks like.
--
-- API names verified against the 2.0.77 docs,
-- /home/shobhitg/factorio/doc-html/runtime-api.json:
--   LuaSurface::platform -> LuaSpacePlatform, optional.
--   LuaSpacePlatform::name (string), ::force (LuaForce, never nil),
--     ::space_location (LuaSpaceLocationPrototype, optional: nil while the
--     platform is in flight between locations), ::state
--     (defines.space_platform_state), ::scheduled_for_deletion (uint32,
--     never nil: "0 if not scheduled for deletion").
--   LuaPrototypeBase::name, inherited by LuaSpaceLocationPrototype.
--
-- scheduled_for_deletion is a tick count, not a boolean: `if not
-- platform.scheduled_for_deletion` is false for every healthy platform,
-- because 0 is truthy in Lua, so that naive test would skip every one of
-- them. The correct test, used below, is `(x or 0) == 0`.

local STATE_NAMES = {
  [defines.space_platform_state.no_path]                  = "no path",
  [defines.space_platform_state.no_schedule]               = "no schedule",
  [defines.space_platform_state.on_the_path]               = "on the path",
  [defines.space_platform_state.paused]                    = "paused",
  [defines.space_platform_state.starter_pack_on_the_way]   = "starter pack on the way",
  [defines.space_platform_state.starter_pack_requested]    = "starter pack requested",
  [defines.space_platform_state.waiting_at_station]        = "waiting at station",
  [defines.space_platform_state.waiting_for_departure]     = "waiting for departure",
  [defines.space_platform_state.waiting_for_starter_pack]  = "waiting for starter pack",
}

local M = {}

--- Adds platform, owner, location and state to `row` when `surface` carries a
--- live platform, leaving `row` untouched otherwise: an ordinary planet
--- surface, or a platform pending deletion, reads exactly as it did before
--- this module existed. `location` is left unset rather than nil while the
--- platform is in flight, so the JSON writer drops it rather than the caller
--- having to.
--- The platform `surface` carries, or nil when it carries none, when the
--- object has been invalidated, or when one is already counting down to
--- deletion. Takes anything with a `.platform` field shaped like a
--- LuaSpacePlatform rather than a real LuaSurface, so a test can hand it a
--- plain table.
---
--- This is the single definition of "live platform". It lived in two files
--- once, and the copies had already drifted: one checked `.valid` and the
--- other did not, so an invalidated platform read as live on one path and not
--- the other.
function M.live(surface)
  local platform = surface and surface.platform
  if not platform or platform.valid == false then return nil end
  if (platform.scheduled_for_deletion or 0) ~= 0 then return nil end
  return platform
end

function M.merge_into(row, surface)
  local platform = M.live(surface)
  if not platform then return end
  row.platform = platform.name
  row.owner = platform.force and platform.force.name or nil
  row.state = STATE_NAMES[platform.state] or "unknown"
  if platform.space_location then row.location = platform.space_location.name end
end

return M
