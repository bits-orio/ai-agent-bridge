-- AI Agent Bridge - control.lua
-- Author: bits-orio
-- License: MIT
--
-- AI Agent Bridge, companion mod, control stage.
--
-- Wiring only. Every concern lives under scripts/; this file requires each
-- module and hooks it into Factorio's command, remote-interface and event
-- registration points. Read CONTEXT.md and PLAN.md at the repo root first,
-- they define the domain words and the aab-rpc-v1 protocol this wires up.

local rpc          = require("scripts.rpc")
local questions    = require("scripts.questions")
local ask_command  = require("scripts.ask_command")
local remote_iface = require("scripts.remote")
local engine_tools = require("scripts.tools.engine")
local events       = require("scripts.events")
local chat         = require("scripts.chat")
local ask_rate     = require("scripts.ask_rate")

-- Console commands. commands.add_command must run every time this mod's Lua
-- state starts (nothing about a command registration persists across a
-- save/load), so this happens unconditionally here rather than inside
-- on_init/on_load.
commands.add_command("aab-rpc", "AI Agent Bridge: the aab-rpc-v1 protocol command (RCON).", rpc.handle)
ask_command.register()

-- Remote interfaces: same "every load" requirement as commands above.
remote_iface.register()
engine_tools.register()

-- Event handlers feeding events.jsonl.
events.register()

-- One handler per event per mod: a second script.on_event for the same event
-- replaces the first. on_console_chat has two readers, so the fan-out is here
-- rather than inside either of them. Logging runs first, so a prefixed
-- question appears in events.jsonl as the chat line the player typed and then
-- as the question it became.
script.on_event(defines.events.on_console_chat, function(e)
  events.on_console_chat(e)
  chat.on_console_chat(e)
end)

script.on_event(defines.events.on_player_left_game, function(e)
  events.on_player_left_game(e)
  ask_rate.forget(e.player_index)
end)

script.on_init(function()
  questions.ensure_storage()
end)

script.on_configuration_changed(function()
  questions.ensure_storage()
end)
