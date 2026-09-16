-- AI Agent Bridge - scripts/sprites.lua
-- Author: bits-orio
-- License: MIT
--
-- Bare internal names become sprites at render time. The model is asked to
-- write [img=item.iron-ore] and never "iron-ore" or "Iron ore", but a model
-- that writes the internal name anyway still gets an icon: the companion
-- knows every prototype, so the lookup is exact and costs one table read.
--
-- Only hyphenated names are touched (iron-ore, assembling-machine-2). A bare
-- single word such as stone or water is ordinary prose more often than a
-- prototype, and the prompt covers those. Text inside any [..] tag is left
-- alone, so an existing sprite or colour tag is never rewritten.

local richtext = require("scripts.richtext")

local M = {}

-- Lookup order for a name that exists in more than one class: coal is both
-- an item and a resource entity, and prose about coal means the item.
local CLASSES = { "item", "fluid", "technology", "entity" }

local function class_of(name)
  if not prototypes then return nil end
  for _, class in ipairs(CLASSES) do
    local table_ = prototypes[class]
    if table_ and table_[name] then return class end
  end
  return nil
end

local function decorate_plain(text)
  return (text:gsub("%f[%w%-]([%l%d]+%-[%l%d%-]*[%l%d])%f[^%w%-]", function(name)
    local class = class_of(name)
    if class then return "[img=" .. class .. "." .. name .. "]" end
    return name
  end))
end

--- `text` with every bare hyphenated prototype name outside a tag turned
--- into its sprite tag.
function M.decorate(text)
  if type(text) ~= "string" or not text:find("-", 1, true) then return text end
  return richtext.map_outside_tags(text, decorate_plain)
end

-- The sprite classes an [img=class.name] may use, mapped to what the game
-- calls the path when the two differ. The service's prompt once asked for
-- [img=planet.nauvis], and planet/nauvis is not a sprite path; the planet's
-- icon lives at space-location/nauvis (helpers.is_valid_sprite_path on the
-- live engine: false and true), so every planet icon printed raw until this
-- was measured.
local PATH_ALIASES = { planet = "space-location" }

-- The clickable tags the model may write, and the prototype table each is
-- checked against. A tag naming a prototype the game does not have prints
-- raw, brackets and all; its plain name is what the reader should get.
local CLICKABLE = {
  item = "item", entity = "entity", fluid = "fluid", technology = "technology",
  recipe = "recipe", tile = "tile", ["virtual-signal"] = "virtual_signal",
}

local function sprite_exists(path)
  local ok, valid = pcall(helpers.is_valid_sprite_path, path)
  return ok and valid == true
end

--- `text` with every sprite or clickable tag the game cannot draw taken
--- out. Factorio prints a whole chat line raw, tags and all, when a sprite
--- tag names a sprite it does not have: the model wrote [img=entity.biter]
--- for "biters", and there is no prototype of that name, only small-biter
--- and its kin. An [img=...] whose sprite does not exist is dropped (a
--- planet path is read as the space location it is first), and a clickable
--- tag for an unknown prototype prints its plain name. Every other tag,
--- gps, color, font, passes untouched.
function M.prune(text)
  if type(text) ~= "string" or not text:find("[", 1, true) then return text end
  local dropped = false
  text = text:gsub("%[([%w%-]+)=([^%[%]]*)%]", function(kind, value)
    if kind == "img" then
      local class, name = value:match("^([%w%-]+)[./](.+)$")
      if class then
        if sprite_exists(class .. "/" .. name) then return "[img=" .. class .. "." .. name .. "]" end
        local alias = PATH_ALIASES[class]
        if alias and sprite_exists(alias .. "/" .. name) then return "[img=" .. alias .. "." .. name .. "]" end
      end
      dropped = true
      return ""
    end
    local table_name = CLICKABLE[kind]
    if table_name and prototypes and prototypes[table_name] then
      if prototypes[table_name][value] then return nil end
      return value
    end
    return nil
  end)
  if dropped then text = text:gsub("  +", " "):gsub("^ ", "") end
  return text
end

return M
