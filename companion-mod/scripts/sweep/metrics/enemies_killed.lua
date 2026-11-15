-- AI Agent Bridge - scripts/sweep/metrics/enemies_killed.lua
-- Author: bits-orio
-- License: MIT
--
-- The enemies_killed sweep metric: how many enemies each force has killed,
-- trees and rocks left out. The kills metric counts everything a force
-- destroyed, so "who killed the most biters" was answered by whoever felled
-- the most trees. kills{all=true} now splits that and returns an `enemies`
-- count per force; this reads that column through the kills metric's own
-- reader, so the two cannot drift apart.

local kills_metric = require("scripts.sweep.metrics.kills")

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = nil,
  unit = "count",
  places = 0,
  costly = false,
}

function M.read(_ctx, _axis, _a)
  return kills_metric.read_field("enemies")
end

return M
