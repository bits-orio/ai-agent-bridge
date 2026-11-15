-- AI Agent Bridge - scripts/tools/name_lookup.lua
-- Author: bits-orio
-- License: MIT
--
-- One prototype name lookup for every tool that takes one: an exact name
-- is used as it is; a name the game does not have is replaced by its close
-- match when there is exactly one, and the reply says so with resolved_from;
-- with several, nothing is guessed and the caller gets suggestions.
--
-- Why a single match is used. A refused name costs a whole model round: the
-- ledger holds "what do I need to make gold ore" refused for gold-ore, then
-- answered for pm-gold-ore, a mod's own prefix the model could not know.
-- When the game has one name that fits, the round buys nothing.
--
-- What counts as a close match, strongest first, the first tier with any
-- candidate deciding (scripts/close_names.lua): the name respelled (case,
-- spaces, a plural), then the vanilla nickname (items only: red science is
-- automation-science-pack), then names holding every word of it. A name
-- that only shares one word with others (iron, ore) is never used, only
-- suggested: that is a guess, not a match.

local close_names = require("scripts.close_names")

local M = {}

--- The prototype tables one kind of name lives in.
local TABLES = {
  item   = function() return { prototypes.item } end,
  fluid  = function() return { prototypes.fluid } end,
  goods  = function() return { prototypes.item, prototypes.fluid } end,
  entity = function() return { prototypes.entity } end,
  recipe = function() return { prototypes.recipe } end,
}

local function exists(tables, name)
  for _, t in ipairs(tables) do
    if t[name] then return true end
  end
  return false
end

--- The candidates of one tier, each once, in the order found.
local function collect(tables, list)
  local out, seen = {}, {}
  for _, name in ipairs(list) do
    if name and not seen[name] and exists(tables, name) then
      seen[name] = true
      out[#out + 1] = name
    end
  end
  return out
end

local function every_word(tables, name)
  local list = {}
  for _, t in ipairs(tables) do
    for _, found in ipairs(close_names.sharing_every_fragment(t, name)) do list[#list + 1] = found end
  end
  return list
end

--- The strongest tier of close matches for a name the game does not have.
local function close_matches(tables, kind, name)
  local respelled = collect(tables, close_names.spellings(name))
  if #respelled > 0 then return respelled end
  if kind == "item" or kind == "goods" then
    local nick = collect(tables, { close_names.nickname(name) })
    if #nick > 0 then return nick end
  end
  return collect(tables, every_word(tables, name))
end

--- Up to five names to suggest: the close matches, else any name sharing a
--- word with what was asked.
local function suggestions(tables, matches, name)
  if #matches > 0 then return matches end
  local out = {}
  for _, t in ipairs(tables) do
    for _, found in ipairs(close_names.sharing_fragment(t, name)) do
      if #out < close_names.MAX_SUGGESTIONS then out[#out + 1] = found end
    end
  end
  return out
end

--- Resolves `name` of `kind` (item, fluid, goods for either, entity,
--- recipe). Returns the name to use and, when it was not the one asked
--- for, {asked, used}; or nil and up to five suggestions.
function M.resolve(name, kind)
  local tables = TABLES[kind]()
  if exists(tables, name) then return name, nil end
  local matches = close_matches(tables, kind, name)
  if #matches == 1 then return matches[1], { asked = name, used = matches[1] } end
  return nil, suggestions(tables, matches, name)
end

--- resolve() over a list: the names to use in order, the {asked, used}
--- pairs (nil when none), and the names that did not resolve with their
--- suggestions ({name = list}, nil when all did).
function M.resolve_all(names, kind)
  local used, resolved, missing = {}, {}, {}
  for i, name in ipairs(names) do
    local use, extra = M.resolve(name, kind)
    if use then
      used[i] = use
      if extra then resolved[#resolved + 1] = extra end
    else
      missing[name] = extra
    end
  end
  return used, (#resolved > 0 and resolved or nil), (next(missing) and missing or nil)
end

return M
