-- The player-facing question ring (CONTEXT.md "Question"). /ask (or
-- /aab-ask, see register_command below) creates one; the service drains
-- them with aab-rpc's `poll` op and marks them answered with `answer`.
-- Bounded at RING_SIZE so a server nobody polls for a while can't grow
-- storage without limit.

local RING_SIZE = 64

local active_command -- module-local: which command name is live this session

local M = {}

function M.ensure_storage()
  storage.aab = storage.aab or {}
  storage.aab.q = storage.aab.q or { next_id = 1, ring = {} }
end

--- Creates a question. spec = { text, player_index?, force? }. Returns the
--- new question id, or nil if spec.text is missing. This is also reachable
--- from the ai-agent-bridge-v1 remote interface by any other mod, so bad
--- input returns nil rather than error()s -- a mistake in a caller's spec
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
  return qid
end

--- Questions with id > after, oldest first -- the aab-rpc `poll` op.
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

function M.pending_count()
  M.ensure_storage()
  local n = 0
  for _, q in ipairs(storage.aab.q.ring) do
    if not q.answered then n = n + 1 end
  end
  return n
end

function M.active_command()
  return active_command
end

local function handle_ask(cmd)
  local player = cmd.player_index and game.get_player(cmd.player_index)
  if not player then return end -- player-only; RCON callers use the ai-agent-bridge-v1 ask() function instead
  if not cmd.parameter or cmd.parameter == "" then
    player.print("[AI Agent Bridge] Usage: /" .. active_command .. " <question>")
    return
  end
  local qid = M.ask({ text = cmd.parameter, player_index = cmd.player_index, force = player.force.name })
  if qid then
    player.print("[AI Agent Bridge] Got it, thinking about: " .. cmd.parameter)
  end
end

--- Registers the player command every load. /ask is the preferred name; if
--- another mod already owns it this falls back to /aab-ask and remembers
--- which one is live so the aab-rpc `status` op can report it.
function M.register_command()
  local added = pcall(commands.add_command, "ask", "Ask the AI Agent Bridge a question about this game.", handle_ask)
  if added then
    active_command = "ask"
  else
    commands.add_command("aab-ask", "Ask the AI Agent Bridge a question about this game.", handle_ask)
    active_command = "aab-ask"
  end
end

return M
