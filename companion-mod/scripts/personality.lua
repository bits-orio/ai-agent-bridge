-- AI Agent Bridge - scripts/personality.lua
-- Author: bits-orio
-- License: MIT
--
-- The bot's voice, the runtime-global setting aab-personality. The companion
-- never interprets it: it fixes the value on each question row when the
-- question is asked, the poll reply carries it, and the service decides what
-- it means. Blank leaves the voice to the operator's config, "off" turns it
-- off, a preset name picks a fixed voice, anything else describes one.
--
-- A mod may write its own runtime-global settings (LuaSettings::global,
-- 2.0.77 and 2.1.17: "Mods can only change their own settings"), so the
-- admin command sets this one in game; the map settings dialog works too.

local text_clip = require("scripts.text_clip")

local M = {}

M.SETTING = "aab-personality"

-- The most a question carries, in bytes. Sixteen poll rows at this and the
-- question's own 400 still sit far under the rpc reply cap.
M.MAX_BYTES = 500

local function trim(s)
  return (s:gsub("^%s+", ""):gsub("%s+$", ""))
end

--- The setting as it stands, trimmed, "" when unset.
function M.current()
  local setting = settings.global[M.SETTING]
  local value = setting and setting.value
  if type(value) ~= "string" then return "" end
  return trim(value)
end

--- The value a new question carries: the setting now, at most MAX_BYTES cut
--- on a character boundary, nil when blank so the poll row leaves it out.
--- The command refuses a longer value; the settings dialog cannot.
function M.for_question()
  local value = trim(text_clip.bytes(M.current(), M.MAX_BYTES))
  if value == "" then return nil end
  return value
end

--- Writes the setting. Every peer runs the command that calls this, so the
--- write is the same everywhere.
function M.set(value)
  settings.global[M.SETTING] = { value = value }
end

return M
