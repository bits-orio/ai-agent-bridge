-- The catalog ops of aab-rpc-v1, all three pure reads, merged into rpc.lua's
-- op table at load time the way scripts/rpc_selftest.lua is.
--
-- Why three and not one. A `tools` reply puts every provider on the server
-- under a single byte cap, so the most verbose mod installed decided whether
-- anybody's tools arrived at all: one provider over the cap and the whole
-- catalog came back as too_large, which leaves the agent answering from the
-- prompt alone. `providers` lists the probes and the names under each, which
-- stays small however many mods answer, and `manifest` fetches one provider's
-- entry on its own. A manifest too large to send now costs that provider its
-- tools and nobody else theirs. `tools` stays for small servers and for the
-- end-to-end harness.
--
-- Nothing here is stored and nothing is cached: every op rescans
-- remote.interfaces (CONTEXT.md invariant 3).

local probe = require("scripts.probe")

local M = { ops = {} }

local function ok_reply(r) return { ok = true, r = r } end

local function no_provider(message) return { ok = false, e = "no_provider", m = message } end

--- One provider's manifest, with one log line per tool it cost itself and one
--- for a provider whose probe could not be read at all. A broken probe must
--- never cost the agent every other mod's tools, so this reports and carries on.
local function read(iface_name)
  local manifest, unusable = probe.read(iface_name, function(dropped)
    probe.complain("dropped tool " .. iface_name .. "." .. dropped)
  end)
  if not manifest then
    probe.complain("dropped provider " .. iface_name .. ": " .. unusable)
  end
  return manifest
end

--- Walks every provider in interface-name order, handing each usable manifest
--- to `shape`, and collects what `shape` returns.
local function each_provider(shape)
  local out = {}
  for _, iface_name in ipairs(probe.provider_names()) do
    local manifest = read(iface_name)
    if manifest then out[#out + 1] = shape(iface_name, manifest) end
  end
  return out
end

--- The whole catalog in one reply: {iface, v, tools} per provider, manifests and all.
function M.ops.tools(_req, _cmd)
  return ok_reply(each_provider(function(iface_name, manifest)
    return { iface = iface_name, v = manifest.v, tools = manifest.tools }
  end))
end

--- Step one of the catalog: which interfaces carry a probe, and the names of
--- the tools under each. Names only, sorted, so the reply size grows with the
--- number of tools rather than with how much their authors had to say.
function M.ops.providers(_req, _cmd)
  return ok_reply(each_provider(function(iface_name, manifest)
    local names = {}
    for name in pairs(manifest.tools) do names[#names + 1] = name end
    table.sort(names)
    return { iface = iface_name, v = manifest.v, tools = names }
  end))
end

--- Step two: one provider's manifest, verbatim, as {v, tools}. The interface
--- name is the caller's own argument so it is not echoed back.
function M.ops.manifest(req, _cmd)
  if type(req.i) ~= "string" or req.i == "" then
    return no_provider("manifest needs i, the provider's interface name")
  end
  if not probe.has_probe(req.i) then
    return no_provider("no such provider: " .. req.i)
  end
  local manifest = read(req.i)
  if not manifest then
    return no_provider("provider manifest invalid: " .. req.i)
  end
  return ok_reply({ v = manifest.v, tools = manifest.tools })
end

return M
