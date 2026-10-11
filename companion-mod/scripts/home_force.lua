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

local PROBE_FN = "home_force_v1"

local M = {}

--- Sorted names of every interface exposing the probe.
function M.provider_names()
  local names = {}
  for iface_name, functions in pairs(remote.interfaces) do
    if functions[PROBE_FN] then names[#names + 1] = iface_name end
  end
  table.sort(names)
  return names
end

--- The player's home force name: the first provider by interface name that
--- names a force the game has, nil when none does.
function M.of(player)
  if not (player and player.valid) then return nil end
  for _, iface_name in ipairs(M.provider_names()) do
    local ok, name = pcall(remote.call, iface_name, PROBE_FN, player.index)
    if not ok then
      log("[ai-agent-bridge] home force provider " .. iface_name .. " failed: " .. tostring(name))
    elseif type(name) == "string" and game.forces[name] then
      return name
    end
  end
  return nil
end

--- The force a player's question is about, and the force they are on now
--- when that is a different one (nil otherwise).
function M.resolve(player)
  local current = player.force.name
  local home = M.of(player)
  if home and home ~= current then return home, current end
  return current, nil
end

return M
