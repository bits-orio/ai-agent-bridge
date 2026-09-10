-- Finds every provider's agent_tools_v1 probe in remote.interfaces
-- (CONTEXT.md "Probe"), reads one provider's manifest, and routes the aab-rpc
-- `call` op to one tool on one provider. Nothing here is ever stored: a
-- manifest is read from scratch on every use, the interface list is sorted so
-- a reply built from it is byte-stable, and a removed provider just vanishes
-- (CONTEXT.md invariant 3).
--
-- scripts/rpc_catalog.lua shapes these reads into the three catalog ops.

local manifest_check = require("scripts.probe_manifest")

local PROBE_FN = "agent_tools_v1"

local M = {}

--- One line in the log, the shape every drop uses.
function M.complain(message)
  log("[ai-agent-bridge] probe: " .. message)
end

--- Does this interface carry a probe right now?
function M.has_probe(iface_name)
  local functions = remote.interfaces[iface_name]
  return (functions and functions[PROBE_FN]) ~= nil
end

--- Sorted names of every interface carrying a probe. Sorted rather than in
--- whatever order pairs() walked, so two identical games answer identically.
function M.provider_names()
  local names = {}
  for iface_name, functions in pairs(remote.interfaces) do
    if functions[PROBE_FN] then names[#names + 1] = iface_name end
  end
  table.sort(names)
  return names
end

--- Reads one provider's manifest. Returns the manifest's version and its
--- usable tools, or nil and the reason it was unusable. `report` takes one
--- sentence per dropped tool; pass nil to drop quietly.
function M.read(iface_name, report)
  local answered, manifest = pcall(remote.call, iface_name, PROBE_FN)
  if not answered then
    return nil, PROBE_FN .. "() errored: " .. tostring(manifest):match("^[^\n]*")
  end
  if type(manifest) ~= "table" or type(manifest.tools) ~= "table" then
    return nil, PROBE_FN .. "() did not return a manifest with a tools table"
  end
  return { v = manifest.v or 1, tools = manifest_check.clean_tools(manifest.tools, report) }
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
  -- A tool takes one plain table (CONTEXT.md "Tool"), and a client is entitled
  -- to leave `a` out for a tool that needs nothing. Substitute the empty table
  -- here, once, so every tool says "force is required" in its own words
  -- instead of indexing nil and handing the caller a Lua traceback through
  -- this mod's internal file names.
  if args == nil then
    args = {}
  elseif type(args) ~= "table" then
    return { ok = false, e = "bad_json", m = "a must be a JSON object" }
  end

  local iface_functions = remote.interfaces[iface_name]
  if not iface_functions or not iface_functions[PROBE_FN] then
    return { ok = false, e = "no_provider", m = "no such provider: " .. iface_name }
  end

  -- Re-read and re-clean, quietly: the catalog already logged whatever it
  -- dropped, and a call must never reach a tool the catalog refused to list.
  local manifest = M.read(iface_name, nil)
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
