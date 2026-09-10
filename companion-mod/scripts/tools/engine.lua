-- The companion's own tool provider: engine reads exposed as agent_tools_v1
-- tools on the "ai-agent-bridge-tools" interface. Discovered by
-- scripts/probe.lua the same way any other mod's tools are -- the companion
-- is a provider like everyone else (CONTEXT.md "Provider").

local force_lookup = require("scripts.tools.force_lookup")
local production   = require("scripts.tools.production")

local INTERFACE = "ai-agent-bridge-tools"

-- No `force` arg: this is the one tool that lists every force, so a single
-- force name would make no sense (CONTEXT.md: force is reserved and
-- injected on every OTHER tool, but a provider never declares params it
-- doesn't use).
local function list_forces(_a)
  local rows = {}
  for _, force in pairs(game.forces) do
    rows[#rows + 1] = {
      name = force.name,
      player_count = #force.players,
      connected_player_count = #force.connected_players,
    }
  end
  table.sort(rows, function(x, y) return x.name < y.name end)
  return { forces = rows }
end

local function list_players(a)
  local force = force_lookup.require_force(a.force)
  local rows = {}
  for _, player in pairs(force.players) do
    rows[#rows + 1] = { name = player.name, connected = player.connected, admin = player.admin }
  end
  table.sort(rows, function(x, y) return x.name < y.name end)
  return { force = force.name, players = rows }
end

local function current_research(a)
  local force = force_lookup.require_force(a.force)
  local tech = force.current_research
  if not tech then
    return { force = force.name, researching = false }
  end
  return {
    force = force.name, researching = true, tech = tech.name,
    level = tech.level, progress = force.research_progress,
  }
end

local MANIFEST = {
  list_forces = { desc = "Every force on the server: name, player count, connected player count. Takes no arguments." },
  list_players = { desc = "Every player belonging to one force: name, connected, admin." },
  current_research = { desc = "The technology one force is currently researching, if any, and its progress." },
}
for name, entry in pairs(production.manifest) do
  MANIFEST[name] = entry
end

local M = {}

function M.register()
  remote.add_interface(INTERFACE, {
    agent_tools_v1 = function() return { v = 1, tools = MANIFEST } end,
    list_forces = list_forces,
    list_players = list_players,
    current_research = current_research,
    item_rate = production.item_rate,
    top_items = production.top_items,
  })
end

return M
