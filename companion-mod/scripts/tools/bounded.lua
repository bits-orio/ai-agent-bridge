-- Shared row bounds for the enumerating tools. Every tool that lists things
-- has to say how many it will return and stop there: the rpc reply is refused
-- whole once it passes the byte cap (CONTEXT.md invariant 4), so an unbounded
-- list is a tool that works on a fresh map and answers too_large on a server
-- that has been up a month.
--
-- The pattern every caller follows: collect the rows, sort them, cut to
-- limit(), and report `total` beside `shown` so the agent can say "200 known,
-- 12 online" instead of believing it saw everything.

local M = {}

--- A caller's limit, clamped to 1..max, `default` when it is missing or not a
--- number.
function M.limit(value, default, max)
  local n = math.floor(tonumber(value) or default)
  if n < 1 then n = 1 end
  if n > max then n = max end
  return n
end

--- The first n rows, in the order they are already in. Sort before calling, so
--- which rows survive a cut is stable rather than whatever pairs() produced.
function M.cut(rows, n)
  local out = {}
  for i = 1, math.min(n, #rows) do out[i] = rows[i] end
  return out
end

--- Sorts rows by their `name` field, in place, and returns them.
function M.by_name(rows)
  table.sort(rows, function(x, y) return x.name < y.name end)
  return rows
end

--- The one rounding helper every double in a reply passes through: four
--- decimals for an evolution factor, two for a progress fraction, a rate or an
--- hour count.
---
--- Why it exists at all: helpers.table_to_json writes a double at full
--- round-trip precision, so an evolution factor of 0.31 reaches the model as
--- 0.3100000000000000088817841970012523233890533447265625. Fifty-odd bytes of
--- the reply's budget spent on digits that carry no information, and a figure
--- the model may quote back at a player verbatim.
---
--- Anything that is not a number comes back untouched, so an absent progress
--- stays absent rather than becoming 0.
function M.round(value, places)
  if type(value) ~= "number" then return value end
  local scale = 10 ^ (places or 2)
  if value < 0 then return -math.floor(-value * scale + 0.5) / scale end
  return math.floor(value * scale + 0.5) / scale
end

return M
