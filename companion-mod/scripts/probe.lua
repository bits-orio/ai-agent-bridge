-- Scans remote.interfaces for every provider's agent_tools_v1 probe
-- (CONTEXT.md "Probe", "Catalog") and routes the aab-rpc `call` op to one
-- tool on one provider. Nothing here is ever stored: the catalog is
-- rebuilt from scratch on every use, sorted by interface name so its JSON
-- stays byte-stable, and a removed provider just vanishes (CONTEXT.md
-- invariant 3).

local manifest_check = require("scripts.probe_manifest")

local PROBE_FN = "agent_tools_v1"

local M = {}

local function complain(message)
  log("[ai-agent-bridge] probe: " .. message)
end

--- Reads one provider's manifest. Returns the manifest's version and its
--- usable tools, or nil and the reason it was unusable. `report` takes one
--- sentence per dropped tool.
local function read_manifest(iface_name, report)
  local answered, manifest = pcall(remote.call, iface_name, PROBE_FN)
  if not answered then
    return nil, PROBE_FN .. "() errored: " .. tostring(manifest):match("^[^\n]*")
  end
  if type(manifest) ~= "table" or type(manifest.tools) ~= "table" then
    return nil, PROBE_FN .. "() did not return a manifest with a tools table"
  end
  return { v = manifest.v or 1, tools = manifest_check.clean_tools(manifest.tools, report) }
end

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
      local manifest, unusable = read_manifest(iface_name, function(dropped)
        complain("dropped tool " .. iface_name .. "." .. dropped)
      end)
      if manifest then
        providers[#providers + 1] = { iface = iface_name, v = manifest.v, tools = manifest.tools }
      else
        -- One line, and on with the scan: one mod's broken probe must not cost
        -- the agent every other mod's tools.
        complain("dropped provider " .. iface_name .. ": " .. unusable)
      end
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

  -- Re-read and re-clean, quietly: the catalog already logged whatever it
  -- dropped, and a call must never reach a tool the catalog refused to list.
  local manifest = read_manifest(iface_name, nil)
  if not manifest then
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
    -- The engine appends a Lua traceback to the message; keep the first line,
    -- which already names the interface, the function and the error.
    return { ok = false, e = "provider_error", m = (tostring(result):match("^[^\n]*")) }
  end
  return { ok = true, r = result }
end

return M
