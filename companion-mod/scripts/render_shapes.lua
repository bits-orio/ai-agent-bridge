-- AI Agent Bridge - scripts/render_shapes.lua
-- Author: bits-orio
-- License: MIT
--
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
-- every cell to 320 characters (MaxCellChars) before it sends the artifact,
-- and 320 characters of Japanese or emoji take up to 1280 bytes, so clipping
-- at 320 bytes here would cut a legal answer to a quarter of its length. The
-- net fires on a line the decoration below carried past it, or on a client
-- that drives the protocol without the service; when it does, the line is cut
-- on a character boundary and ends with an ellipsis, the way the service ends
-- its own cuts, so a reader can tell a cut line from one that simply ends.

local sprites       = require("scripts.sprites")
local richtext      = require("scripts.richtext")
local player_colors = require("scripts.player_colors")
local labels        = require("scripts.labels")
local text_clip     = require("scripts.text_clip")

local MAX_BYTES = 1280
local ELLIPSIS = text_clip.ELLIPSIS

-- The count caps the service enforces before it sends an artifact, one for one
-- (service/internal/agent/artifact.go, the Go name beside each), so a client
-- that drives the protocol without the service meets the same limits.
local SUMMARY_LINES   = 3  -- MaxSummaryLines
local LIST_ITEMS      = 10 -- MaxListItems
local TABLE_COLUMNS   = 5  -- MaxTableColumns
local TABLE_ROWS      = 8  -- MaxTableRows
local COMPARISON_ROWS = 5  -- MaxComparisonRows

--- `text` within MAX_BYTES. Over it, the text is cut on a character
--- boundary, a tag the cut tore is dropped, open colour and font spans are
--- closed, and an ellipsis goes in before the closers, all of it within
--- MAX_BYTES: a torn tag would make Factorio draw the whole line raw.
local function fit_line(text)
  return richtext.fit(text, MAX_BYTES, ELLIPSIS)
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
  -- Repaired, decorated, then fitted. Repaired first: a sprite tag the
  -- model wrote for a sprite the game does not have prints the whole line
  -- raw, so it is fixed before anything else is added. The labels and
  -- sprites decorated in here can carry a line past MAX_BYTES that the
  -- service kept within it, and fit_line never cuts inside one of their tags.
  local flat = tostring(v):gsub("%c+", " ")
  return fit_line(player_colors.decorate(labels.decorate(sprites.decorate(sprites.repair(flat)))))
end

local function clip(t, n)
  local out = {}
  for i = 1, math.min(n, #t) do out[i] = t[i] end
  return out
end

local M = {}

--- The count caps by shape. The artifact check reads them so it judges an
--- artifact by what the renderer would show of it.
M.CAPS = {
  summary = SUMMARY_LINES, list = LIST_ITEMS, comparison = COMPARISON_ROWS,
  table_columns = TABLE_COLUMNS, table_rows = TABLE_ROWS,
}

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
  for _, l in ipairs(clip(a.lines or {}, SUMMARY_LINES)) do
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
  for _, item in ipairs(clip(a.items or {}, LIST_ITEMS)) do
    out[#out + 1] = "- " .. line(item)
  end
  return out
end

-- { shape="comparison", columns={"A","B"}, rows={{label=,a=,b=},...} }: up to five rows.
function M.comparison(a)
  local cols = a.columns or {}
  local function name(value, fallback)
    local text = line(value)
    if text == "" then return fallback end
    return text
  end
  local out = { name(cols[1], "A") .. "  vs  " .. name(cols[2], "B") }
  for _, row in ipairs(clip(a.rows or {}, COMPARISON_ROWS)) do
    out[#out + 1] = string.format("- %s: %s vs %s", line(row.label), line(row.a), line(row.b))
  end
  return out
end

-- Tables are set in the mod's own monospace font (data.lua declares
-- aab-mono from the game's default-mono face at the chat size), because
-- columns line up only when every character is the same width and the chat
-- font is not: on the live server every table printed with its column
-- boundaries wandering from row to row. Each column is padded to its widest
-- cell, numbers sit right-aligned under a right-aligned heading, and a rule
-- runs under the header. Widths are measured on what is visible: a colour
-- or font tag is nothing, any other tag draws an icon about one cell wide.
local MONO_FONT = "aab-mono"
local MAX_COLUMN_WIDTH = 40

local function mono_font_exists()
  return prototypes ~= nil and prototypes.font ~= nil and prototypes.font[MONO_FONT] ~= nil
end

local function visible_width(s)
  local icons = 0
  local text = s:gsub("%[([^%[%]]*)%]", function(tag)
    if not richtext.is_style(tag) then icons = icons + 1 end
    return ""
  end)
  -- Characters, not bytes: every UTF-8 sequence has exactly one lead byte,
  -- and the game's Lua has no utf8 library to count them with.
  local _, chars = text:gsub("[^\128-\191]", "")
  return chars + icons
end

local function is_numeric(s)
  local text = s:gsub("%[[^%[%]]*%]", "")
  return text:match("^[-+]?%d[%d,%.]*[%a%%/]*$") ~= nil
end

local function mono(cells)
  return "[font=" .. MONO_FONT .. "]" .. (table.concat(cells, " | "):gsub("%s+$", "")) .. "[/font]"
end

-- { shape="table", columns={...}, rows={{...},...} }: up to five columns, eight rows.
function M.table(a)
  local grid = M.grid(a)
  if not mono_font_exists() then
    local out = { table.concat(grid.columns, " | ") }
    for _, row in ipairs(grid.rows) do out[#out + 1] = table.concat(row, " | ") end
    return out
  end
  local widths, numeric = {}, {}
  for i, column in ipairs(grid.columns) do
    widths[i] = visible_width(column)
    numeric[i] = true
  end
  for _, row in ipairs(grid.rows) do
    for i, cell in ipairs(row) do
      widths[i] = math.max(widths[i], visible_width(cell))
      if cell ~= "" and not is_numeric(cell) then numeric[i] = false end
    end
  end
  for i in ipairs(widths) do widths[i] = math.min(widths[i], MAX_COLUMN_WIDTH) end
  local function pad(cell, i)
    local fill = widths[i] - visible_width(cell)
    if fill <= 0 then return cell end
    if numeric[i] then return string.rep(" ", fill) .. cell end
    return cell .. string.rep(" ", fill)
  end
  local header, rule = {}, {}
  for i, column in ipairs(grid.columns) do
    header[i] = pad(column, i)
    rule[i] = string.rep("-", widths[i])
  end
  local out = { mono(header), mono({ table.concat(rule, "-+-") }) }
  for _, row in ipairs(grid.rows) do
    local cells = {}
    for i = 1, #grid.columns do cells[i] = pad(row[i], i) end
    out[#out + 1] = mono(cells)
  end
  return out
end

--- The `table` shape as a rectangle rather than joined lines, for the popup's
--- GUI table. Same clipping and sanitising as M.table, and every row is padded
--- to the column count so the GUI table stays rectangular.
function M.grid(a)
  local columns = {}
  for i, c in ipairs(clip(a.columns or {}, TABLE_COLUMNS)) do columns[i] = line(c) end
  local rows = {}
  for _, row in ipairs(clip(a.rows or {}, TABLE_ROWS)) do
    local cells = {}
    for i = 1, #columns do cells[i] = line(row[i]) end
    rows[#rows + 1] = cells
  end
  return { columns = columns, rows = rows }
end

return M
