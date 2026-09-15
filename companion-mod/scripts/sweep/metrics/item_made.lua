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
-- shown-below-total guard below still runs, the same discipline
-- metrics/entities.lua and metrics/players.lua apply, in case a future
-- production_since ever grows a top-level cut of its own.

local production_since_tool = require("scripts.tools.production_since")
local axes                  = require("scripts.sweep.axes")

-- Looked up at call time rather than captured at load: the same reason
-- entities.lua gives for its own delegate wrapper (scripts/sweep/metrics/
-- entities.lua), so a test can stand a stub into this seam without editing
-- this file.
local function PRODUCTION_SINCE(a) return production_since_tool.functions.production_since(a) end

local M = {
  axes = { "force" },
  default_axis = "force",
  subject = "item prototype name, e.g. iron-plate",
  subject_kind = "item",
  unit = "count",
  places = 0,
  -- since_tick = 0 reads the engine's own lifetime counters, the same O(1)-
  -- per-quality read production_since.lua's lifetime() uses; no surface is
  -- ever walked, so this costs nothing beyond one call per force.
  costly = false,
}

-- One call per live force. 600 mirrors item_rate's own budget below and
-- entity_count.lua's MAX_PASSES, set against a measured 322 force-and-surface
-- walk (docs/design/phase5-sweep.md open question 4); a plain per-force count
-- is far cheaper than that walk, so the same ceiling is generous headroom
-- here, not a tight fit.
M.max_passes = 600
function M.predict(ctx, _axis, _a) return #ctx.forces end

function M.check(a)
  if type(a.subject) ~= "string" or a.subject == "" then
    return { reason = "item_made needs a subject: name an item prototype, e.g. iron-plate" }
  end
  -- Checked here, before any force is asked: a name that is not an item
  -- would otherwise sweep every force and answer a confident zero for each,
  -- which reads as "nobody has made any" rather than "no such item".
  if not prototypes.item[a.subject] then
    return { subject = a.subject, reason = "no item prototype named " .. a.subject .. ": use the internal name, for example iron-plate" }
  end
  return nil
end

function M.read(ctx, _axis, a)
  local rows = {}
  for _, force in ipairs(ctx.forces) do
    local reply = PRODUCTION_SINCE({ force = force.name, item = a.subject, since_tick = 0 })
    if reply.found == false then
      return nil, { subject = a.subject, reason = reply.reason }
    end
    -- Every companion tool's own value may arrive as a short decimal STRING
    -- (bounded.round), and a provider's could too (registry.lua's own
    -- foreign_metric does the same tonumber before summing). production_since
    -- rounds `produced` the same way, so this is not optional here either.
    if reply.total and reply.shown and reply.shown < reply.total then
      return nil, {
        subject = a.subject, shown = reply.shown, total = reply.total,
        reason = "production_since showed " .. reply.shown .. " of " .. reply.total ..
                 " rows, so a ranking over them would name whoever survived the cut",
      }
    end
    local value = tonumber(reply.produced)
    if value then
      rows[#rows + 1] = { name = axes.force({ force = force.name }), value = value }
    end
  end
  return rows
end

return M
