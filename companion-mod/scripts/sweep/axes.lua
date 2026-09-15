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
function M.platform(cell) return cell.platform end
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
