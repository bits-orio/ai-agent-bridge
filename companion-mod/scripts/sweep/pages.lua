-- AI Agent Bridge - scripts/sweep/pages.lua
-- Author: bits-orio
-- License: MIT
--
-- Every row of a paged all=true delegate, read page by page. The tools cut
-- their replies to the service's byte budget and say where the next page
-- starts (next_offset, scripts/tools/bounded.lua page), so a metric that
-- ranked the first page alone would name whoever survived the cut. Reading
-- on until there is no next page gives the ranking every row, and the sweep
-- envelope then fits its own reply.
--
-- Each page re-runs the delegate, so MAX_PAGES bounds what one sweep may
-- spend: 40 pages is several thousand forces at the row sizes the tools
-- send, far past any server measured.

local MAX_PAGES = 40

local M = {}

local function cut_refusal(label, shown, total)
  return {
    shown = shown, total = total,
    reason = label .. " showed " .. shown .. " of " .. total ..
             " rows, so a ranking over them would name whoever survived the cut",
  }
end

--- The rows under `key` across every page `fn(args)` answers, or nil and a
--- refusal: a found=false reply passed on, or a cut no page reaches past.
--- `args` gains offset on each call.
function M.collect(fn, args, key, label)
  local rows, offset, seen = {}, 0, 0
  for _ = 1, MAX_PAGES do
    args.offset = offset
    local reply = fn(args)
    if reply.found == false then
      return nil, { reason = reply.reason, passes = reply.passes, max_passes = reply.max_passes }
    end
    local page = reply[key] or {}
    for _, row in ipairs(page) do rows[#rows + 1] = row end
    -- What the delegate says it showed, which a delegate that does not page
    -- may report apart from the rows it sent.
    seen = seen + (tonumber(reply.shown) or #page)
    if not reply.next_offset then
      if reply.total and seen < reply.total then return nil, cut_refusal(label, seen, reply.total) end
      return rows
    end
    offset = reply.next_offset
  end
  return nil, cut_refusal(label, seen, seen + 1)
end

M.MAX_PAGES = MAX_PAGES

return M
