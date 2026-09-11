-- AI Agent Bridge - scripts/ask_command.lua
-- Author: bits-orio
-- License: MIT
--
-- The /ask console command, the way a player asks without any setting being
-- touched. PLAN.md open question 1: /ask may already belong to another mod, so
-- this registers it under pcall and falls back to /aab-ask, then remembers
-- which name is live so the aab-rpc `status` op can tell the service.
--
-- Registered every load. Nothing about a command registration survives a
-- save/load, and nothing about it is stored.

local questions = require("scripts.questions")

local PREFERRED = "ask"
local FALLBACK = "aab-ask"
local HELP = "Ask the AI Agent Bridge a question about this game."

local active_name -- module-local, reset every session by M.register

local M = {}

--- Which command name is live this session, "ask" or "aab-ask".
function M.active_name()
  return active_name
end

local function handle(cmd)
  local player = cmd.player_index and game.get_player(cmd.player_index)
  -- Player-only. An RCON caller has no player_index and uses the
  -- ai-agent-bridge-v1 ask() function instead.
  if not player then return end
  if not cmd.parameter or cmd.parameter == "" then
    player.print("[AI Agent Bridge] Usage: /" .. active_name .. " <question>")
    return
  end
  questions.ask_as_player(player, cmd.parameter)
end

function M.register()
  if pcall(commands.add_command, PREFERRED, HELP, handle) then
    active_name = PREFERRED
  else
    commands.add_command(FALLBACK, HELP, handle)
    active_name = FALLBACK
  end
end

return M
