-- AI Agent Bridge - scripts/probe_manifest.lua
-- Author: bits-orio
-- License: MIT
--
-- What a provider's manifest has to look like before the companion passes it
-- on (CONTEXT.md "Manifest"). A manifest comes from another mod, so it is
-- checked here rather than trusted: the tools reply is one JSON object, and a
-- single unencodable or malformed entry in it would cost the agent every tool
-- from every provider at once.
--
-- Dropping is per entry. A provider with one bad tool keeps the rest of them.

local M = {}

--- params is optional, and when present it is a map of strings: the grammar
--- "<type>[!] <description>" the service parses.
local function params_problem(params)
  if params == nil then return nil end
  if type(params) ~= "table" then return "params is not a table" end
  for name, grammar in pairs(params) do
    if type(name) ~= "string" then return "a param name is not a string" end
    if type(grammar) ~= "string" then return "param " .. name .. " is not a string" end
  end
  return nil
end

local function entry_problem(name, entry)
  if type(name) ~= "string" then return "tool name is not a string" end
  if type(entry) ~= "table" then return "entry is not a table" end
  if type(entry.desc) ~= "string" then return "desc is not a string" end
  return params_problem(entry.params)
end

--- The usable tools of one manifest, as a fresh table. `report` is called with
--- one sentence per dropped entry; pass nil to drop quietly.
function M.clean_tools(tools, report)
  local out = {}
  for name, entry in pairs(tools) do
    local wrong = entry_problem(name, entry)
    if wrong then
      if report then report(tostring(name) .. ": " .. wrong) end
    else
      out[name] = entry
    end
  end
  return out
end

return M
