-- AI Agent Bridge - scripts/tools/mods.lua
-- Author: bits-orio
-- License: MIT
--
-- mods: the active mod list. A model answers recipe and technology questions
-- from memory of the base game; on a modded server that memory is wrong, and
-- this is the cheap way to know whether it can be trusted.
--
-- API names verified against runtime-api.json 2.1.23: LuaBootstrap::active_mods
-- (dictionary[string -> string], mod name to version).

local bounded = require("scripts.tools.bounded")

local M = {}

M.manifest = {
  mods = {
    desc = "The active mods with versions. Check this before answering recipe or technology questions from memory on a modded server.",
    params = {
      offset = bounded.OFFSET_PARAM,
    },
  },
}

local function mods(a)
  local rows = {}
  for name, version in pairs(script.active_mods) do rows[#rows + 1] = { name = name, version = version } end
  bounded.by_name(rows)
  return bounded.page(rows, a.offset, function(page)
    return { count = #rows, mods = page }
  end)
end

M.functions = { mods = mods }

return M
