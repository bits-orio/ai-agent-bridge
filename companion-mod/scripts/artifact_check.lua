-- AI Agent Bridge - scripts/artifact_check.lua
-- Author: bits-orio
-- License: MIT
--
-- Artifact validation for the `answer` op. The renderer clips and sanitises,
-- it does not judge: anything that reaches it is presented somehow. This
-- module is the judge, so a client that sends the wrong shape of data gets
-- `bad_artifact` back and its question stays pending and answerable, instead
-- of being marked answered with nothing behind it.
--
-- The protocol is open (CONTEXT.md: "Any client that speaks it can drive the
-- companion"), so every field is checked here rather than trusted because the
-- reference service happens to get it right. That includes an artifact that
-- is well formed and says nothing: marked answered, it would leave the asker
-- a blank chat line and the question closed.

local shapes   = require("scripts.render_shapes")
local richtext = require("scripts.richtext")

local SCALARS = { string = true, number = true, boolean = true }
local CAPS = shapes.CAPS

local function is_scalar(v) return SCALARS[type(v)] == true end

--- A table used as a JSON array: empty, or holding index 1. A JSON object
--- reaching an array field is a caller's mistake, not an empty list.
local function is_array(v)
  return type(v) == "table" and (next(v) == nil or v[1] ~= nil)
end

local function array_of_scalars(v)
  if not is_array(v) then return false end
  for _, entry in ipairs(v) do
    if not is_scalar(entry) then return false end
  end
  return true
end

local function array_of_arrays(v)
  if not is_array(v) then return false end
  for _, row in ipairs(v) do
    if not array_of_scalars(row) then return false end
  end
  return true
end

local function array_of_pairs(v)
  if not is_array(v) then return false end
  for _, row in ipairs(v) do
    if type(row) ~= "table" then return false end
    for _, key in ipairs({ "label", "a", "b" }) do
      if row[key] ~= nil and not is_scalar(row[key]) then return false end
    end
  end
  return true
end

-- One check per shape. Each returns nil when the artifact is usable, or the
-- sentence the caller gets back in the reply's `m` field.
local SHAPES = {
  summary = function(a)
    if not array_of_scalars(a.lines) then return "summary needs lines, an array of strings" end
  end,
  notice = function(a)
    if not is_scalar(a.text) then return "notice needs text, a string" end
    if a.level ~= nil and type(a.level) ~= "string" then return "notice level must be a string" end
  end,
  list = function(a)
    if not array_of_scalars(a.items) then return "list needs items, an array of strings" end
  end,
  table = function(a)
    if not array_of_scalars(a.columns) then return "table needs columns, an array of strings" end
    if not array_of_arrays(a.rows) then return "table needs rows, an array of arrays of strings" end
  end,
  comparison = function(a)
    if not array_of_scalars(a.columns) then return "comparison needs columns, an array of two strings" end
    if not array_of_pairs(a.rows) then return "comparison needs rows, an array of {label, a, b} objects" end
  end,
}

--- True for a tag that draws nothing by itself: a colour or font span's
--- ends, or a tag naming nothing, which the renderer closes up.
local function draws_nothing(tag)
  return richtext.is_style(tag) or tag:match("^[%w%-]+=%s*$") ~= nil
end

--- True when `v` shows the reader something: any number or boolean, or a
--- string with a character left once spaces, control characters and the
--- tags that draw nothing are gone.
local function visible(v)
  if type(v) == "number" or type(v) == "boolean" then return true end
  if type(v) ~= "string" then return false end
  local bare = v:gsub("%[([^%[%]]*)%]", function(tag)
    if draws_nothing(tag) then return "" end
  end)
  return bare:find("[^%s%c]") ~= nil
end

--- The first `n` entries of an array, as the renderer clips one.
local function first(list, n)
  local out = {}
  for i = 1, math.min(n, #list) do out[#out + 1] = list[i] end
  return out
end

-- What each shape prints of its own text, within the renderer's caps. The
-- labels a shape adds by itself, a comparison's A and B or a list's dash,
-- are not the answer and do not count.
local SHOWN = {
  summary = function(a) return first(a.lines, CAPS.summary) end,
  notice = function(a) return { a.text } end,
  list = function(a) return first(a.items, CAPS.list) end,
  table = function(a)
    local out = first(a.columns, CAPS.table_columns)
    local width = #out
    for _, row in ipairs(first(a.rows, CAPS.table_rows)) do
      for _, cell in ipairs(first(row, width)) do out[#out + 1] = cell end
    end
    return out
  end,
  comparison = function(a)
    local out = first(a.columns, 2)
    for _, row in ipairs(first(a.rows, CAPS.comparison)) do
      for _, key in ipairs({ "label", "a", "b" }) do out[#out + 1] = row[key] end
    end
    return out
  end,
}

--- True when nothing the artifact says would reach the chat line: its title
--- and everything its shape prints are blank.
local function says_nothing(a)
  if visible(a.title) then return false end
  for _, value in ipairs(SHOWN[a.shape](a)) do
    if visible(value) then return false end
  end
  return true
end

local M = {}

--- nil when this artifact can be rendered, or a sentence saying what is wrong
--- with it. The caller turns that into a bad_artifact reply.
function M.problem(a)
  if type(a) ~= "table" then return "artifact must be an object" end
  if type(a.shape) ~= "string" then return "artifact needs shape, a string" end
  local check = SHAPES[a.shape]
  if not check then
    -- Clipped: the shape name came from a caller, and the reply it lands in
    -- has a byte cap of its own.
    return "unknown shape: " .. a.shape:sub(1, 40) .. " (use summary, notice, list, table or comparison)"
  end
  if a.title ~= nil and type(a.title) ~= "string" then return "title must be a string" end
  if a.session ~= nil and type(a.session) ~= "table" then return "session must be an object" end
  if a.to_asker ~= nil and type(a.to_asker) ~= "boolean" then return "to_asker must be a boolean" end
  local wrong = check(a)
  if wrong then return wrong end
  if says_nothing(a) then
    return a.shape .. " would print nothing: its title and every line, item or cell are blank"
  end
  return nil
end

return M
