-- AI Agent Bridge - scripts/sweep/metrics/item_made.lua
-- Author: bits-orio
-- License: MIT
--
-- The item_made sweep metric: how much of one item each force has ever made.
-- Delegates entirely to scripts/tools/production_since.lua's own
-- `production_since` function with since_tick = 0, the exact path that file's
-- own header documents ("since_tick 0 counts the whole game, exactly"), one
-- call per force. Surface is left out on every call so the tool sums every
-- surface itself, the same "a force's total never depends on the model
-- guessing where that force lives" rule production_since.lua's own
-- surfaces_for() already follows.
--
-- Force axis only. production_since answers "how much", not "where": its own
-- per-surface breakdown is already bounded to MAX_BREAKDOWN=5 and carried as
-- informational rows beside a `surfaces_counted` total, not a second sweep
-- axis to regroup by. The `produced` figure this metric reads is the exact
-- sum computed before that breakdown is ever cut (production_since.lua:
-- "the three numbers the question asked for come first"), so nothing here
-- is ever a ranking over a truncated sample by construction. The generic
-- shown-below-total guard in item_lifetime.lua still runs, in case a future
-- production_since ever grows a top-level cut of its own.

-- The builder lives in item_lifetime.lua, shared with item_used.

return require("scripts.sweep.metrics.item_lifetime").metric("item_made", "produced")
