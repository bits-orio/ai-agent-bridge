-- AI Agent Bridge - scripts/sweep/metrics/players.lua
-- Author: bits-orio
-- License: MIT
--
-- The players sweep metric: who is where. Delegates to
-- scripts/tools/basics.lua's own `list_players` function with all=true, the
-- tool that already knows every player on every force; this file only
-- groups the rows it returns by the requested axis and counts them.
--
-- list_players{all=true} answers connected players only by default, the
-- same default the tool itself uses, so "which force has the most players"
-- means "online now" unless the caller widens it some other way.
--
-- Two axes, both a headcount: "force" sums to how many players stand on
-- each force, the "which team has the most players" question. "player" is
-- one row per player, value 1 each, which on its own just lists them; it
-- exists because player is one of the five axes the phase5 contract names,
-- and grouping the one sweep that actually has per-player rows by player is
-- the natural home for it. Neither surface nor platform axis applies:
-- list_players carries no surface for a player, only a force.

local basics_tool = require("scripts.tools.basics")
local pages = require("scripts.sweep.pages")
local axes         = require("scripts.sweep.axes")

local LIST_PLAYERS = basics_tool.functions.list_players

local M = {
  axes = { "force", "player" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
}

function M.read(_ctx, axis, _a)
  -- The delegate sorts by PLAYER NAME and pages its rows to the reply
  -- budget, so one page is an alphabetical slice rather than the whole
  -- roster, and a leader counted from it names the wrong team. Every page is
  -- read (scripts/sweep/pages.lua), and a cut no page reaches past is still
  -- refused whole rather than ranked.
  local players, refusal = pages.collect(LIST_PLAYERS, { all = true }, "players", "list_players")
  if not players then return nil, refusal end

  local by_cell, order = {}, {}
  for _, row in ipairs(players) do
    local cell = axes[axis]({ force = row.force, player = row.name })
    if cell then
      if not by_cell[cell] then order[#order + 1] = cell end
      by_cell[cell] = (by_cell[cell] or 0) + 1
    end
  end
  local rows = {}
  for _, cell in ipairs(order) do rows[#rows + 1] = { name = cell, value = by_cell[cell] } end
  return rows
end

return M
