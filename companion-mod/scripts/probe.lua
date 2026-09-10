-- Scans remote.interfaces for every provider's agent_tools_v1 probe
-- (CONTEXT.md "Probe", "Catalog") and routes the aab-rpc `call` op to one
-- tool on one provider. Nothing here is ever stored -- the catalog is
-- rebuilt from scratch on every use, sorted by interface name so its JSON
-- stays byte-stable, and a removed provider just vanishes (CONTEXT.md
-- invariant 3).

local PROBE_FN = "agent_tools_v1"

local M = {}

--- Sorted list of {iface, v, tools} for every provider found right now.
function M.catalog()
  local names = {}
  for iface_name in pairs(remote.interfaces) do
    names[#names + 1] = iface_name
  end
  table.sort(names)

  local providers = {}
  for _, iface_name in ipairs(names) do
    if remote.interfaces[iface_name][PROBE_FN] then
      local ok, manifest = pcall(remote.call, iface_name, PROBE_FN)
      if ok and type(manifest) == "table" and type(manifest.tools) == "table" then
        providers[#providers + 1] = { iface = iface_name, v = manifest.v or 1, tools = manifest.tools }
      elseif not ok then
        log("[ai-agent-bridge] probe: " .. iface_name .. "." .. PROBE_FN .. "() errored: " .. tostring(manifest))
      end
      -- ok but no `tools` table: not a valid manifest, silently skipped.
    end
  end
  return providers
end

--- Calls one tool on one provider. Always re-probes fresh; never trusts a
--- prior catalog (CONTEXT.md invariant 3). Returns the reply shape the
--- aab-rpc-v1 wire protocol uses directly, so rpc.lua's `call` op can hand
--- this straight back: {ok=true, r=...} or {ok=false, e=..., m=...}.
function M.call(iface_name, fn_name, args)
  if type(iface_name) ~= "string" or iface_name == "" then
    return { ok = false, e = "no_provider", m = "missing interface name" }
  end
  if type(fn_name) ~= "string" or fn_name == "" then
    return { ok = false, e = "no_tool", m = "missing function name" }
  end

  local iface_functions = remote.interfaces[iface_name]
  if not iface_functions or not iface_functions[PROBE_FN] then
    return { ok = false, e = "no_provider", m = "no such provider: " .. iface_name }
  end

  local probed, manifest = pcall(remote.call, iface_name, PROBE_FN)
  if not probed or type(manifest) ~= "table" or type(manifest.tools) ~= "table" then
    return { ok = false, e = "no_provider", m = "provider manifest invalid: " .. iface_name }
  end
  if not manifest.tools[fn_name] then
    return { ok = false, e = "no_tool", m = "provider has no tool named: " .. fn_name }
  end
  if not iface_functions[fn_name] then
    return { ok = false, e = "no_tool", m = "interface has no function named: " .. fn_name }
  end

  local called, result = pcall(remote.call, iface_name, fn_name, args)
  if not called then
    return { ok = false, e = "provider_error", m = tostring(result) }
  end
  return { ok = true, r = result }
end

return M
