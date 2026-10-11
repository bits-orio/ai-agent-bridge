-- AI Agent Bridge - scripts/richtext.lua
-- Author: bits-orio
-- License: MIT
--
-- One helper for every pass that rewrites chat text: apply a function to
-- the stretches outside [..] tags and leave the tags themselves alone, so a
-- sprite, a colour or a gps tag is never rewritten by a later pass. And one
-- for every cut of text the game will print (fit), so no cut tears a tag.

local text_clip = require("scripts.text_clip")

local M = {}

--- True for a tag that only styles the text around it: a colour or font
--- span's opening or closing tag. `tag` is the text between the brackets.
function M.is_style(tag)
  return tag:sub(1, 6) == "color=" or tag:sub(1, 5) == "font=" or tag == "/color" or tag == "/font"
end

--- `text` with `fn` applied to every stretch outside a [..] tag. fn also
--- receives how many colour or font spans are open around that stretch, so
--- a pass that would nest a coloured label inside an open colour can leave
--- the colour off instead.
function M.map_outside_tags(text, fn)
  if type(text) ~= "string" or not text:find("[", 1, true) then return fn(text, 0) end
  local out, pos, depth = {}, 1, 0
  while true do
    local open_, close_ = text:find("%[[^%[%]]*%]", pos)
    if not open_ then break end
    out[#out + 1] = fn(text:sub(pos, open_ - 1), depth)
    local tag = text:sub(open_ + 1, close_ - 1)
    if tag:sub(1, 6) == "color=" or tag:sub(1, 5) == "font=" then
      depth = depth + 1
    elseif (tag == "/color" or tag == "/font") and depth > 0 then
      depth = depth - 1
    end
    out[#out + 1] = text:sub(open_, close_)
    pos = close_ + 1
  end
  out[#out + 1] = fn(text:sub(pos), depth)
  return table.concat(out)
end

--- `text` with a tag a cut landed inside dropped back to its bracket, and
--- the spaces before it: "see [/col" is "see".
function M.tidy(text)
  local last_open = text:match("^.*()%[")
  local last_close = text:match("^.*()%]")
  if last_open and (not last_close or last_open > last_close) then
    return (text:sub(1, last_open - 1):gsub("%s+$", ""))
  end
  return text
end

--- The closing tags `text` still owes, innermost first. Only color and font
--- open a span; gps, img and the rest stand alone. A closer met with nothing
--- open is left alone, since a stray [/color] renders as nothing.
function M.closers(text)
  local open = {}
  for tag in text:gmatch("%[([^%[%]]*)%]") do
    if tag:sub(1, 6) == "color=" then
      open[#open + 1] = "color"
    elseif tag:sub(1, 5) == "font=" then
      open[#open + 1] = "font"
    elseif tag == "/color" or tag == "/font" then
      local want = tag:sub(2)
      for j = #open, 1, -1 do
        if open[j] == want then table.remove(open, j) break end
      end
    end
  end
  local out = {}
  for j = #open, 1, -1 do out[#out + 1] = "[/" .. open[j] .. "]" end
  return table.concat(out)
end

--- `text` made renderable again after a cut. Factorio draws a whole chat
--- line raw, tags and all, when any tag in it is malformed, and a byte cut
--- can do that two ways: land inside a tag, leaving "[/col", or fall after a
--- colour or font was opened and before it closed. The torn tag is dropped
--- back to its bracket (tidy) and the open spans are closed innermost first
--- (closers). The service repairs its own cuts the same way (clipRichText);
--- this covers the renderer's, which come after force labels and sprites
--- have been decorated in and so can land inside a tag the model never wrote.
function M.repair(text)
  if type(text) ~= "string" then return text end
  local tidied = M.tidy(text)
  return tidied .. M.closers(tidied)
end

--- `text` within `n` bytes with no tag torn. Text that fits is returned
--- whole. Longer text is cut on a character boundary, a tag the cut landed
--- in is dropped (tidy) with the spaces before it, and `mark`, when given,
--- then the closing tags still owed (closers) follow, all of it within `n`.
--- Used for the renderer's per-line net, whose mark is an ellipsis, and for
--- a scope provider's tag, printed on an answer's first line.
function M.fit(text, n, mark)
  if #text <= n then return text end
  mark = mark or ""
  local budget = n - #mark
  while true do
    local kept = M.tidy(text_clip.bytes(text, budget)):gsub("%s+$", "")
    local closers = M.closers(kept)
    if kept == "" or #kept + #mark + #closers <= n then
      return kept .. mark .. closers
    end
    budget = #kept - 1
  end
end

return M
