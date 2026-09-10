-- The three tools that need nothing but game state: who the forces are, who
-- plays on one, and what one is researching. Registered on the companion's own
-- provider interface by scripts/tools/engine.lua.

local force_lookup = require("scripts.tools.force_lookup")
local bounded      = require("scripts.tools.bounded")

local DEFAULT_PLAYERS = 20
local MAX_PLAYERS = 50
local DEFAULT_FORCES = 50
local MAX_FORCES = 100

local M = {}

M.manifest = {
  list_forces = {
    desc = "The forces on the server: name, player count, connected player count. Sorted by name. The reply reports total beside shown, so raise limit if there are more forces than came back.",
    params = {
      limit = "integer how many rows to return, default " .. DEFAULT_FORCES .. ", at most " .. MAX_FORCES,
    },
  },
  list_players = {
    desc = "Players belonging to one force: name, connected, admin. Connected players only unless you pass connected=false. The reply reports known, every player the force has ever had, total, how many matched, and shown, how many rows came back, so say \"12 online of 214 known\" rather than counting rows.",
    params = {
      connected = "boolean true (the default) for connected players only, false for every player the force has ever had",
      limit     = "integer how many rows to return, default " .. DEFAULT_PLAYERS .. ", at most " .. MAX_PLAYERS,
    },
  },
  current_research = {
    desc = "The technology one force is currently researching, if any, and its progress.",
  },
}

-- The `force` argument is ignored here: this is the one tool that lists every
-- force, so a single force name would make no sense (CONTEXT.md: force is
-- reserved and injected on every OTHER tool). A scenario mod can run dozens of
-- forces, so the row count is bounded like every other enumeration.
local function list_forces(a)
  a = a or {} -- the only tool that is useful with no arguments at all
  local rows = {}
  for _, force in pairs(game.forces) do
    rows[#rows + 1] = {
      name = force.name,
      player_count = #force.players,
      connected_player_count = #force.connected_players,
    }
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_FORCES, MAX_FORCES))
  return { total = #rows, shown = #shown, forces = shown }
end

-- force.players holds every player the force has ever had, not the ones online
-- now, so a public server's list runs to hundreds. Connected only by default,
-- and bounded either way.
local function list_players(a)
  local force = force_lookup.require_force(a.force)
  local connected_only = a.connected ~= false
  local rows = {}
  for _, player in pairs(force.players) do
    if player.connected or not connected_only then
      rows[#rows + 1] = { name = player.name, connected = player.connected, admin = player.admin }
    end
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_PLAYERS, MAX_PLAYERS))
  return {
    force = force.name,
    connected_only = connected_only,
    known = #force.players,
    total = #rows,
    shown = #shown,
    players = shown,
  }
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

M.functions = {
  list_forces = list_forces,
  list_players = list_players,
  current_research = current_research,
}

return M
