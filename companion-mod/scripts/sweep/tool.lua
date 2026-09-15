-- AI Agent Bridge - scripts/sweep/tool.lua
-- Author: bits-orio
-- License: MIT
--
-- sweep: the one tool answering "which force, surface, platform or player
-- has the most of something", across every one of them in a single call
-- instead of one call per force (docs/design/phase5-sweep.md). Registered on
-- the companion's own tool provider by scripts/tools/engine.lua exactly like
-- every other tool module; the walk, the axis vocabulary and the metric
-- registry it delegates to live alongside this file, under scripts/sweep/.
--
-- This file owns only the manifest and the thin function wrapper walk.run
-- needs; it decides nothing about metrics, axes or budgets itself.

local registry = require("scripts.sweep.registry")
local walk     = require("scripts.sweep.walk")
local envelope = require("scripts.sweep.envelope")

local M = {}

M.manifest = {
  sweep = {
    -- Generated from the registry rather than typed out here, so the
    -- vocabulary this description promises can never list a metric that
    -- is not registered or leave one out (the phase5 contract: "The tool's
    -- manifest description must GENERATE its metric vocabulary from the
    -- registry").
    desc = "Which force, surface, platform or player has the most of something, in one call " ..
           "instead of one per force. metric is one of: " .. registry.metric_line() ..
           ". axis groups the rows; each metric's own list above is what it accepts, and its " ..
           "first is the default when axis is left out. subject names the specific thing a " ..
           "metric measures, an entity prototype name for entities, e.g. lab; only entities " ..
           "needs one. Rows sort largest value first. An unrecognised metric, or an axis a " ..
           "metric does not sweep by, answers found=false with every metric's own card, so a " ..
           "wrong guess recovers in one round instead of a one-line error.",
    params = {
      metric  = "string! which metric to sweep; see desc for the full list",
      axis    = "string group rows by force, surface, platform, player, or force+surface; default depends on the metric",
      subject = "string what the metric measures, e.g. an entity prototype name for entities; required only when the metric needs one",
      limit   = "integer rows to return, default " .. envelope.DEFAULT_ROWS .. ", max " .. envelope.MAX_ROWS,
    },
  },
}

local function sweep(a)
  return walk.run(a or {})
end

M.functions = { sweep = sweep }

return M
