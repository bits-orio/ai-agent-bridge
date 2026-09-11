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

local M = {}

local TICKS_PER_SECOND = 60
local TICKS_PER_MINUTE = 60 * TICKS_PER_SECOND

local function setting(name, default)
  local s = settings.global[name]
  local v = s and s.value
  if type(v) ~= "number" then return default end
  return v
end

local function state()
  storage.aab = storage.aab or {}
  storage.aab.rate = storage.aab.rate or { last = {}, minute = 0, count = 0 }
  return storage.aab.rate
end

--- nil when this player may ask now, else the sentence to tell them. On
--- nil the ask is recorded.
function M.check(player)
  local r = state()
  local now = game.tick

  local cooldown = setting("aab-ask-cooldown-seconds", 5) * TICKS_PER_SECOND
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

--- Forgets a player who left, so the table never grows past the players
--- who are around.
function M.forget(player_index)
  state().last[player_index] = nil
end

return M
