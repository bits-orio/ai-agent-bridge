-- Shared "resolve a force by name" helper for engine tools. Every engine
-- tool except list_forces requires `force` and should error() on an unknown
-- one, so scripts/probe.lua's call() maps it to a plain provider_error reply
-- for the caller -- tools never write storage or return partial data for a
-- bad name.

local M = {}

function M.require_force(name)
  if type(name) ~= "string" or name == "" then
    error("force is required")
  end
  -- game.forces is indexable directly by name (LuaGameScript::forces).
  local force = game.forces[name]
  if not force then
    error("unknown force: " .. name)
  end
  return force
end

return M
