-- One helper for every pass that rewrites chat text: apply a function to
-- the stretches outside [..] tags and leave the tags themselves alone, so a
-- sprite, a colour or a gps tag is never rewritten by a later pass.

local M = {}

--- `text` with `fn` applied to every stretch outside a [..] tag.
function M.map_outside_tags(text, fn)
  if type(text) ~= "string" or not text:find("[", 1, true) then return fn(text) end
  local out, pos = {}, 1
  while true do
    local open_, close_ = text:find("%[[^%[%]]*%]", pos)
    if not open_ then break end
    out[#out + 1] = fn(text:sub(pos, open_ - 1))
    out[#out + 1] = text:sub(open_, close_)
    pos = close_ + 1
  end
  out[#out + 1] = fn(text:sub(pos))
  return table.concat(out)
end

return M
