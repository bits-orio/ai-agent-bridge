-- AI Agent Bridge - scripts/ask_command.lua
-- Author: bits-orio
-- License: MIT
--
-- The /ask console command, the way a player asks without any setting being
-- touched. PLAN.md open question 1: /ask may already belong to another mod, so
-- /aab-ask is always registered, /ask is tried under pcall beside it, and the
-- aab-claim-ask-command startup setting lets an operator leave /ask alone. The
-- module remembers which name is live so the aab-rpc `status` op can tell the
-- service.
--
-- Registered every load. Nothing about a command registration survives a
-- save/load, and nothing about it is stored.

local questions = require("scripts.questions")
local ask_hints = require("scripts.ask_hints")

local PREFERRED = "ask"
local FALLBACK = "aab-ask"
local HELP = "Ask the AI Agent Bridge a question about this game."

local active_name -- module-local, reset every session by M.register

local M = {}

--- Which command name is live this session, "ask" or "aab-ask", nil if
--- neither could be registered.
function M.active_name()
  return active_name
end

local function handle(cmd)
  local player = cmd.player_index and game.get_player(cmd.player_index)
  -- Player-only. An RCON caller has no player_index and uses the
  -- ai-agent-bridge-v1 ask() function instead.
  if not player then return end
  if not cmd.parameter or cmd.parameter == "" then
    player.print("[AI Agent Bridge] Usage: /" .. (active_name or FALLBACK) .. " <question>")
    return
  end
  questions.ask_as_player(player, cmd.parameter)
end

--- Registers /aab-ask always, and /ask as well unless the startup setting
--- aab-claim-ask-command is off, because /ask may belong to another mod.
--- Each is tried under pcall: a name another mod already holds raises.
--- Both run the same handler. The name reported is /ask when it took,
--- else /aab-ask.
function M.register()
  active_name = nil
  if pcall(commands.add_command, FALLBACK, HELP, handle) then active_name = FALLBACK end
  local claim = settings.startup["aab-claim-ask-command"]
  if (claim == nil or claim.value ~= false) and pcall(commands.add_command, PREFERRED, HELP, handle) then
    active_name = PREFERRED
  end
  ask_hints.set_command(active_name)
end

return M
