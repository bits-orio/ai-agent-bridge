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

return M
