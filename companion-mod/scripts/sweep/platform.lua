-- AI Agent Bridge - scripts/sweep/platform.lua
-- Author: bits-orio
-- License: MIT
--
-- Whether one surface is a live space platform, for the sweep's platform axis
-- and its prefilter. Isolated in its own file so the one landmine that lives
-- here has exactly one place to get right, and so it can be unit tested
-- against a plain table with no fake game behind it at all.
--
-- API verified against ~/factorio/doc-html/runtime-api.json (the 2.0.77
-- dump, application "factorio", stage "runtime"), read directly rather than
-- assumed:
--   LuaSurface::platform -> LuaSpacePlatform, optional.
--   LuaSpacePlatform::scheduled_for_deletion: "If this platform is scheduled
--     for deletion. Returns how many ticks are left before the platform will
--     be deleted. 0 if not scheduled for deletion."
--   LuaSpacePlatform::name: "The name of this space platform."
-- In Lua, 0 is truthy, so `if not surface.platform.scheduled_for_deletion`
-- reads false, "not scheduled", for every platform including one 30 seconds
-- from deletion, and would count it in every sweep. The only correct test is
-- `(scheduled_for_deletion or 0) == 0`; the `or 0` covers a stub or an older
-- engine that leaves the field nil rather than writing a live zero.

local platform_lookup = require("scripts.tools.platform_lookup")

local M = {}

--- The platform's own name when `surface` carries a live one, nil for a
--- plain surface or a platform already counting down to deletion. Takes
--- anything with a `.platform` field shaped like a LuaSpacePlatform, not a
--- real LuaSurface specifically, so a test can hand it a plain table.
function M.name_of(surface)
  local platform = platform_lookup.live(surface)
  return platform and platform.name or nil
end

return M
