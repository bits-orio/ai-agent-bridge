-- AI Agent Bridge - scripts/rpc_selftest.lua
-- Author: bits-orio
-- License: MIT
--
-- Phase 0 diagnostic ops for aab-rpc (PLAN.md "Phase 0, checks"): the
-- transport assumptions the whole design rests on. Kept apart from rpc.lua
-- so the frozen Phase 1 protocol ops stay easy to read on their own; merged
-- into rpc.lua's op table at load time.

-- These ops are for the test rigs. The startup setting aab-selftest-ops
-- (default off) turns them on; off, every one answers bad_op as an unknown
-- op does, and the helper interface below is not even registered, so a real
-- server carries none of this surface.
local function enabled()
  local setting = settings.startup["aab-selftest-ops"]
  return setting ~= nil and setting.value == true
end

-- A tiny provider whose only job is proving pcall(remote.call, ...) catches
-- an error thrown inside a provider's own function. Registered every load,
-- like every other remote interface in this mod, but only when enabled.
if enabled() then
  remote.add_interface("ai-agent-bridge-selftest", {
    boom = function() error("boom") end,
  })
end

local MAX_KB = 4096

local function ok_reply(r) return { ok = true, r = r } end

--- `fn` as an op that refuses with bad_op while the diagnostic ops are off.
local function gated(name, fn)
  return function(req, cmd)
    if not enabled() then return { ok = false, e = "bad_op", m = name } end
    return fn(req, cmd)
  end
end

local M = { ops = {} }

--- Is player_index nil for RCON-invoked commands? (Documented only for the
--- server console, so it is verified here rather than assumed.)
function M.ops.ping(_req, cmd)
  return ok_reply({
    player_index = cmd.player_index,
    tick         = cmd.tick,
    has_player   = cmd.player_index ~= nil,
  })
end

--- Prints a JSON string of req.kb kilobytes so the reply size at which RCON
--- truncates can be measured from outside. Deliberately NOT subject to
--- rpc.lua's per-op reply cap (big is never capped), so it carries
--- its own ceiling: a command runs on every peer, and a mistyped kb would ask
--- the server and every client to allocate the same absurd string at once.
--- MAX_KB is 4096, the 4 MB reply that measurement reached (TESTING.md 1.5).
function M.ops.big(req, _cmd)
  local kb = tonumber(req.kb) or 1
  if kb < 0 then kb = 0 end
  if kb > MAX_KB then kb = MAX_KB end
  local payload = string.rep("x", math.floor(kb * 1024))
  return ok_reply({ kb = kb, bytes = #payload, payload = payload })
end

--- Does a storage write inside an RCON command replicate to every connected
--- client? Increments a counter so a second client can be watched for it.
--- It raises no on_answer: a subscriber, a relay for one, would take the
--- fake answer for a real one.
function M.ops.write(_req, _cmd)
  storage.aab = storage.aab or {}
  storage.aab.test_counter = (storage.aab.test_counter or 0) + 1
  return ok_reply({ counter = storage.aab.test_counter })
end

--- Does pcall(remote.call, ...) catch an error thrown inside the callee?
function M.ops.pcall_test(_req, _cmd)
  local ran_ok, result = pcall(remote.call, "ai-agent-bridge-selftest", "boom")
  return ok_reply({ caught = not ran_ok, message = tostring(result) })
end

for name, fn in pairs(M.ops) do M.ops[name] = gated(name, fn) end

return M
