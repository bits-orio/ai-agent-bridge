-- Renders one answered question's artifact into chat (ADR 0003: "the model
-- fills a shape; the companion renders it"). Chat is the one destination:
-- the question was asked there, and a window opening over whatever the
-- player was doing is an interruption.
--
-- Who reads the answer is the question's scope, fixed when it was asked
-- (docs/design/phase3-spec.md, part 2); scripts/audience.lua does the
-- printing, for answers and for the question echo alike.
--
-- render() returns what it rendered, {shape, title, body, lines}, so the
-- `answer` op can store the lines on the question and the `answers` op can
-- hand back exactly what the player saw without recomputing anything.

local shapes   = require("scripts.render_shapes")
local audience = require("scripts.audience")

-- The five v1 shapes, by name. An explicit set, not "does render_shapes have a
-- function of that name": the module also exports helpers, and an artifact
-- naming one of those must not reach it.
local SHAPES = { summary = true, notice = true, list = true, table = true, comparison = true }

local M = {}

--- One artifact -> {shape, title, body, lines}. `body` is the shape's own
--- lines; `lines` is what a reader sees, the title first when there is one.
local function compose(artifact)
  local shape = type(artifact) == "table" and artifact.shape or nil
  local body
  if type(shape) == "string" and SHAPES[shape] then
    body = shapes[shape](artifact)
  else
    shape = "notice"
    body = { "(unrecognised answer shape)" }
  end

  local title = shapes.title_of(artifact)
  local lines = {}
  if title then lines[1] = title end
  for _, text in ipairs(body) do lines[#lines + 1] = text end
  return { shape = shape, title = title, body = body, lines = lines }
end

--- "(new session)" or "(new session #iron)" when the service says this
--- question started one; nothing otherwise.
local function session_marker(artifact)
  local session = type(artifact) == "table" and artifact.session or nil
  if type(session) ~= "table" or session.fresh ~= true then return nil end
  if type(session.name) == "string" and session.name ~= "" then
    return "(new session #" .. session.name .. ")"
  end
  return "(new session)"
end

--- The chat text: the companion's name, then the channel tag the scope
--- provider gave (laid out like a player's own line, name then badge), then
--- the marker, a colon, and the lines.
local function chat_text(question, artifact, rendered)
  local head = audience.head(question)
  local marker = session_marker(artifact)
  if marker then head = head .. " " .. marker end
  return head .. ": " .. table.concat(rendered.lines, "\n")
end

--- Renders `artifact` for `question` (a row from scripts/questions.lua).
function M.render(question, artifact)
  local rendered = compose(artifact)
  audience.deliver(question, chat_text(question, artifact, rendered), artifact.to_asker == true)
  return rendered
end

return M
