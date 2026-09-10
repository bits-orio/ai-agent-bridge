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

return M
