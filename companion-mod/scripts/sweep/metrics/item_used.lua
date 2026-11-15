-- AI Agent Bridge - scripts/sweep/metrics/item_used.lua
-- Author: bits-orio
-- License: MIT
--
-- The item_used sweep metric: how much of one item each force has ever
-- used, the consumption side of item_made. "Who has used the most coal?"
-- cost twenty production_since calls, one per team, with nothing to sweep.
-- production_since's `consumed` is the output side of the item statistics
-- (LuaFlowStatistics output_counts, what machines and labs took in), with
-- since_tick = 0 the engine's lifetime counters over every surface and
-- quality, so this is the same one call per force item_made makes.

return require("scripts.sweep.metrics.item_lifetime").metric("item_used", "consumed")
