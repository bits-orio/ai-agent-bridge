-- AI Agent Bridge - scripts/text_clip.lua
-- Author: bits-orio
-- License: MIT
--
-- Cutting text to a byte length without leaving half a character behind.
-- Player names, force labels, questions and answers carry non-ASCII often
-- enough to matter, and a string cut inside a UTF-8 sequence prints as
-- garbage. One helper for every cut the companion makes: the question text,
-- the personality a question carries, a scope provider's key and tag, a
-- force label, and the renderer's per-line net.

local M = {}

--- The mark that ends a cut answer line: U+2026, the ellipsis the service
--- ends its own cuts with, written as bytes because the game's Lua 5.2 has
--- no \u{} escape.
M.ELLIPSIS = "\226\128\166"

--- The longest prefix of `s` that is at most `n` bytes and ends on a
--- character boundary. Walks back to the lead byte of the sequence the cut
--- lands in and keeps that sequence only if all of it fits.
function M.bytes(s, n)
  if #s <= n then return s end
  if n <= 0 then return "" end
  local i = n
  while i > 0 do
    local b = s:byte(i)
    if b < 0x80 or b >= 0xC0 then break end -- reached the lead byte
    i = i - 1
  end
  local cut = n
  if i > 0 then
    local lead = s:byte(i)
    local width = 1
    if lead >= 0xF0 then width = 4
    elseif lead >= 0xE0 then width = 3
    elseif lead >= 0xC0 then width = 2 end
    if i + width - 1 > n then cut = i - 1 end
  end
  return s:sub(1, cut)
end

return M
