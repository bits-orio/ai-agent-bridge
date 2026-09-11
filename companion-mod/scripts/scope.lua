-- AI Agent Bridge - scripts/scope.lua
-- Author: bits-orio
-- License: MIT
--
-- Chat scope: who may hear an answer (docs/design/phase3-spec.md, part 2).
--
-- The companion never knows who is on which team. It asks, once per
-- question, whether any mod wants to narrow the audience: every remote
-- interface exposing `chat_scope_v1(player_index, text)` is a scope
-- provider, found by scan the same way tool providers are and never stored
-- (CONTEXT.md invariant 3). No provider, or a nil answer, means global.
--
-- The result is fixed on the question row when the question is created.
-- What a player sees later is decided by that row, not by whatever the
-- channel has become since.

local PROBE_FN = "chat_scope_v1"
local NAME_LIMIT = 64

local M = {}

M.GLOBAL_KEY = "global"

local function global()
  return { key = M.GLOBAL_KEY, private = false }
end

local function clip(s, n)
  if type(s) ~= "string" then return nil end
  if #s > n then return s:sub(1, n) end
  return s
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

--- One provider's answer, reduced to the shape the row stores. A private
--- answer without a usable audience is kept private and answered to the
--- asker alone, which is the safe side of a provider's mistake.
local function sanitize(raw)
  if type(raw) ~= "table" then return nil end
  local scope = global()
  scope.key = clip(raw.key, NAME_LIMIT) or M.GLOBAL_KEY
  scope.private = raw.private == true
  scope.label = clip(raw.label, NAME_LIMIT)
  scope.tag = clip(raw.tag, 200)
  if scope.private then
    local audience = type(raw.audience) == "table" and raw.audience or {}
    if type(audience.force) == "string" then
      scope.audience = { force = audience.force }
    elseif ints(audience.players) then
      scope.audience = { players = ints(audience.players) }
    end
  end
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

--- The scope for one question: the most private answer any provider gives,
--- the first interface by name breaking a tie, global when nobody answers.
function M.resolve(player_index, text)
  local chosen = global()
  for _, iface_name in ipairs(M.provider_names()) do
    local answered, raw = pcall(remote.call, iface_name, PROBE_FN, player_index, text)
    if answered then
      local scope = sanitize(raw)
      if scope and (scope.private and not chosen.private) then
        chosen = scope
      elseif scope and scope.private == chosen.private and chosen.provider == nil then
        chosen = scope
      end
      if chosen == scope then chosen.provider = iface_name end
    else
      log("[ai-agent-bridge] chat scope provider " .. iface_name .. " failed: " .. tostring(raw))
    end
  end
  chosen.provider = nil
  return chosen
end

return M
