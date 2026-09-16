-- AI Agent Bridge - scripts/richtext.lua
-- Author: bits-orio
-- License: MIT
--
-- One helper for every pass that rewrites chat text: apply a function to
-- the stretches outside [..] tags and leave the tags themselves alone, so a
-- sprite, a colour or a gps tag is never rewritten by a later pass.

local M = {}

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

--- `text` made renderable again after a cut. Factorio draws a whole chat
--- line raw, tags and all, when any tag in it is malformed, and a byte cut
--- can do that two ways: land inside a tag, leaving "[/col", or fall after a
--- colour or font was opened and before it closed. The torn tag is dropped
--- back to its bracket and the open spans are closed innermost first. Only
--- color and font open a span; gps, img and the rest stand alone. The
--- service repairs its own cuts the same way (clipRichText); this covers the
--- renderer's, which come after team labels and sprites have been decorated
--- in and so can land inside a tag the model never wrote.
function M.repair(text)
  if type(text) ~= "string" then return text end
  local last_open = text:match("^.*()%[")
  local last_close = text:match("^.*()%]")
  if last_open and (not last_close or last_open > last_close) then
    text = text:sub(1, last_open - 1):gsub("%s+$", "")
  end
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
  local closers = {}
  for j = #open, 1, -1 do closers[#closers + 1] = "[/" .. open[j] .. "]" end
  return text .. table.concat(closers)
end

return M
