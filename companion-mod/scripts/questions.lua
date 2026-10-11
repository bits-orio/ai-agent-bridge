-- AI Agent Bridge - scripts/questions.lua
-- Author: bits-orio
-- License: MIT
--
-- The question ring (CONTEXT.md "Question"), write side. The /ask command
-- (scripts/ask_command.lua), the chat prefix (scripts/chat.lua) and the
-- ai-agent-bridge-v1 remote interface all create one; the service drains them
-- with aab-rpc's `poll` op (scripts/question_reads.lua) and marks them
-- answered with `answer`. Bounded at RING_SIZE so a server nobody polls for a
-- while can't grow storage without limit.

local events          = require("scripts.events")
local player_lookup   = require("scripts.player_lookup")
local scope           = require("scripts.scope")
local chat_visibility = require("scripts.chat_visibility")
local audience        = require("scripts.audience")
local ask_rate        = require("scripts.ask_rate")
local personality     = require("scripts.personality")
local home_force      = require("scripts.home_force")
local text_clip       = require("scripts.text_clip")

local RING_SIZE = 64

-- Longest question text kept, in bytes. Sixteen questions of this size, each
-- with a personality at its own cap, still fit one poll reply under the rpc
-- reply cap; anything longer is cut on a UTF-8 boundary rather than refused,
-- so a long-winded asker still gets an answer to the start of what they typed.
local MAX_TEXT_BYTES = 400

local M = {}

function M.ensure_storage()
  storage.aab = storage.aab or {}
  storage.aab.q = storage.aab.q or { next_id = 1, ring = {} }
end

--- The chat scope, fixed when the question is made (docs/design/phase3-spec.md
--- part 2). A caller may hand one in, a mod asking for a channel of its own.
--- A question made from a chat line takes that line's scope: with no provider
--- answering, a line on a server with two populated forces reached only the
--- speaker's force, so its question and answer stay there too. Any other
--- question from a player is resolved from the providers, and the rest are
--- global.
local function scope_of(spec, player, player_index, text)
  if type(spec.scope) == "table" then return spec.scope end
  local line = type(spec.line) == "string" and spec.line or nil
  if line and player then return chat_visibility.scope_of_line(player, line) end
  if player_index then return scope.resolve(player_index, line or text) end
  return { key = scope.GLOBAL_KEY, private = false }
end

--- Creates a question. spec = { text, player_index?, force?, viewing_force?, watching_force?, scope? }. Returns the
--- new question id, or nil if spec.text is missing. This is also reachable
--- from the ai-agent-bridge-v1 remote interface by any other mod, so bad
--- input returns nil rather than error()s: a mistake in a caller's spec
--- should not crash that caller. force and player_index are kept only when
--- they carry the right type, because a row that cannot be encoded to JSON
--- would fail every later poll, not just this caller's own call.
function M.ask(spec)
  if type(spec) ~= "table" or type(spec.text) ~= "string" or spec.text == "" then
    return nil
  end
  M.ensure_storage()
  local text = text_clip.bytes(spec.text, MAX_TEXT_BYTES)
  local player_index = type(spec.player_index) == "number" and spec.player_index or nil
  local force = type(spec.force) == "string" and spec.force or nil
  -- The force the asker is on right now when it is not the force the
  -- question is about: a member watching from a spectator force, say.
  local viewing = type(spec.viewing_force) == "string" and spec.viewing_force ~= force
    and spec.viewing_force or nil
  -- The force whose base the asker is looking at, when not their own.
  local watching = type(spec.watching_force) == "string" and spec.watching_force ~= force
    and spec.watching_force or nil
  -- Looked up once, here, and stored: the asker may have left by the time the
  -- service polls, and history is keyed by player name rather than by index.
  local player = player_lookup.by_index(player_index)
  local player_name = player and player.name or nil
  -- Where the asker is, fixed now: a "where is my X" question is about the
  -- surface they are looking at far more often than not, and the model is
  -- told so. LuaControl::surface follows the controller, so in remote view
  -- it is the viewed surface; LuaPlayer::physical_surface is where the
  -- character stands, kept when it differs.
  local player_surface = player and player.surface and player.surface.valid and player.surface.name or nil
  local physical = player and player.physical_surface and player.physical_surface.valid
    and player.physical_surface.name or nil
  if physical == player_surface then physical = nil end
  local sc = scope_of(spec, player, player_index, text)
  local key = type(sc.key) == "string" and sc.key or scope.GLOBAL_KEY
  -- Read the way a provider's answer is: a mod handing in private = 1 asks
  -- privately, it does not open the question to everyone.
  local private = scope.is_private(sc.private)

  local q = storage.aab.q
  local qid = q.next_id
  q.next_id = qid + 1
  q.ring[#q.ring + 1] = {
    id = qid,
    text = text,
    player_index = player_index,
    player_name = player_name,
    surface = player_surface,
    physical_surface = physical,
    force = force,
    viewing_force = viewing,
    watching_force = watching,
    tick = game.tick,
    answered = false,
    scope = key,
    private = private,
    audience = private and type(sc.audience) == "table" and sc.audience or nil,
    tag = type(sc.tag) == "string" and sc.tag or nil,
    -- The voice the answer is written in, as the setting stood when the
    -- question was asked, so changing it never re-voices one in flight.
    personality = personality.for_question(),
  }
  if #q.ring > RING_SIZE then
    table.remove(q.ring, 1)
  end

  -- The text as kept, the same cut the poll row carries, so history holds
  -- the question the service answered and never more of it.
  events.write("question", {
    qid = qid, player = player_name, force = force, text = text,
    scope = key, private = private,
  })
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

--- Takes a question from a player and shows it to its audience. Shared by
--- the /ask command and the chat prefix. `line` is what the player typed
--- before any prefix was stripped, handed to the scope providers as is, and
--- its presence says the question was already visible as chat.
function M.ask_as_player(player, text, line)
  local refused = ask_rate.check(player)
  if refused then
    player.print("[AI Agent Bridge] " .. refused)
    return nil
  end
  -- The force a home force provider names, when one does: a member who
  -- spectates sits on another force, and "our team" still means theirs.
  local force, viewing, watching = home_force.resolve(player)
  local qid = M.ask({ text = text, player_index = player.index, force = force, viewing_force = viewing,
                      watching_force = watching, line = line })
  if qid and not line then
    -- A /ask command is not a chat line, so nobody else saw it. Echo it to
    -- the same audience the answer will reach, or a session others may
    -- follow up on would start with an answer to a question they never saw.
    -- A chat-prefix question is its own echo: the chat line was visible.
    local question = M.find(qid)
    audience.deliver(question, audience.head(question) .. " " .. player.name .. " asked: " .. text)
  end
  return qid
end

return M
