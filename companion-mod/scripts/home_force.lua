-- AI Agent Bridge - scripts/home_force.lua
-- Author: bits-orio
-- License: MIT
--
-- Home force: the force a player's questions are about. A mod may move a
-- player onto another force for a while, a spectator force or a lobby, and
-- "how is our research going?" asked from there is about the force the
-- player belongs to, not the one they are watching from. The engine only
-- knows the second. Any remote interface exposing
-- `home_force_v1(player_index)` returning a force name is a home force
-- provider, found by scan like every other probe and never stored. Nil, an
-- error, or a name no force has means "do not know", and the player's own
-- force stands.
--
-- Watching force: the force whose base a player is looking at, from any
-- interface exposing `watching_force_v1(player_index)`, nil when they watch
-- nobody's. Asked "are we making repair packs here?" while watching another
-- team's surface, the model searched the asker's own machines, found none,
-- and said nothing on that surface made any, when the watched team did.

local PROBE_FN = "home_force_v1"
local WATCH_FN = "watching_force_v1"

local M = {}

--- Sorted names of every interface exposing `fn`.
local function providers(fn)
  local names = {}
  for iface_name, functions in pairs(remote.interfaces) do
    if functions[fn] then names[#names + 1] = iface_name end
  end
  table.sort(names)
  return names
end

function M.provider_names() return providers(PROBE_FN) end

--- The first force a provider of `fn` names for the player, by interface
--- name, that the game has; nil when none does.
local function ask(fn, player)
  if not (player and player.valid) then return nil end
  for _, iface_name in ipairs(providers(fn)) do
    local ok, name = pcall(remote.call, iface_name, fn, player.index)
    if not ok then
      log("[ai-agent-bridge] " .. fn .. " provider " .. iface_name .. " failed: " .. tostring(name))
    elseif type(name) == "string" and game.forces[name] then
      return name
    end
  end
  return nil
end

--- The player's home force name, nil when no provider names one.
function M.of(player) return ask(PROBE_FN, player) end

--- The force whose base the player is watching, nil when nobody's.
function M.watching(player) return ask(WATCH_FN, player) end

--- The force a player's question is about; the force they are on now when
--- that is a different one; and the force they are watching when that is
--- not the first. Nil for either when it adds nothing.
function M.resolve(player)
  local current = player.force.name
  local force = M.of(player) or current
  local watched = M.watching(player)
  if watched == force then watched = nil end
  return force, force ~= current and current or nil, watched
end

return M
