-- AI Agent Bridge - scripts/tools/basics.lua
-- Author: bits-orio
-- License: MIT
--
-- The three tools that need nothing but game state: who the forces are, who
-- plays on one, and what one is researching. Registered on the companion's own
-- provider interface by scripts/tools/engine.lua.

local force_lookup = require("scripts.tools.force_lookup")
local bounded      = require("scripts.tools.bounded")

local DEFAULT_PLAYERS = 20
local MAX_PLAYERS = 50
local DEFAULT_FORCES = 50
local MAX_FORCES = bounded.MAX_FORCES

local M = {}

M.manifest = {
  list_forces = {
    desc = "Forces on the server: name, players, connected players, by name. Forces that have never had a player are left out as empty slots unless include_empty=true; empty beside shown says how many were left out. total beside shown says if there are more.",
    params = {
      include_empty = "boolean default false: also list forces that never had a player, the team slots a scenario mod made in advance and the engine's own",
      limit         = "integer rows, default " .. DEFAULT_FORCES .. ", max " .. MAX_FORCES,
    },
  },
  list_players = {
    desc = "Players of one force: name, connected, admin. Connected only unless connected=false. Quote total and known, not the row count. all=true answers every force in one call, name and force per row, with total and shown but no known, and limit is ignored: use it for any who-is-online or every-player question instead of one call per force.",
    params = {
      connected = "boolean default true: connected only; false: every player ever",
      limit     = "integer rows, default " .. DEFAULT_PLAYERS .. ", max " .. MAX_PLAYERS,
      all       = "boolean default false: one row per player, every force; force ignored",
    },
  },
  current_research = {
    desc = "The technology one force is researching now, if any, and its progress. all=true answers for every force that has players or is researching, one row each in a single call: use it for any each-team or every-force question instead of one call per force.",
    params = {
      all = "boolean default false: one row per force with players or research; the force argument is ignored",
    },
  },
}

-- The `force` argument is ignored here: this is the one tool that lists every
-- force, so a single force name would make no sense (CONTEXT.md: force is
-- reserved and injected on every OTHER tool). A scenario mod can run dozens of
-- forces, so the row count is bounded like every other enumeration.
--
-- A force nobody has ever joined is scaffolding, not a team: a team mod makes
-- one per slot when the map starts, and the engine has its own. They are left
-- out unless asked for, and counted in `empty` so the reply still says how many
-- forces the game holds. A team whose players are all offline has had players,
-- so it stays in.
local function list_forces(a)
  a = a or {} -- the only tool that is useful with no arguments at all
  -- The asker's own force is never an empty slot, whoever has joined it. The
  -- service fills force with the asker's, and on a server with nobody
  -- connected every force has zero players, so the empty-slot rule hid all of
  -- them including the one asking: the e2e rig answered "what forces are
  -- there" with forces {} and empty 3 from the moment 1.0.3 shipped the rule.
  local mine = a.force
  local rows, empty = {}, 0
  for _, force in pairs(game.forces) do
    local ever = #force.players
    if ever > 0 or a.include_empty == true or force.name == mine then
      rows[#rows + 1] = {
        name = force.name,
        player_count = ever,
        connected_player_count = #force.connected_players,
      }
    else
      empty = empty + 1
    end
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, bounded.limit(a.limit, DEFAULT_FORCES, MAX_FORCES))
  return { total = #rows, empty = empty, shown = #shown, forces = shown }
end

-- all=true is the who-is-online question in one call, across every force
-- instead of one list_players per force. The row drops `admin`, which repeats
-- once per player across every force here and buys nothing a who-is-online
-- question asks for, and adds `force`, since that is now the only thing that
-- says whose player a row is. Sorted by name
-- and bounded like every other sweep; a limit argument is for the single-force
-- case, so all=true is capped at MAX_PLAYERS the same way current_research's
-- all=true is capped at MAX_FORCES rather than reading a.limit.
local function list_players_all(a)
  local connected_only = a.connected ~= false
  local rows = {}
  for _, force in pairs(game.forces) do
    for _, player in pairs(force.players) do
      if player.connected or not connected_only then
        rows[#rows + 1] = { name = player.name, force = force.name, connected = player.connected }
      end
    end
  end
  bounded.by_name(rows)
  local shown = bounded.cut(rows, MAX_PLAYERS)
  return { total = #rows, shown = #shown, players = shown }
end

-- force.players holds every player the force has ever had, not the ones online
-- now, so a public server's list runs to hundreds. Connected only by default,
-- and bounded either way.
local function list_players(a)
  if a.all == true then
    return list_players_all(a)
  end
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

local function research_row(force)
  local tech = force.current_research
  if not tech then
    return { force = force.name, researching = false }
  end
  return {
    force = force.name, researching = true, tech = tech.name,
    level = tech.level, progress = bounded.round(force.research_progress, 2),
  }
end

-- all=true is the every-team question in one call. The rows are the forces
-- that have players or research going; an empty slot has neither, and a force
-- that is researching with nobody online is still worth a row.
local function current_research(a)
  a = a or {}
  if a.all == true then
    local rows = {}
    for _, force in pairs(game.forces) do
      if #force.players > 0 or force.current_research then
        rows[#rows + 1] = research_row(force)
      end
    end
    table.sort(rows, function(x, y) return x.force < y.force end)
    local shown = bounded.cut(rows, MAX_FORCES)
    return { total = #rows, shown = #shown, forces = shown }
  end
  return research_row(force_lookup.require_force(a.force))
end

M.functions = {
  list_forces = list_forces,
  list_players = list_players,
  current_research = current_research,
}

return M
