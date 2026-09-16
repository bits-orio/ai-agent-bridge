-- AI Agent Bridge - scripts/player_colors.lua
-- Author: bits-orio
-- License: MIT
--
-- Player names in the player's own chat colour, the way the game itself
-- prints them. LuaPlayer::chat_color is the engine's, every player has one,
-- and it takes no team mod to read, so this is the one colouring the
-- companion can do for any server: a force's colour belongs to the team mod
-- and arrives through the force_labels_v1 label; a player's belongs to the
-- engine and arrives here. The swap happens where labels and sprites do,
-- once, when an answer is rendered, so the model never spends a token on it.
--
-- API verified against the 2.0.77 docs: LuaPlayer::chat_color :: Color,
-- "The color used when this player talks in game"; Color carries r, g, b
-- either all in [0, 1] or all in [0, 255] when any is above 1.

local richtext = require("scripts.richtext")
local bounded  = require("scripts.tools.bounded")

local M = {}

--- "[color=r,g,b]" for a Color, channels normalised to [0, 1] and written
--- short: a 0.5 stays 0.5, a 1 is 1, never 1.000000.
local function open_tag(color)
  local r, g, b = color.r or 0, color.g or 0, color.b or 0
  if r > 1 or g > 1 or b > 1 then r, g, b = r / 255, g / 255, b / 255 end
  return "[color=" .. tostring(bounded.round(r, 3)) .. "," .. tostring(bounded.round(g, 3)) .. "," ..
         tostring(bounded.round(b, 3)) .. "]"
end

-- One scan per tick: a rendered table asks once per cell, and the map cannot
-- change inside a tick. A plain Lua local, never storage, and the same on
-- every peer because game.players is.
local cache, cache_tick = nil, nil
local function cached_map()
  if cache_tick ~= game.tick then
    cache, cache_tick = {}, game.tick
    for _, player in pairs(game.players) do
      if player.valid and type(player.name) == "string" and player.name ~= "" and player.chat_color then
        cache[player.name] = open_tag(player.chat_color)
      end
    end
  end
  return cache
end

--- `text` with every player name outside a tag wrapped in that player's
--- chat colour. Names are matched whole and case-sensitively, the way the
--- game spells them: a username is not a word, and "Bob" in prose is a
--- player only when the player is called exactly that. A name inside an
--- open colour or font span is left alone, so one span never nests another.
--- A trailing dot is not part of the name: "ask Bob." colours Bob.
function M.decorate(text)
  if type(text) ~= "string" or text == "" or not text:find("%S") then return text end
  local map = cached_map()
  if next(map) == nil then return text end
  return richtext.map_outside_tags(text, function(chunk, depth)
    if (depth or 0) > 0 then return chunk end
    return (chunk:gsub("%f[%w%-_.]([%w%-_.]+)%f[^%w%-_.]", function(token)
      local tag = map[token]
      if tag then return tag .. token .. "[/color]" end
      local bare, dots = token:match("^(.-)(%.+)$")
      if bare and map[bare] then return map[bare] .. bare .. "[/color]" .. dots end
      return token
    end))
  end)
end

return M
