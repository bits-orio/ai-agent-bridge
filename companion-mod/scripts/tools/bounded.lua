-- AI Agent Bridge - scripts/tools/bounded.lua
-- Author: bits-orio
-- License: MIT
--
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

--- The row cap every force sweep shares. A scenario mod can run dozens of
--- forces, so a sweep that answers for all of them still has to stop
--- somewhere, and it must stop at the same place in every tool.
M.MAX_FORCES = 100

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
---
--- Rounding the double is not enough: the engine's JSON writer prints any
--- non-integer double at full precision, so a rounded 0.79 still leaves as
--- 0.79000000000000003552713678800500929355621337890625 (measured on 2.0.77).
--- A whole number leaves as a number; anything else leaves as the short
--- decimal string, "1.79", which a model reads as the figure it is.
function M.round(value, places)
  if type(value) ~= "number" then return value end
  places = places or 2
  local scale = 10 ^ places
  local rounded
  if value < 0 then rounded = -math.floor(-value * scale + 0.5) / scale
  else rounded = math.floor(value * scale + 0.5) / scale end
  if rounded == math.floor(rounded) then return math.floor(rounded) end
  local text = string.format("%." .. places .. "f", rounded)
  return (text:gsub("0+$", ""))
end

return M
