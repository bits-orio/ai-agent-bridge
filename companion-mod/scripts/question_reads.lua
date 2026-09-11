-- The read side of the question ring: the `poll` and `answers` ops. Both are
-- pure reads, both take a cursor and a bounded row count, and both walk the
-- ring oldest first so a caller can page through it with the id of the last
-- row it saw.
--
-- The row count matters for more than tidiness. A reply over the rpc
-- command's byte cap is refused whole (CONTEXT.md invariant 4), so an
-- unbounded poll on a server with a backlog would answer `too_large` every
-- time and the service would never make progress.

local questions = require("scripts.questions")

local DEFAULT_LIMIT = 16
local MAX_LIMIT = 64

local M = {}

--- A caller's limit, clamped to 1..MAX_LIMIT, DEFAULT_LIMIT when absent.
local function row_limit(limit)
  local n = math.floor(tonumber(limit) or DEFAULT_LIMIT)
  if n < 1 then n = 1 end
  if n > MAX_LIMIT then n = MAX_LIMIT end
  return n
end

--- Rows for the first `limit` questions with id > after that pass `wanted`,
--- oldest first. The ring is already in id order, so one pass is enough.
local function page(after, limit, wanted, row_of)
  questions.ensure_storage()
  after = tonumber(after) or 0
  local max_rows = row_limit(limit)
  local out = {}
  for _, q in ipairs(storage.aab.q.ring) do
    if q.id > after and wanted(q) then
      out[#out + 1] = row_of(q)
      if #out >= max_rows then break end
    end
  end
  return out
end

--- Unanswered questions with id > after, oldest first: the aab-rpc `poll` op.
--- Answered ones are skipped, so a cursor of 0 means "everything still
--- waiting" and a service that restarts never re-answers what it already did.
function M.poll(after, limit)
  return page(after, limit,
    function(q) return not q.answered end,
    function(q)
      return {
        id = q.id, text = q.text, player_index = q.player_index,
        player_name = q.player_name, force = q.force, tick = q.tick,
        scope = q.scope, private = q.private or nil, surface = q.surface,
        physical_surface = q.physical_surface,
      }
    end)
end

--- Answered questions with id > after, oldest first, each with the lines the
--- asker actually saw: the aab-rpc `answers` op.
function M.answers(after, limit)
  return page(after, limit,
    function(q) return q.answered end,
    function(q)
      return { id = q.id, shape = q.shape, lines = q.lines or {}, player_index = q.player_index }
    end)
end

return M
