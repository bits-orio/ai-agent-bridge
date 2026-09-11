-- AI Agent Bridge - scripts/chat.lua
-- Author: bits-orio
-- License: MIT
--
-- The chat trigger (PLAN.md open question 2). Off until an operator sets
-- aab-chat-prefix, so ordinary chat never reaches the model by accident.
-- A line a player types that starts with the prefix becomes a question, with
-- the rest of the line as its text.
--
-- The prefix is matched literally, leading and trailing spaces included, and
-- never as a Lua pattern. Pick something no ordinary sentence starts with,
-- "?" or "@ai " for example, because a prefix of "ai" would also fire on
-- "airlocks are cheaper".
--
-- control.lua owns the single on_console_chat handler and calls M.on_console_chat
-- from it. Nothing here loops back into that event: player.print writes to one
-- player's console and does not raise on_console_chat.

local questions = require("scripts.questions")

local M = {}

local function configured_prefix()
  local setting = settings.global["aab-chat-prefix"]
  local value = setting and setting.value
  if type(value) ~= "string" or value == "" then return nil end
  return value
end

local function trim(s)
  return (s:gsub("^%s+", ""):gsub("%s+$", ""))
end

--- Turns one prefixed chat line into a question. Called by control.lua.
function M.on_console_chat(e)
  local prefix = configured_prefix()
  if not prefix then return end
  if not e.player_index then return end
  local message = e.message
  if type(message) ~= "string" then return end
  if message:sub(1, #prefix) ~= prefix then return end

  local text = trim(message:sub(#prefix + 1))
  if text == "" then return end

  local player = game.get_player(e.player_index)
  if not (player and player.valid) then return end
  questions.ask_as_player(player, text, message)
end

return M
