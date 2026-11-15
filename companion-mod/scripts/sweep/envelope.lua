-- AI Agent Bridge - scripts/sweep/envelope.lua
-- Author: bits-orio
-- License: MIT
--
-- The one columnar shape every successful sweep reply takes. Fixed by the
-- phase5 implementation contract's "envelope contract" section: rows are
-- arrays positional to cols, never objects, sorted by value descending
-- before any cut, and skipped/why say what a bound left out. Refusals are
-- built by walk.lua directly (they carry no rows at all); this file only
-- shapes a metric's own {name, value} rows into the reply the model reads.

local bounded = require("scripts.tools.bounded")

-- 20 matches DEFAULT_PLAYERS and DEFAULT_FORCES elsewhere in this catalog
-- (scripts/tools/basics.lua): enough to see the shape of a small server
-- without spending the reply's byte budget on a tail nobody asked for.
-- 100 reuses bounded.MAX_FORCES, the row cap every other sweep in this
-- catalog already shares, rather than inventing a second number that could
-- drift from it.
local DEFAULT_ROWS = 20
local MAX_ROWS = bounded.MAX_FORCES

local M = {}

--- The reply for one page of rows. `why` is absent rather than a literal
--- JSON null when nothing was skipped: a Lua table cannot hold a key with a
--- nil value, and the JSON writer drops a missing key. It says "bytes" when
--- the byte bound cut deeper than the limit did, so the model knows a bigger
--- limit would not show more.
local function reply_of(head, page)
  local skipped = head.total - #page
  return {
    sweep_v = 1, axis = head.axis, metric = head.metric_name, subject = head.subject, unit = head.unit,
    cols = { "name", "value" }, rows = page,
    better = head.better,
    total = head.total, shown = #page, skipped = skipped,
    why = skipped > 0 and (#page < head.wanted and "bytes" or "limit") or nil,
  }
end

M.DEFAULT_ROWS = DEFAULT_ROWS
M.MAX_ROWS = MAX_ROWS

--- Builds the sweep_v=1 reply from one metric's own rows: { {name=, value=},
--- ... }, values still raw numbers. Sorting happens here, once, on the raw
--- number, so rounding for the wire (bounded.round, below) never touches
--- which row is the leader before a limit cuts the tail off it. Mutation-
--- tested: dropping this sort, or sorting before rounding truncated the
--- comparison to a string, both turn the "largest first" test in
--- aab_breadth_test.lua red (see that file for which).
function M.build(metric_name, axis, metric, rows, a)
  table.sort(rows, function(x, y)
    if x.value ~= y.value then return x.value > y.value end
    return x.name < y.name
  end)

  local total = #rows
  local limit = bounded.limit(a.limit, DEFAULT_ROWS, MAX_ROWS)
  local wanted = math.min(limit, total)

  local shown_rows = {}
  for i = 1, wanted do
    local row = rows[i]
    shown_rows[i] = { row.name, bounded.round(row.value, metric.places or 0) }
  end
  -- Bounded by bytes after the limit: the whole reply, header and rows, is
  -- held under bounded.REPLY_BUDGET, the service's own cut less a margin. A
  -- name on the platform axis is a whole label, ship, owner, location and
  -- gps, so the bytes decide how many of a limit of 100 go out.
  local head = { metric_name = metric_name, axis = axis, subject = a.subject, unit = metric.unit,
                 better = metric.better, total = total, wanted = wanted }
  local function build(page) return reply_of(head, page) end
  return build(bounded.fit_reply(shown_rows, build))
end

return M
