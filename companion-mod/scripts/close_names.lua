-- AI Agent Bridge - scripts/close_names.lua
-- Author: bits-orio
-- License: MIT
--
-- The real prototype name behind one the game does not have. One source for
-- every pass that repairs or suggests a name: the renderer fixing a sprite
-- tag the model misspelled, find_entities answering an unknown name with
-- close ones, recipe_needs saying which item was meant.
--
-- spellings undoes the ways a name drifts from its prototype: capitals,
-- spaces or underscores for hyphens, a plural. nickname reads what players
-- call a vanilla science pack, circuit or belt: red belt is
-- fast-transport-belt. sharing_fragment lists names that share a word of
-- three or more letters with what was asked for, sharing_every_fragment the
-- names that hold every such word.

local MAX_SUGGESTIONS = 5

-- What players call the base game's science packs, circuits and belts, by
-- the word in front of the family's own: red science, blue circuits, a red
-- belt. Vanilla only, the base game and Space Age: every target is a
-- prototype in data/base or data/space-age of 2.0.77 and 2.1.17. The
-- companion names no other mod, and a nickname for a modded item would be a
-- guess about prototypes it has never seen. A caller always checks the game
-- has the target, so a modded game that renamed or removed one simply never
-- hears its nickname.
local NICKNAMES = {
  science = {
    red = "automation-science-pack", green = "logistic-science-pack",
    grey = "military-science-pack", gray = "military-science-pack", black = "military-science-pack",
    blue = "chemical-science-pack", purple = "production-science-pack",
    yellow = "utility-science-pack", white = "space-science-pack",
    vulcanus = "metallurgic-science-pack", fulgora = "electromagnetic-science-pack",
    gleba = "agricultural-science-pack", aquilo = "cryogenic-science-pack",
  },
  circuit = { green = "electronic-circuit", red = "advanced-circuit", blue = "processing-unit" },
  belt = {
    yellow = "transport-belt", red = "fast-transport-belt",
    blue = "express-transport-belt", green = "turbo-transport-belt",
  },
}
-- The family a nickname's tail names: the only tails a nickname may end in.
local FAMILY = {
  science = "science", ["science-pack"] = "science",
  circuit = "circuit", belt = "belt", ["transport-belt"] = "belt",
}

local M = {}

--- `name` lowercased and trimmed, every run of spaces or underscores one
--- hyphen: "Iron Plates" is iron-plates.
function M.normalise(name)
  local trimmed = tostring(name):lower():gsub("^%s+", ""):gsub("%s+$", "")
  return (trimmed:gsub("[%s_]+", "-"))
end

--- The other spellings a prototype named like `name` may go by, the likeliest
--- first: normalised, then with a trailing plural dropped, so iron-plates is
--- also iron-plate and batteries also battery. Never `name` itself, which a
--- caller tries before these.
function M.spellings(name)
  local out, seen = {}, { [name] = true }
  local function add(spelling)
    if spelling ~= "" and not seen[spelling] then
      seen[spelling] = true
      out[#out + 1] = spelling
    end
  end
  local base = M.normalise(name)
  add(base)
  if base:sub(-1) == "s" then add(base:sub(1, -2)) end
  if base:sub(-2) == "es" then add(base:sub(1, -3)) end
  if base:sub(-3) == "ies" then add(base:sub(1, -4) .. "y") end
  return out
end

--- The item a spelling that is exactly <colour>-<family> nicknames, or nil.
local function nickname_of(form)
  local word, tail = form:match("^(%l+)%-(.+)$")
  local family = word and FAMILY[tail]
  return family and NICKNAMES[family][word] or nil
end

--- The one item players call `name`, or nil: red-transport-belt is
--- fast-transport-belt, Blue Science Packs is chemical-science-pack. The
--- whole name must be a colour (a planet, for a Space Age pack) and a
--- family, respelled the way `spellings` allows and no further: "red",
--- "red-belt-mk2" and "purple-belt" name nothing, because a guess draws the
--- wrong icon. One target or none, never a list. The caller checks that the
--- game has the target, and tries the real names before this.
function M.nickname(name)
  local forms = M.spellings(name)
  table.insert(forms, 1, M.normalise(name))
  for _, form in ipairs(forms) do
    local target = nickname_of(form)
    if target then return target end
  end
  return nil
end

--- Up to five names in `table_` sharing a fragment of three or more letters
--- with `query`, so "repair-kit" answers with repair-pack. One pass over the
--- table per fragment, and only ever on a miss. Moved here from
--- find_entities unchanged, which is why the first five found come back in
--- the order found and a shorter list sorted.
function M.sharing_fragment(table_, query)
  local out, seen = {}, {}
  for fragment in tostring(query):lower():gmatch("[%l%d]+") do
    if #fragment >= 3 then
      for name in pairs(table_) do
        if not seen[name] and name:find(fragment, 1, true) then
          seen[name] = true
          out[#out + 1] = name
          if #out >= MAX_SUGGESTIONS then return out end
        end
      end
    end
  end
  table.sort(out)
  return out
end

--- The words of three or more letters in `query`, lowercased.
local function fragments_of(query)
  local out = {}
  for fragment in tostring(query):lower():gmatch("[%l%d]+") do
    if #fragment >= 3 then out[#out + 1] = fragment end
  end
  return out
end

local function holds_all(name, fragments)
  for _, fragment in ipairs(fragments) do
    if not name:find(fragment, 1, true) then return false end
  end
  return true
end

--- Up to five names in `table_` that hold every word of three or more
--- letters in `query`, sorted: electric-engine is electric-engine-unit, where
--- sharing_fragment would fill its five with whatever says electric first.
--- Nothing for a query with no such word.
function M.sharing_every_fragment(table_, query)
  local fragments = fragments_of(query)
  if #fragments == 0 then return {} end
  local out = {}
  for name in pairs(table_) do
    if holds_all(name, fragments) then out[#out + 1] = name end
  end
  table.sort(out)
  for i = #out, MAX_SUGGESTIONS + 1, -1 do out[i] = nil end
  return out
end

M.MAX_SUGGESTIONS = MAX_SUGGESTIONS
M.NICKNAMES = NICKNAMES

return M
