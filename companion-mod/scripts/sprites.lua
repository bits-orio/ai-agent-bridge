-- AI Agent Bridge - scripts/sprites.lua
-- Author: bits-orio
-- License: MIT
--
-- Sprite tags in an answer, both ways round. decorate turns a bare internal
-- name the model wrote into its icon; repair turns a tag the game cannot
-- draw into one it can, or into the plain words it stood for.
--
-- decorate touches only hyphenated names (iron-ore, assembling-machine-2).
-- A bare single word such as stone or water is ordinary prose more often
-- than a prototype, and the prompt covers those. Text inside any [..] tag is
-- left alone, so an existing sprite or colour tag is never rewritten.

local richtext    = require("scripts.richtext")
local close_names = require("scripts.close_names")

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

-- ── repair ────────────────────────────────────────────────────────────
-- Factorio prints a tag it cannot draw raw, brackets and all. The model wrote
-- [img=entity.biter] for a kind of enemy with no prototype of that name, and
-- "A [img=item.red-transport-belt] carries 30/s" for a belt the game calls
-- fast-transport-belt. Dropping the tag left "A  carries 30/s", a sentence
-- with its subject missing. Each tag goes down one chain instead, the first
-- step that finds something the game has wins:
--   1. a planet path is read as the space location it is (planet/nauvis is
--      not a sprite path, space-location/nauvis is: helpers.is_valid_sprite_path
--      on the live engine, false and true);
--   2. the same name in another class: item.crude-oil is fluid.crude-oil,
--      entity.iron-plate is item.iron-plate;
--   3. the name respelled (scripts/close_names.lua): lowercase, hyphens for
--      spaces and underscores, a plural dropped, then step 2 again;
--   4. the name players give a vanilla item (close_names.nickname): the red
--      belt is fast-transport-belt, a blue science pack is
--      chemical-science-pack, then step 2 again for that item. The whole
--      name has to be a nickname, colour and family, never a partial match;
--   5. the plain words, hyphens to spaces: "red transport belt", "biter".
-- A clickable tag, [item=x] and its kin, goes down the same chain and ends at
-- its plain name. Every step asks the game: helpers.is_valid_sprite_path for
-- an [img], the prototype tables for a clickable tag. A name the game really
-- has always wins over a nickname. gps, color, font and every other tag pass
-- untouched.

-- What the model may call a sprite class the game names otherwise.
local PATH_ALIASES = { planet = "space-location" }

-- Where a name is looked for after the class the model wrote, in order: the
-- sprite classes an [img] is repaired across, and the clickable tags.
local KINDS = { "item", "fluid", "entity", "technology", "recipe", "tile", "virtual-signal" }

-- The clickable tags the model may write, and the prototype table each is
-- checked against.
local CLICKABLE = {
  item = "item", fluid = "fluid", entity = "entity", technology = "technology",
  recipe = "recipe", tile = "tile", ["virtual-signal"] = "virtual_signal",
}

local function sprite_exists(path)
  local ok, valid = pcall(helpers.is_valid_sprite_path, path)
  return ok and valid == true
end

local function has_sprite(class, name)
  return sprite_exists(class .. "/" .. name)
end

local function has_prototype(kind, name)
  local table_ = prototypes and prototypes[CLICKABLE[kind]]
  return table_ ~= nil and table_[name] ~= nil
end

--- The classes to search, each once: the one written, its alias, the rest.
local function search_order(written, alias, order)
  local out, seen = {}, {}
  local function add(class)
    if class and not seen[class] then
      seen[class] = true
      out[#out + 1] = class
    end
  end
  add(written)
  add(alias)
  for _, class in ipairs(order) do add(class) end
  return out
end

--- The first class holding `name` exactly as spelled, or nil.
local function first_class(name, classes, has)
  for _, class in ipairs(classes) do
    if has(class, name) then return class end
  end
  return nil
end

--- The first class and spelling the game has for `name`: the name as
--- written through every class, then each respelling the same way, and last
--- the item players nickname it after, asked of the game like the rest.
local function find(name, classes, has)
  local spellings = close_names.spellings(name)
  table.insert(spellings, 1, name)
  for _, spelling in ipairs(spellings) do
    local class = first_class(spelling, classes, has)
    if class then return class, spelling end
  end
  local target = close_names.nickname(name)
  local class = target and first_class(target, classes, has)
  if class then return class, target end
  return nil
end

local function words(name)
  return (name:gsub("[%-_]+", " "))
end

--- What one [img=...] becomes. nil keeps a bare sprite name the game has.
local function repair_img(value)
  local class, name = value:match("^([%w%-]+)[./](.+)$")
  if not class then
    if sprite_exists(value) then return nil end
    name = value
  end
  local found, spelling = find(name, search_order(class, PATH_ALIASES[class], KINDS), has_sprite)
  if found then return "[img=" .. found .. "." .. spelling .. "]" end
  return words(name)
end

--- ",quality=rare" when the game has that quality, else nothing: a quality
--- it does not have would print the whole tag raw.
local function quality_of(rest)
  local quality = rest:match("^,quality=([%w%-_]+)$")
  if quality and prototypes and prototypes.quality and prototypes.quality[quality] then return rest end
  return ""
end

--- What one clickable tag becomes. nil keeps it exactly as written.
local function repair_clickable(kind, value)
  local name, rest = value:match("^([^,]*)(.*)$")
  local quality = quality_of(rest)
  local found, spelling = find(name, search_order(kind, nil, KINDS), has_prototype)
  if not found then return name end
  if found == kind and spelling == name and quality == rest then return nil end
  return "[" .. found .. "=" .. spelling .. quality .. "]"
end

--- `text` with every sprite or clickable tag the game cannot draw repaired:
--- a tag it can draw, or the words it stood for, never raw brackets.
function M.repair(text)
  if type(text) ~= "string" or not text:find("[", 1, true) then return text end
  local emptied = false
  text = text:gsub("%[([%w%-]+)=([^%[%]]*)%]", function(kind, value)
    local fixed
    if kind == "img" then
      fixed = repair_img(value)
    elseif CLICKABLE[kind] then
      fixed = repair_clickable(kind, value)
    end
    if fixed == "" then emptied = true end
    return fixed
  end)
  -- Only a tag with nothing in it leaves a hole; close it up.
  if emptied then text = text:gsub("  +", " "):gsub("^ ", "") end
  return text
end

return M
