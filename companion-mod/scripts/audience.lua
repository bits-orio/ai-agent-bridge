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

--- Whether the aab-answer-audience setting keeps a global answer to its
--- asker. Read by the question when it is made, so its log line can say so.
function M.asker_mode()
  local setting = settings.global["aab-answer-audience"]
  return (setting and setting.value or "server") == "asker"
end

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

--- One print that can never raise: delivery is the last step of an answer
--- that is already rendered and valid, and a failure in it is not the
--- artifact's fault. Says whether the line went out.
local function safe_print(target, text)
  local printed, why = pcall(function() target.print(text) end)
  if not printed then
    log("[ai-agent-bridge] delivery failed: " .. tostring(why):match("^[^\n]*"))
  end
  return printed
end

--- Prints to one player and says whether it went out. Shared with the hint
--- (scripts/ask_hints.lua), which prints one private line the same guarded way.
function M.print_one(player, text)
  return safe_print(player, text)
end

--- Prints one line to every connected player through the same guarded print
--- an answer uses. For a line that belongs to no question (scripts/say.lua).
function M.print_all(text)
  return safe_print(game, text)
end

--- The connected, valid players behind a list of indices. By_index, not
--- get_player: the engine raises for an index it refuses.
local function connected_by_index(indices)
  local out = {}
  for _, index in ipairs(indices) do
    local player = player_lookup.by_index(index)
    if player and player.valid and player.connected then out[#out + 1] = player end
  end
  return out
end

local function copy_list(list)
  local out = {}
  for _, player in ipairs(list) do out[#out + 1] = player end
  return out
end

--- Who a question's line reaches, decided in one place for the printing and
--- for the hint. Returns kind and value:
---   "server"  every connected player, printed once to the game
---   "force"   a force, value is the LuaForce
---   "players" value is a list of connected players
---   "none"    nobody
local function target(question)
  local asker_only = question.to_asker == true or M.asker_mode()
  if question.private then
    local audience = type(question.audience) == "table" and question.audience or {}
    if audience.force then
      local force = game.forces[audience.force]
      -- A row that stored the force's index only counts while the force under
      -- that name is still the same one; rows from before the index was
      -- stored have none and are read by name as they always were.
      local same = force and force.valid
        and (audience.force_index == nil or force.index == audience.force_index)
      if same then return "force", force end
    elseif audience.players then
      local listed = connected_by_index(audience.players)
      if #listed > 0 then return "players", listed end
    end
    -- A private audience that has gone: the asker alone, privacy over reach.
    local player = M.asker(question)
    if player then return "players", { player } end
    return "none"
  end
  -- A global question prints to the server, or in asker mode to the asker
  -- alone: a player who has left gets nothing and nobody else does either. A
  -- question a mod asked with no player has no asker to keep it for and
  -- prints to the server. A question logged as shown to its asker alone stays
  -- that way even when the setting has changed since.
  if not asker_only or question.player_index == nil then return "server" end
  local player = M.asker(question)
  if player then return "players", { player } end
  return "none"
end

--- The connected players this question's line reaches, in the engine's
--- order, as a fresh list. The hint reads it so it can never reach anyone
--- the answer itself did not.
function M.recipients(question)
  local kind, value = target(question)
  if kind == "server" then return copy_list(game.connected_players) end
  if kind == "force" then return copy_list(value.connected_players) end
  if kind == "players" then return copy_list(value) end
  return {}
end

--- Prints `text` to the question's audience, or to the asker alone when
--- `to_asker` is set: a refusal (over quota, over budget) is between the
--- service and the one who asked, and printing it to everyone would turn a
--- player's spam into the server's.
function M.deliver(question, text, to_asker)
  if to_asker then
    local player = M.asker(question)
    if player then safe_print(player, text) end
    return
  end
  local kind, value = target(question)
  if kind == "server" then
    safe_print(game, text)
  elseif kind == "force" then
    safe_print(value, text)
  elseif kind == "players" then
    for _, player in ipairs(value) do safe_print(player, text) end
  elseif not question.private then
    log("[ai-agent-bridge] question " .. tostring(question.id)
      .. " printed to nobody: aab-answer-audience is asker and its asker has left")
  end
end

return M
