-- AI Agent Bridge - scripts/tools/force_lookup.lua
-- Author: bits-orio
-- License: MIT
--
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

local labels = require("scripts.labels")

local M = {}

--- The force a label names, when a labels provider calls one of the forces
--- that. Case-insensitive, spaces trimmed, so "team 02" and "Team 02" both
--- find team-2 when that is its label. Nil when no label matches.
local function by_label(name)
  local wanted = name:lower():gsub("^%s+", ""):gsub("%s+$", "")
  for force_name, label in pairs(labels.cached_map()) do
    if label:lower() == wanted and game.forces[force_name] then
      return game.forces[force_name]
    end
  end
  return nil
end

--- The force called `name`: by its force name first, then by the label
--- players use for it. A model that read a label in a tool result and
--- passed it back still reaches the right force, and the reply carries the
--- force name so the next call gets it right.
function M.require_force(name)
  if type(name) ~= "string" or name == "" then
    error("force is required", 0)
  end
  -- game.forces is indexable directly by name (LuaGameScript::forces).
  local force = game.forces[name] or by_label(name)
  if not force then
    error("unknown force: " .. name .. " (use the force name, not a display name)", 0)
  end
  return force
end

return M
