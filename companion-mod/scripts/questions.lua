-- The question ring (CONTEXT.md "Question"), write side. The /ask command
-- (scripts/ask_command.lua), the chat prefix (scripts/chat.lua) and the
-- ai-agent-bridge-v1 remote interface all create one; the service drains them
-- with aab-rpc's `poll` op (scripts/question_reads.lua) and marks them
-- answered with `answer`. Bounded at RING_SIZE so a server nobody polls for a
-- while can't grow storage without limit.

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
--- should not crash that caller. force and player_index are kept only when
--- they carry the right type, because a row that cannot be encoded to JSON
--- would fail every later poll, not just this caller's own call.
-- Longest question text kept, in bytes. Sixteen questions of this size still
-- fit one poll reply under the rpc reply cap; anything longer is cut on a
-- UTF-8 boundary rather than refused, so a long-winded asker still gets an
-- answer to the start of what they typed.
local MAX_TEXT_BYTES = 400

local function clip_utf8(s, n)
  if #s <= n then return s end
  local cut = n
  -- Step back over continuation bytes (10xxxxxx) so the cut never lands
  -- inside a multi-byte character.
  while cut > 1 and s:byte(cut + 1) and s:byte(cut + 1) >= 0x80 and s:byte(cut + 1) < 0xC0 do
    cut = cut - 1
  end
  return s:sub(1, cut)
end

function M.ask(spec)
  if type(spec) ~= "table" or type(spec.text) ~= "string" or spec.text == "" then
    return nil
  end
  M.ensure_storage()
  local text = clip_utf8(spec.text, MAX_TEXT_BYTES)
  local player_index = type(spec.player_index) == "number" and spec.player_index or nil
  local force = type(spec.force) == "string" and spec.force or nil
  -- Looked up once, here, and stored: the asker may have left by the time the
  -- service polls, and history is keyed by player name rather than by index.
  local player = player_lookup.by_index(player_index)
  local player_name = player and player.name or nil

  local q = storage.aab.q
  local qid = q.next_id
  q.next_id = qid + 1
  q.ring[#q.ring + 1] = {
    id = qid,
    text = text,
    player_index = player_index,
    player_name = player_name,
    force = force,
    tick = game.tick,
    answered = false,
  }
  if #q.ring > RING_SIZE then
    table.remove(q.ring, 1)
  end

  events.write("question", { qid = qid, player = player_name, force = force, text = spec.text })
  return qid
end

--- The question with this id, or nil if it isn't in the ring (already fell
--- off the back, or never existed). A pure read.
function M.find(qid)
  M.ensure_storage()
  for _, q in ipairs(storage.aab.q.ring) do
    if q.id == qid then return q end
  end
  return nil
end

--- Marks one question answered and stores what the companion rendered on it,
--- so the `answers` op stays a pure read. Called from the `answer` op only,
--- after rendering succeeded: the one place in the rpc command allowed to
--- write storage (CONTEXT.md invariant 2).
function M.record_answer(question, rendered)
  question.answered = true
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

--- The highest question id issued so far, 0 before the first question. The
--- `status` op reports it so a service that restarts can tell at a glance how
--- far the game has got.
function M.last_id()
  M.ensure_storage()
  return storage.aab.q.next_id - 1
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
