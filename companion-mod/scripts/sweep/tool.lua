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
local flow     = require("scripts.tools.flow")

local M = {}

M.manifest = {
  sweep = {
    -- Generated from the registry rather than typed out here, so the
    -- vocabulary this description promises can never list a metric that
    -- is not registered or leave one out (the phase5 contract: "The tool's
    -- manifest description must GENERATE its metric vocabulary from the
    -- registry").
    desc = "Which force, surface, platform or player has the most of something, in one call instead of one per force. metric, with the axes it groups by, the first the default: " ..
           registry.metric_line() ..
           ". Another mod may declare more. subject is the entity, item or fluid prototype a metric measures, e.g. lab or iron-plate. " ..
           "Rows sort largest first. An unknown metric, or an axis a metric does not group by, answers found=false with every metric's card.",
    params = {
      metric  = "string! which metric to sweep; see desc for the full list",
      axis    = "string force, surface, platform, player or force+surface; default is the metric's first",
      subject = "string the entity, item or fluid prototype name a metric marked needs subject measures",
      window  = "string averaging window for a rate metric, one of " .. flow.window_names .. "; default one_minute",
      limit   = "integer rows to return, default " .. envelope.DEFAULT_ROWS .. ", max " .. envelope.MAX_ROWS,
    },
  },
}

local function sweep(a)
  return walk.run(a or {})
end

M.functions = { sweep = sweep }

return M
