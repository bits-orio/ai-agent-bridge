-- AI Agent Bridge - scripts/tools/recipe_names.lua
-- Author: bits-orio
-- License: MIT
--
-- The names recipe_needs is handed that the game does not have, and the real
-- ones the caller most likely meant. A live run sent red-science-pack and
-- paid a round to learn it was wrong; a reply that names
-- automation-science-pack beside the miss saves the round after it.
--
-- did_you_mean puts the likeliest first: the name respelled (case, spaces, a
-- plural; scripts/close_names.lua), then the name players give a base-game
-- science pack, circuit or belt by its colour or its planet, then names
-- holding every word of it, then names sharing any word with it, items
-- before fluids. The word passes run over the singular spellings too: gears,
-- circuits, belts, plates and engines found nothing while the plural was the
-- only word looked for, and none of those plurals is in a prototype name.
-- Five at most, each a real item or fluid.

local close_names = require("scripts.close_names")

local MAX_GUESSES = close_names.MAX_SUGGESTIONS

local M = {}

--- "item" or "fluid" for a name the game has, the item first when both
--- share it; nil for one it does not.
function M.kind_of(name)
  if prototypes.item[name] then return "item" end
  if prototypes.fluid[name] then return "fluid" end
  return nil
end

local function exists(name)
  return M.kind_of(name) ~= nil
end

--- Names in items or stop_at the game has no item or fluid for, sorted,
--- each once.
function M.unknown(items, stop_at)
  local seen, out = {}, {}
  for _, list in ipairs({ items, stop_at }) do
    for _, name in ipairs(list) do
      if not seen[name] and not exists(name) then
        seen[name] = true
        out[#out + 1] = name
      end
    end
  end
  table.sort(out)
  return out
end

--- The name normalised, then its other spellings, each once.
local function forms_of(name)
  local out, seen = {}, {}
  local candidates = close_names.spellings(name)
  table.insert(candidates, 1, close_names.normalise(name))
  for _, form in ipairs(candidates) do
    if not seen[form] then
      seen[form] = true
      out[#out + 1] = form
    end
  end
  return out
end

--- Every word of three or more letters in any of `forms`, each once, as one
--- query: a word shared by the plural and the singular is looked for once.
local function words_of(forms)
  local out, seen = {}, {}
  for _, form in ipairs(forms) do
    for word in form:gmatch("[%l%d]+") do
      if #word >= 3 and not seen[word] then
        seen[word] = true
        out[#out + 1] = word
      end
    end
  end
  return table.concat(out, " ")
end

--- Up to five real item or fluid names for one the game does not have,
--- likeliest first. Each word pass stops once five are found: it walks every
--- item or fluid there is, and only runs on a miss.
function M.did_you_mean(name)
  local out, seen = {}, {}
  local function add(candidate)
    if candidate and not seen[candidate] and #out < MAX_GUESSES and exists(candidate) then
      seen[candidate] = true
      out[#out + 1] = candidate
    end
  end
  local forms = forms_of(name)
  for _, form in ipairs(forms) do add(form) end
  add(close_names.nickname(name))
  local tables = { prototypes.item, prototypes.fluid }
  for _, table_ in ipairs(tables) do
    for _, form in ipairs(forms) do
      if #out >= MAX_GUESSES then return out end
      for _, candidate in ipairs(close_names.sharing_every_fragment(table_, form)) do add(candidate) end
    end
  end
  local words = words_of(forms)
  for _, table_ in ipairs(tables) do
    if #out >= MAX_GUESSES then return out end
    for _, candidate in ipairs(close_names.sharing_fragment(table_, words)) do add(candidate) end
  end
  return out
end

--- { [unknown name] = its guesses } for every name with any, nil when none
--- has one.
function M.guesses(unknown)
  local out, any = {}, false
  for _, name in ipairs(unknown) do
    local guesses = M.did_you_mean(name)
    if #guesses > 0 then
      out[name] = guesses
      any = true
    end
  end
  return any and out or nil
end

return M
