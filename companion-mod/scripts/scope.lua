-- AI Agent Bridge - scripts/scope.lua
-- Author: bits-orio
-- License: MIT
--
-- Chat scope: who may hear an answer (docs/design/phase3-spec.md, part 2).
--
-- The companion knows forces, not chat channels. It asks about every
-- question and every chat line the event file records whether any mod wants
-- to narrow who hears it: every remote interface exposing
-- `chat_scope_v1(player_index, text)` is a scope provider, found by scan the
-- same way tool providers are and never stored (CONTEXT.md invariant 3).
-- No provider, or a nil answer, means nobody knows. An /ask question is then
-- global; a chat line, and a question asked with the chat prefix, falls to
-- the engine's own force rule (scripts/chat_visibility.lua), so the service
-- can keep a private line out of any answer someone outside it will hear.
-- A provider that fails may be the one that would have said private, so a
-- chat line that meets a failure keeps a private answer and nothing less.
--
-- A key is opaque. The service compares it whole, byte for byte, as the key
-- private history is read back under, so the companion never cuts one and
-- gives no character in it a meaning. A key past KEY_LIMIT is refused
-- (read_key).
--
-- The result is fixed on the question row when the question is created.
-- What a player sees later is decided by that row, not by whatever the
-- channel has become since.

local text_clip = require("scripts.text_clip")
local richtext  = require("scripts.richtext")

local PROBE_FN = "chat_scope_v1"
local KEY_LIMIT = 64
local LABEL_LIMIT = 64
local TAG_LIMIT = 200

local M = {}

M.GLOBAL_KEY = "global"

local function global()
  return { key = M.GLOBAL_KEY, private = false }
end

--- Whether a `private` value a provider or an asking mod gave means private:
--- anything but nil or false. A 1 or a "true" is a writer's mistake, and the
--- safe side of it is private, as it is when the service reads a logged
--- flag (history's declared()). An absent value is an answer that never said
--- private, and stays public.
function M.is_private(value)
  return value ~= nil and value ~= false
end

--- A provider's string cut to `n` bytes on a character boundary, nil for
--- anything that is not a string.
local function clip(s, n)
  if type(s) ~= "string" then return nil end
  return text_clip.bytes(s, n)
end

--- A provider's tag within TAG_LIMIT bytes, nil for anything that is not a
--- string. It is printed on the answer's first line, and Factorio draws a
--- whole line raw when any tag in it is torn, so the cut never tears one.
local function clip_tag(s)
  if type(s) ~= "string" then return nil end
  return richtext.fit(s, TAG_LIMIT)
end

local function ints(list)
  if type(list) ~= "table" then return nil end
  local out = {}
  for _, v in ipairs(list) do
    if type(v) == "number" and v == math.floor(v) then out[#out + 1] = v end
  end
  if #out == 0 then return nil end
  return out
end

--- A provider's key, and true when it was refused. Cut to KEY_LIMIT bytes,
--- two keys sharing their first KEY_LIMIT bytes would become one audience
--- and read each other's private history, and a generation added at the end
--- of a long name would be the part cut away. So a longer key is refused
--- whole, and the global key stands in for it, as it does for a key that is
--- not a string at all.
local function read_key(raw_key, iface_name)
  if type(raw_key) ~= "string" then return M.GLOBAL_KEY, false end
  if #raw_key <= KEY_LIMIT then return raw_key, false end
  local why = "[ai-agent-bridge] chat scope provider %s gave a key of %d bytes, past the %d-byte limit: refused"
  log(why:format(iface_name, #raw_key, KEY_LIMIT))
  return M.GLOBAL_KEY, true
end

--- A private answer's audience, a force name or a list of player indices,
--- nil when it names neither.
local function read_audience(raw)
  local audience = type(raw.audience) == "table" and raw.audience or {}
  if type(audience.force) == "string" then return { force = audience.force } end
  local players = ints(audience.players)
  if players then return { players = players } end
  return nil
end

--- One provider's answer, reduced to the shape the row stores. A private
--- answer without a usable audience is kept private and answered to the
--- asker alone, which is the safe side of a provider's mistake. One without
--- a usable key keeps the global key, which the service never reads private
--- rows under: such a line is read back by nobody, and such a question
--- reads public rows alone. A refused key is a mistake of the same kind,
--- and the answer that carried it is private under the global key whatever
--- it said about private.
local function sanitize(raw, iface_name)
  if type(raw) ~= "table" then return nil end
  local key, refused = read_key(raw.key, iface_name)
  local scope = { key = key, private = refused or M.is_private(raw.private) }
  scope.label = clip(raw.label, LABEL_LIMIT)
  scope.tag = clip_tag(raw.tag)
  if scope.private then scope.audience = read_audience(raw) end
  return scope
end

--- Sorted names of every interface exposing the probe.
function M.provider_names()
  local names = {}
  for iface_name, functions in pairs(remote.interfaces) do
    if functions[PROBE_FN] then names[#names + 1] = iface_name end
  end
  table.sort(names)
  return names
end

--- The scope for one line as typed: the most private answer any provider
--- gives, the first interface by name breaking a tie, global when nobody
--- answers. The second result says whether any provider answered at all, so
--- a caller can tell "a provider said global" from "nobody knows". The third
--- says whether any provider failed, so a caller can tell "every provider
--- said global" from "one said global and another could not say". The
--- fourth says whether any provider is installed, so a caller can tell a
--- server with none from one whose providers could not say.
function M.lookup(player_index, text)
  local chosen, failed = global(), false
  local names = M.provider_names()
  for _, iface_name in ipairs(names) do
    local answered, raw = pcall(remote.call, iface_name, PROBE_FN, player_index, text)
    if answered then
      local scope = sanitize(raw, iface_name)
      if scope and (scope.private and not chosen.private) then
        chosen = scope
      elseif scope and scope.private == chosen.private and chosen.provider == nil then
        chosen = scope
      end
      if chosen == scope then chosen.provider = iface_name end
    else
      failed = true
      log("[ai-agent-bridge] chat scope provider " .. iface_name .. " failed: " .. tostring(raw))
    end
  end
  local answered = chosen.provider ~= nil
  chosen.provider = nil
  return chosen, answered, failed, #names > 0
end

--- The scope for one question: what lookup finds, global when nobody answers.
--- A failed provider is passed over here: a global question reads public
--- rows alone, so going global reads nothing private.
function M.resolve(player_index, text)
  return (M.lookup(player_index, text))
end

return M
