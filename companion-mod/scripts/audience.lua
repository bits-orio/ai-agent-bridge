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
-- aab-answer-audience setting says so, and then to nobody once that asker
-- has left. A private audience that has gone (a force removed since) falls
-- back to the asker alone: privacy over reach.

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

--- A private question's line: to the audience fixed on its row, or to the
--- asker alone when that audience has gone.
local function deliver_private(question, text)
  local audience = type(question.audience) == "table" and question.audience or {}
  if audience.force then
    local force = game.forces[audience.force]
    if force and force.valid then force.print(text) return end
  elseif audience.players and print_to_players(audience.players, text) then
    return
  end
  local player = M.asker(question)
  if player then player.print(text) end
end

--- A global question's line: to the server, or in asker mode to the asker
--- alone. In asker mode a player who has left gets nothing and nobody else
--- does either: printing to the server instead would hand everyone an answer
--- the operator chose to keep between one player and the bot. A question a
--- mod asked with no player has no asker to keep it for and prints to the
--- server.
local function deliver_global(question, text)
  local setting = settings.global["aab-answer-audience"]
  local mode = setting and setting.value or "server"
  if mode ~= "asker" or question.player_index == nil then
    game.print(text)
    return
  end
  local player = M.asker(question)
  if player then
    player.print(text)
  else
    log("[ai-agent-bridge] question " .. tostring(question.id)
      .. " printed to nobody: aab-answer-audience is asker and its asker has left")
  end
end

--- Prints `text` to the question's audience, or to the asker alone when
--- `to_asker` is set: a refusal (over quota, over budget) is between the
--- service and the one who asked, and printing it to everyone would turn a
--- player's spam into the server's.
function M.deliver(question, text, to_asker)
  if to_asker then
    local player = M.asker(question)
    if player then player.print(text) end
  elseif question.private then
    deliver_private(question, text)
  else
    deliver_global(question, text)
  end
end

return M
