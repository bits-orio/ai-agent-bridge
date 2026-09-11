-- Pure formatters: one artifact table -> an array of display-line strings, one
-- shape each (ADR 0003, PLAN.md "Artifacts, v1 shapes"). Every cap is
-- enforced by clipping, never by erroring: rendering is best-effort
-- presentation, not protocol validation.
--
-- Every string value passes through line(), which strips control characters
-- (mainly newlines) and clips to MAX_BYTES. Tool results and question text are
-- player-typed data the model may echo back into an artifact; without this, a
-- value like "a\nSomeone: fake line" could forge an extra chat line (PLAN.md
-- open question 6, prompt injection). Factorio rich-text tags are left alone,
-- a supported feature of player.print and of a label caption.
--
-- MAX_BYTES is a safety net, not the cap a reader sees. The service clips
-- every cell to 160 characters before it sends the artifact, and 160
-- characters of Japanese or emoji take up to 640 bytes, so clipping at 160
-- bytes here would cut a legal answer to a third of its length. A client that
-- drives the protocol without the service meets the net instead.

local sprites = require("scripts.sprites")
local MAX_BYTES = 640

-- Cut to n bytes without leaving half a UTF-8 sequence behind. Player names
-- and team names carry non-ASCII often enough to matter. Walks back to the
-- lead byte of the sequence the cut lands in and keeps that sequence only if
-- all of it fits.
local function clip_bytes(s, n)
  if #s <= n then return s end
  local cut = n
  local i = cut
  while i > 0 do
    local b = s:byte(i)
    if b < 0x80 or b >= 0xC0 then break end -- reached the lead byte
    i = i - 1
  end
  if i > 0 then
    local lead = s:byte(i)
    local width = 1
    if lead >= 0xF0 then width = 4
    elseif lead >= 0xE0 then width = 3
    elseif lead >= 0xC0 then width = 2 end
    if i + width - 1 > cut then cut = i - 1 end
  end
  return s:sub(1, cut)
end

-- Scalars render as themselves; anything else says so. A table reaching
-- tostring would print its heap address, which differs on every peer, and
-- that string is stored on the question: mod storage would stop matching
-- between the server and its clients and the next checksum would desync them.
local function line(v)
  if v == nil then return "" end
  local kind = type(v)
  if kind ~= "string" and kind ~= "number" and kind ~= "boolean" then
    return "(unrenderable value)"
  end
  return clip_bytes(sprites.decorate((tostring(v):gsub("%c+", " "))), MAX_BYTES)
end

local function clip(t, n)
  local out = {}
  for i = 1, math.min(n, #t) do out[i] = t[i] end
  return out
end

local M = {}

--- The artifact's optional title, sanitised, or nil when it has none. Every
--- shape but `notice` may carry one.
function M.title_of(a)
  if type(a) ~= "table" or type(a.title) ~= "string" or a.title == "" then return nil end
  local title = line(a.title)
  if title == "" then return nil end
  return title
end

-- { shape="summary", lines={...} }: up to three lines.
function M.summary(a)
  local out = {}
  for _, l in ipairs(clip(a.lines or {}, 3)) do
    out[#out + 1] = line(l)
  end
  return out
end

-- { shape="notice", text="...", level="warning"|"confirmation" }: one line.
function M.notice(a)
  return { line(a.text or "") }
end

-- { shape="list", items={...} }: up to ten one-line rows.
function M.list(a)
  local out = {}
  for _, item in ipairs(clip(a.items or {}, 10)) do
    out[#out + 1] = "- " .. line(item)
  end
  return out
end

-- { shape="comparison", columns={"A","B"}, rows={{label=,a=,b=},...} }: up to five rows.
function M.comparison(a)
  local cols = a.columns or {}
  local out = { line((cols[1] or "A") .. "  vs  " .. (cols[2] or "B")) }
  for _, row in ipairs(clip(a.rows or {}, 5)) do
    out[#out + 1] = string.format("- %s: %s vs %s", line(row.label), line(row.a), line(row.b))
  end
  return out
end

-- { shape="table", columns={...}, rows={{...},...} }: up to five columns, eight rows.
function M.table(a)
  local grid = M.grid(a)
  local out = { table.concat(grid.columns, " | ") }
  for _, row in ipairs(grid.rows) do
    out[#out + 1] = table.concat(row, " | ")
  end
  return out
end

--- The `table` shape as a rectangle rather than joined lines, for the popup's
--- GUI table. Same clipping and sanitising as M.table, and every row is padded
--- to the column count so the GUI table stays rectangular.
function M.grid(a)
  local columns = {}
  for i, c in ipairs(clip(a.columns or {}, 5)) do columns[i] = line(c) end
  local rows = {}
  for _, row in ipairs(clip(a.rows or {}, 8)) do
    local cells = {}
    for i = 1, #columns do cells[i] = line(row[i]) end
    rows[#rows + 1] = cells
  end
  return { columns = columns, rows = rows }
end

return M
