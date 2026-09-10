-- The question ring (CONTEXT.md "Question"). The /ask command
-- (scripts/ask_command.lua), the chat prefix (scripts/chat.lua) and the
-- ai-agent-bridge-v1 remote interface all create one; the service drains them
-- with aab-rpc's `poll` op, marks them answered with `answer`, and reads back
-- what the player saw with `answers`. Bounded at RING_SIZE so a server nobody
-- polls for a while can't grow storage without limit.

local events        = require("scripts.events")
local player_lookup = require("scripts.player_lookup")

local RING_SIZE = 64

local M = {}

function M.ensure_storage()
  storage.aab = storage.aab or {}
  storage.aab.q = storage.aab.q or { next_id = 1, ring = {} }
end

--- Creates a question. spec = { text, player_index?, force? }. Returns the
--- new question id, or nil if spec.text is missing. This is also reachable
--- from the ai-agent-bridge-v1 remote interface by any other mod, so bad
--- input returns nil rather than error()s: a mistake in a caller's spec
--- should not crash that caller.
function M.ask(spec)
  if type(spec) ~= "table" or type(spec.text) ~= "string" or spec.text == "" then
    return nil
  end
  M.ensure_storage()
  local q = storage.aab.q
  local qid = q.next_id
  q.next_id = qid + 1
  q.ring[#q.ring + 1] = {
    id = qid,
    text = spec.text,
    player_index = spec.player_index,
    force = spec.force,
    tick = game.tick,
    answered = false,
  }
  if #q.ring > RING_SIZE then
    table.remove(q.ring, 1)
  end

  local player = player_lookup.by_index(spec.player_index)
  events.write("question", {
    qid    = qid,
    player = player and player.name or nil,
    force  = spec.force,
    text   = spec.text,
  })
  return qid
end

--- Questions with id > after, oldest first: the aab-rpc `poll` op.
function M.poll(after)
  M.ensure_storage()
  after = tonumber(after) or 0
  local out = {}
  for _, q in ipairs(storage.aab.q.ring) do
    if q.id > after then
      out[#out + 1] = { id = q.id, text = q.text, player_index = q.player_index, force = q.force, tick = q.tick }
    end
  end
  return out
end

--- Answered questions with id > after, oldest first, each with the lines the
--- asker actually saw: the aab-rpc `answers` op. Pure read.
function M.answers(after)
  M.ensure_storage()
  after = tonumber(after) or 0
  local out = {}
  for _, q in ipairs(storage.aab.q.ring) do
    if q.answered and q.id > after then
      out[#out + 1] = { id = q.id, shape = q.shape, lines = q.lines or {}, player_index = q.player_index }
    end
  end
  return out
end

--- Marks one question answered and returns it, or nil if it isn't in the
--- ring (already fell off the back, or never existed).
function M.mark_answered(qid)
  M.ensure_storage()
  for _, q in ipairs(storage.aab.q.ring) do
    if q.id == qid then
      q.answered = true
      return q
    end
  end
  return nil
end

--- Stores what the companion rendered onto the question, so the `answers` op
--- stays a pure read. Called from the `answer` op only, the one place in the
--- rpc command allowed to write storage (CONTEXT.md invariant 2).
function M.record_render(question, rendered)
  question.shape = rendered.shape
  question.lines = rendered.lines
end

function M.pending_count()
  M.ensure_storage()
  local n = 0
  for _, q in ipairs(storage.aab.q.ring) do
    if not q.answered then n = n + 1 end
  end
  return n
end

--- Takes a question from a player and tells them it landed. Shared by the
--- /ask command and the chat prefix.
function M.ask_as_player(player, text)
  local qid = M.ask({ text = text, player_index = player.index, force = player.force.name })
  if qid then
    player.print("[AI Agent Bridge] Got it, thinking about: " .. text)
  end
  return qid
end

return M
