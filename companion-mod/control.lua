-- AI Agent Bridge -- companion mod, control stage
--
-- Wiring only. Every concern lives under scripts/; this file requires each
-- module and hooks it into Factorio's command, remote-interface and event
-- registration points. Read CONTEXT.md and PLAN.md at the repo root first --
-- they define the domain words and the aab-rpc-v1 protocol this wires up.

local rpc         = require("scripts.rpc")
local questions    = require("scripts.questions")
local remote_iface = require("scripts.remote")
local engine_tools = require("scripts.tools.engine")
local events       = require("scripts.events")

-- Console commands. commands.add_command must run every time this mod's Lua
-- state starts (nothing about a command registration persists across a
-- save/load), so this happens unconditionally here rather than inside
-- on_init/on_load.
commands.add_command("aab-rpc", "AI Agent Bridge: the aab-rpc-v1 protocol command (RCON).", rpc.handle)
questions.register_command()

-- Remote interfaces: same "every load" requirement as commands above.
remote_iface.register()
engine_tools.register()

-- Event handlers feeding events.jsonl.
events.register()

script.on_init(function()
  questions.ensure_storage()
end)

script.on_configuration_changed(function()
  questions.ensure_storage()
end)
