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

--- REPLY_BUDGET is what one whole reply may encode to, rows and the fields
--- around them. The cap that governs is not rpc.lua's CAPS.call = 8000 but
--- the service's: it hands the model at most max_tool_result_bytes of a
--- result, 4096 by default (service/internal/agent/agent.go), and cuts the
--- rest mid-JSON. 3800 leaves 296 bytes under that cut for what the service
--- itself adds or re-escapes: Go's encoder writes <, > and & as six-byte
--- \u escapes, and a player-named platform or force can carry them.
M.REPLY_BUDGET = 3800

--- ROW_BUDGET is what the rows alone may encode to, for the one caller that
--- fits rows without building its reply (fit below): REPLY_BUDGET less 500
--- for the fields a reply wraps around them.
M.ROW_BUDGET = M.REPLY_BUDGET - 500

--- The first rows that fit a byte budget, for a reply whose rows are wide
--- enough that a row count is the wrong unit to bound them in.
---
--- M.cut alone bounds rows by COUNT, which was right while a row was
--- {force, count} and became wrong the moment a row could carry a platform,
--- its owner, its location and its state: 100 such rows encode past the
--- 8000-byte call cap, and an over-cap reply is refused whole, so the tool
--- spends every pass and then answers nothing the model can act on. Measured:
--- entity_count per_surface died at 32 surfaces and list_surfaces reached 7794
--- bytes at its own default row count.
---
--- Binary search rather than dropping a row at a time, so a wide reply costs
--- about seven encodes instead of one per row discarded.
--- The largest n in 0..most for which fits(n) holds, fits growing false as n
--- grows. Binary search, so a wide reply costs about seven encodes.
local function largest(most, fits)
  if most == 0 or fits(most) then return most end
  local low, high = 0, most - 1
  while low < high do
    local mid = math.floor((low + high + 1) / 2)
    if fits(mid) then low = mid else high = mid - 1 end
  end
  return low
end

function M.fit(rows, budget)
  budget = budget or M.ROW_BUDGET
  local n = largest(#rows, function(n) return #helpers.table_to_json(M.cut(rows, n)) <= budget end)
  if n == #rows then return rows end
  return M.cut(rows, n)
end

--- The offset param every paged tool declares, one wording for all of them.
M.OFFSET_PARAM = "integer rows to skip, default 0; pass a cut reply's next_offset for the next page"

--- A caller's offset: a whole number from 0, 0 when missing or not a number.
function M.offset(value)
  local n = math.floor(tonumber(value) or 0)
  if n < 0 then n = 0 end
  return n
end

--- Stamps total and shown on a reply, and offset and next_offset when the
--- page is not the whole list, so a cut reply says where the next page
--- starts. An uncut first page carries neither, as before paging existed.
local function stamp(reply, total, offset, shown)
  reply.total, reply.shown = total, shown
  if offset > 0 or offset + shown < total then
    reply.offset = offset
    if offset + shown < total then reply.next_offset = offset + shown end
  end
  return reply
end

--- One page of `rows`, already sorted: from `offset` (0 based), at most
--- `max` rows, and no more than the whole reply fits in REPLY_BUDGET.
--- `build(page)` returns the reply around the page's rows and must not
--- change them; this adds total, shown, offset and next_offset. Measuring
--- the finished reply, not the rows alone, is what keeps the fields around
--- the rows inside the budget too.
function M.page(rows, offset, build, max)
  offset = M.offset(offset)
  local rest = {}
  for i = offset + 1, math.min(#rows, offset + (max or #rows)) do rest[#rest + 1] = rows[i] end
  local function reply_for(n) return stamp(build(M.cut(rest, n)), #rows, offset, n) end
  local budget = M.REPLY_BUDGET
  return reply_for(largest(#rest, function(n) return #helpers.table_to_json(reply_for(n)) <= budget end))
end

--- How many of `rows` fit when `build(first_n)` must encode within
--- REPLY_BUDGET: for a reply that is cut but not paged.
function M.fit_reply(rows, build)
  local budget = M.REPLY_BUDGET
  local n = largest(#rows, function(n) return #helpers.table_to_json(build(M.cut(rows, n))) <= budget end)
  return M.cut(rows, n)
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
