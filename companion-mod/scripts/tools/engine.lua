-- AI Agent Bridge - scripts/tools/engine.lua
-- Author: bits-orio
-- License: MIT
--
-- The companion's own tool provider: engine reads exposed as agent_tools_v1
-- tools on the "ai-agent-bridge-tools" interface. Discovered by
-- scripts/probe.lua the same way any other mod's tools are, because the
-- companion is a provider like everyone else (CONTEXT.md "Provider").
--
-- Wiring only. Each module below owns one group of tools and declares both a
-- `manifest` and a `functions` table keyed by the same names; adding a tool
-- means adding a module here, never editing this file's body.

-- One local per module, then the list. A require() straight into a table
-- constructor is a trap: in the last slot it expands to every value require
-- returns, which is more than one on some Lua versions.
local basics           = require("scripts.tools.basics")
local surfaces         = require("scripts.tools.surfaces")
local production       = require("scripts.tools.production")
local production_since = require("scripts.tools.production_since")
local research         = require("scripts.tools.research")
local tech_status      = require("scripts.tools.tech_status")
local logistics        = require("scripts.tools.logistics")
local entity_count     = require("scripts.tools.entity_count")
local environment      = require("scripts.tools.environment")
local rockets          = require("scripts.tools.rockets")
local game_time        = require("scripts.tools.game_time")
local locate           = require("scripts.tools.locate")
local locate_player    = require("scripts.tools.locate_player")
local kills            = require("scripts.tools.kills")
local built            = require("scripts.tools.built")
local fluid_rate       = require("scripts.tools.fluid_rate")
local trains           = require("scripts.tools.trains")
local recipe_needs     = require("scripts.tools.recipe_needs")
local machine_status   = require("scripts.tools.machine_status")
local power            = require("scripts.tools.power")
local research_cost    = require("scripts.tools.research_cost")
local mods             = require("scripts.tools.mods")
local resources        = require("scripts.tools.resources")
local force_card       = require("scripts.tools.force_card")
local sweep            = require("scripts.sweep.tool")

local MODULES = {
  basics, surfaces, production, production_since,
  research, tech_status, logistics, entity_count, environment, rockets, game_time,
  locate, locate_player, kills, built, fluid_rate, trains, recipe_needs, sweep,
  machine_status, power, research_cost, mods, resources, force_card,
}

local INTERFACE = "ai-agent-bridge-tools"

local MANIFEST = {}
local FUNCTIONS = {}

for _, module in ipairs(MODULES) do
  for name, entry in pairs(module.manifest) do
    local fn = module.functions[name]
    -- A manifest entry with no function would advertise a tool that always
    -- answers no_tool. Fail at load instead, where it is obvious.
    if type(fn) ~= "function" then
      error("[ai-agent-bridge] tool " .. name .. " is in a manifest with no function behind it")
    end
    MANIFEST[name] = entry
    FUNCTIONS[name] = fn
  end
end

FUNCTIONS.agent_tools_v1 = function() return { v = 1, tools = MANIFEST } end

local M = {}

function M.register()
  remote.add_interface(INTERFACE, FUNCTIONS)
end

return M
