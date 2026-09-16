-- AI Agent Bridge - scripts/sweep/axes.lua
-- Author: bits-orio
-- License: MIT
--
-- One function per sweep axis. Each takes a canonical cell, a plain
-- { force?, surface?, platform?, player? } table a metric's own read()
-- builds from whatever its delegate returned, and answers the label that
-- cell's row carries as the envelope's "name" column, or nil when this axis
-- has nothing to say about that cell (a plain planet asked about "platform").
--
-- Nothing here drives a walk or reads game state; that is walk.lua and each
-- metric's own read(). This file only names what a row is called.

local M = {}

M.NAMES = { "force", "surface", "platform", "player", "force+surface" }

function M.force(cell) return cell.force end
function M.surface(cell) return cell.surface end
--- A platform row names the platform and, when the cell carries them, who
--- owns it and where it is: "Battle Cruiser (team-2, at nauvis)". A platform
--- has no map position, so "where is it" is the planet it is stopped at or
--- that it is in flight, and the row is the only place a sweep can say so:
--- question 90 on 2026-09-15 got a bare name back and answered the second
--- half of "which ship has most thrusters, where is it" with a gps tag at
--- 0,0 on a planet no tool had mentioned.
function M.platform(cell)
  if not cell.platform then return nil end
  -- The state already reads as words ("in flight" for a ship between
  -- planets, platform_lookup's own table), so nothing is translated here.
  local where
  if cell.location then
    where = "at " .. cell.location
  elseif cell.state then
    where = cell.state
  end
  local inside = {}
  if cell.owner then inside[#inside + 1] = cell.owner end
  if where then inside[#inside + 1] = where end
  local label = cell.platform
  if #inside > 0 then label = label .. " (" .. table.concat(inside, ", ") .. ")" end
  -- A platform is its own surface, and its hub sits at the origin of that
  -- surface, so a gps tag on it is the one ping that opens remote view on
  -- the ship. The row carries it ready-made: question 94 on the rig was told
  -- no such ping existed, because the rule before this one confused "a
  -- platform has no position on a planet" with "a platform cannot be pinged".
  if cell.surface then label = label .. " [gps=0,0," .. cell.surface .. "]" end
  return label
end
function M.player(cell) return cell.player end

-- The one compound axis: the force and surface a cell belongs to, joined
-- into one label since the envelope's row is always one name plus one value
-- (docs/design/phase5-sweep.md, the phase5 contract's envelope shape). Only
-- meaningful when a cell actually carries both; a cell missing either is not
-- a force-surface pair and contributes no row under this axis.
M["force+surface"] = function(cell)
  if not (cell.force and cell.surface) then return nil end
  return cell.force .. " on " .. cell.surface
end

return M
