-- AI Agent Bridge - scripts/ask_rate.lua
-- Author: bits-orio
-- License: MIT
--
-- Rate limits on asking, at the companion, before anything else happens:
-- before the echo to the audience, before the ring, before the service
-- pays for a model call. The service has its own per-player quota for
-- cost; this one protects the game and the chat from a held-down key.
--
-- Two limits, both runtime-global settings:
--   aab-ask-cooldown-seconds: one player may not ask again within this
--     many seconds (default 5).
--   aab-asks-per-minute: the whole server may not ask more than this many
--     times in one minute (default 30). Chat is shared, so the flood one
--     player can cause is bounded here whatever the player count.
-- A refused ask costs its asker one private line and nothing else.
--
-- A third, separate bucket guards the ai-agent-bridge-v1 ask() a mod calls
-- (allow_remote): no player is behind it, so the per-player cooldown has
-- nothing to hold, and a loop in another mod must not fill the ring.

local M = {}

local TICKS_PER_SECOND = 60
local TICKS_PER_MINUTE = 60 * TICKS_PER_SECOND

-- Remote asks the whole server may make in one minute. Generous on purpose:
-- it is a flood guard for a broken caller, not a policy.
local REMOTE_PER_MINUTE = 60

local function setting(name, default)
  local s = settings.global[name]
  local v = s and s.value
  if type(v) ~= "number" then return default end
  return v
end

local function state()
  storage.aab = storage.aab or {}
  storage.aab.rate = storage.aab.rate or { last = {}, minute = 0, count = 0 }
  local r = storage.aab.rate
  -- A save from before the remote bucket has no fields for it.
  r.remote_minute = r.remote_minute or 0
  r.remote_count = r.remote_count or 0
  return r
end

--- Drops every player whose cooldown has run out: they would pass the check
--- anyway, so the entry is only weight. Setting a field to nil while pairs
--- walks the table is allowed, and what is left does not depend on the order.
local function prune(r, now, cooldown)
  for index, at in pairs(r.last) do
    if cooldown <= 0 or now - at >= cooldown then r.last[index] = nil end
  end
end

--- nil when this player may ask now, else the sentence to tell them. On
--- nil the ask is recorded.
function M.check(player)
  local r = state()
  local now = game.tick

  local cooldown = setting("aab-ask-cooldown-seconds", 5) * TICKS_PER_SECOND
  prune(r, now, cooldown)
  local last = r.last[player.index]
  if cooldown > 0 and last and now - last < cooldown then
    local wait = math.ceil((cooldown - (now - last)) / TICKS_PER_SECOND)
    return "Please wait " .. wait .. "s before asking again."
  end

  local per_minute = setting("aab-asks-per-minute", 30)
  local minute = math.floor(now / TICKS_PER_MINUTE)
  if minute ~= r.minute then
    r.minute, r.count = minute, 0
  end
  if per_minute > 0 and r.count >= per_minute then
    return "The server has asked as much as it can this minute. Try again shortly."
  end

  r.last[player.index] = now
  r.count = r.count + 1
  return nil
end

--- Whether a mod's ask() may go through now, counting it when it may. When
--- it may not, says so in the log, once a minute at most.
function M.allow_remote()
  local r = state()
  local minute = math.floor(game.tick / TICKS_PER_MINUTE)
  if minute ~= r.remote_minute then
    r.remote_minute, r.remote_count, r.remote_logged = minute, 0, nil
  end
  if r.remote_count >= REMOTE_PER_MINUTE then
    if not r.remote_logged then
      r.remote_logged = true
      log("[ai-agent-bridge] remote ask refused: more than " .. REMOTE_PER_MINUTE
        .. " in one minute from other mods")
    end
    return false
  end
  r.remote_count = r.remote_count + 1
  return true
end

--- Forgets a player the game removed. Not one who merely left: their
--- cooldown must outlast a leave and rejoin, or leaving would be a way past
--- it. A removed player's index is gone for good, and pruning (above) keeps
--- the table small for everyone else.
function M.forget(player_index)
  state().last[player_index] = nil
end

return M
