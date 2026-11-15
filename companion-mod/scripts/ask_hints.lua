-- AI Agent Bridge - scripts/ask_hints.lua
-- Author: bits-orio
-- License: MIT
--
-- Teaching players how to ask. Someone who watches another player use the
-- bot never sees the command, so with aab-ask-hints set to all, each player
-- who has not asked gets one private line naming it: when they watch a
-- question echo, when an answer reaches them, or when they join.
--
-- storage.aab.hints.taught[player_index] = true once a player has asked or
-- has been hinted. Every peer runs the same code in the same order, so the
-- writes agree. The ask_command module is required lazily: it requires
-- questions, which requires this.

local audience      = require("scripts.audience")
local render_shapes = require("scripts.render_shapes")

local M = {}

function M.ensure_storage()
  storage.aab = storage.aab or {}
  storage.aab.hints = storage.aab.hints or { taught = {} }
  storage.aab.hints.taught = storage.aab.hints.taught or {}
end

--- The aab-ask-hints value: all, echo (default) or off.
function M.mode()
  local setting = settings.global["aab-ask-hints"]
  local value = setting and setting.value
  if value == "all" or value == "off" then return value end
  return "echo"
end

--- The command name that is live, "ask" or "aab-ask".
function M.command()
  return require("scripts.ask_command").active_name() or "aab-ask"
end

local function taught(player)
  M.ensure_storage()
  return storage.aab.hints.taught[player.index] == true
end

--- Marks a player as knowing how to ask, whatever the setting.
function M.mark(player)
  M.ensure_storage()
  storage.aab.hints.taught[player.index] = true
end

--- A removed player's entry goes; a player who only left keeps theirs.
function M.forget(player_index)
  M.ensure_storage()
  storage.aab.hints.taught[player_index] = nil
end

function M.line()
  local text = audience.NAME .. " You can ask too: /" .. M.command() .. " <your question>"
  local prefix = settings.global["aab-chat-prefix"]
  prefix = prefix and prefix.value
  if type(prefix) == "string" and prefix ~= "" then
    text = text .. ", or start a chat line with " .. prefix
  end
  -- The prefix is operator-typed: same strip and length net as any line.
  return render_shapes.plain_line(text)
end

--- Gives one player the hint if the setting is all and they are untaught.
function M.hint(player)
  if M.mode() ~= "all" or taught(player) then return end
  if not (player and player.valid and player.connected) then return end
  audience.print_one(player, M.line())
  M.mark(player)
end

--- Hints the question's audience, the asker excepted.
function M.hint_audience(question)
  if M.mode() ~= "all" then return end
  for _, player in ipairs(audience.recipients(question)) do
    if player.index ~= question.player_index then M.hint(player) end
  end
end

return M
