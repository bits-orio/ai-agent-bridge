-- AI Agent Bridge - scripts/personality_command.lua
-- Author: bits-orio
-- License: MIT
--
-- /aab-personality: shows the bot's voice to anyone, and lets an admin change
-- it without opening the map settings. With no argument it prints the value;
-- "reset" clears it back to the operator's config; anything else becomes the
-- value, "off" included. Only an admin, the server console or RCON may change
-- it (the last two arrive with no player_index), and a player who is not an
-- admin is told so.
--
-- Registered every load, like /ask and /aab-rpc. The aab- prefix keeps it
-- clear of every other mod's commands; registration still runs under pcall,
-- so a mod that took the name anyway costs this command and nothing else.

local personality = require("scripts.personality")

local NAME = "aab-personality"
local HELP = "AI Agent Bridge: show how the bot sounds. Admins: /aab-personality <how it should sound>, off, or reset."
local PREFIX = "[AI Agent Bridge] "

local M = {}

--- One line back to whoever ran the command: the player, or the server
--- console and RCON, which have no player to print to.
local function tell(player, text)
  if player then
    player.print(PREFIX .. text)
    return
  end
  rcon.print(PREFIX .. text)
  log(PREFIX .. text)
end

local function describe(value)
  if value == "" then return "No personality is set in game: the operator's config decides how the bot sounds." end
  if value == "off" then return "Personality: off. The bot answers with no voice." end
  return "Personality: " .. value
end

--- The new value for what an admin typed, or nil and why it was refused.
local function wanted_value(text)
  local lowered = text:lower()
  if lowered == "reset" then return "" end
  if lowered == "off" then return "off" end
  if #text > personality.MAX_BYTES then
    return nil, "That is " .. #text .. " bytes; a personality holds at most " .. personality.MAX_BYTES .. ". Shorten it and try again."
  end
  return text
end

--- What the caller is told once a value is set. A question already asked
--- keeps the voice it was asked with, so each says when it takes effect.
local function confirmation(value)
  if value == "" then
    return "Personality reset: the operator's config decides how the bot sounds from the next question on."
  end
  if value == "off" then return "Personality off: the bot answers with no voice from the next question on." end
  return "Personality set, from the next question on: " .. value
end

local function change(player, text)
  local value, refused = wanted_value(text)
  if not value then return tell(player, refused) end
  personality.set(value)
  log("[ai-agent-bridge] personality changed by " .. (player and player.name or "the server") .. ": " .. describe(value))
  tell(player, confirmation(value))
end

local function handle(cmd)
  local player = cmd.player_index and game.get_player(cmd.player_index) or nil
  if cmd.player_index and not (player and player.valid) then return end
  local text = (cmd.parameter or ""):gsub("^%s+", ""):gsub("%s+$", "")
  if text == "" then return tell(player, describe(personality.current())) end
  if player and not player.admin then
    return tell(player, "Only an admin can change how the bot sounds. /" .. NAME .. " alone shows it.")
  end
  change(player, text)
end

function M.register()
  local registered, why = pcall(commands.add_command, NAME, HELP, handle)
  if not registered then
    log("[ai-agent-bridge] /" .. NAME .. " is taken by another mod, so it is not registered: " .. tostring(why))
  end
end

return M
