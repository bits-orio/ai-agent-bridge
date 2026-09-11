-- AI Agent Bridge - scripts/audience.lua
-- Author: bits-orio
-- License: MIT
--
-- Who reads a line about a question: its audience (docs/design/phase3-spec.md
-- part 2). Shared by the answer renderer and the question echo, so the two
-- can never disagree about who was allowed to hear what.
--
-- A private question prints to the audience fixed on its row when it was
-- asked. A global one prints to the server, or to the asker alone when the
-- aab-answer-audience setting says so. A private audience that has gone (a
-- force removed since) falls back to the asker alone: privacy over reach.

local player_lookup = require("scripts.player_lookup")

local M = {}

M.NAME = "[AI Agent Bridge]"

--- The asker as a connected player, or nil.
function M.asker(question)
  local player = player_lookup.by_index(question.player_index)
  if player and player.valid and player.connected then return player end
  return nil
end

--- The companion's name followed by the question's channel tag, the way a
--- player's own line carries name then badge.
function M.head(question)
  if type(question.tag) == "string" and question.tag ~= "" then
    return M.NAME .. " " .. question.tag
  end
  return M.NAME
end

local function print_to_players(indices, text)
  local reached = false
  for _, index in ipairs(indices) do
    local player = game.get_player(index)
    if player and player.valid and player.connected then
      player.print(text)
      reached = true
    end
  end
  return reached
end

--- Prints `text` to the question's audience, or to the asker alone when
--- `to_asker` is set: a refusal (over quota, over budget) is between the
--- service and the one who asked, and printing it to everyone would turn a
--- player's spam into the server's.
function M.deliver(question, text, to_asker)
  if to_asker then
    local player = M.asker(question)
    if player then player.print(text) end
    return
  end
  if question.private then
    local audience = type(question.audience) == "table" and question.audience or {}
    if audience.force then
      local force = game.forces[audience.force]
      if force and force.valid then force.print(text) return end
    elseif audience.players and print_to_players(audience.players, text) then
      return
    end
    local player = M.asker(question)
    if player then player.print(text) end
    return
  end

  local setting = settings.global["aab-answer-audience"]
  local mode = setting and setting.value or "server"
  if mode == "asker" then
    local player = M.asker(question)
    if player then player.print(text) return end
  end
  game.print(text)
end

return M
