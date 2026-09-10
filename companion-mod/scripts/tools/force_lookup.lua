-- Shared "resolve a force by name" helper for engine tools. Every engine
-- tool except list_forces requires `force` and should error() on an unknown
-- one, so scripts/probe.lua's call() maps it to a plain provider_error reply
-- for the caller. Tools never write storage or return partial data for a
-- bad name.
--
-- Every deliberate message a tool raises uses error(message, 0). Level 0 is
-- what keeps "force is required" from reaching the model as
-- "__ai-agent-bridge__/scripts/tools/force_lookup.lua:11: force is required":
-- the sentence is for whoever called the tool, and the path is noise in its
-- context window. An unexpected runtime error still carries its file and line,
-- which is what an operator reading the log needs.

local M = {}

function M.require_force(name)
  if type(name) ~= "string" or name == "" then
    error("force is required", 0)
  end
  -- game.forces is indexable directly by name (LuaGameScript::forces).
  local force = game.forces[name]
  if not force then
    error("unknown force: " .. name, 0)
  end
  return force
end

return M
