-- Renders one answered question's artifact (ADR 0003: "the model fills a
-- shape; the companion renders it"). Two destinations, chat and the popup
-- frame in scripts/popup.lua, chosen by the aab-answer-style setting.
--
-- render() returns what it rendered, {shape, title, body, lines}, so the
-- `answer` op can store the lines on the question and the `answers` op can
-- hand back exactly what the player saw without recomputing anything.

local shapes        = require("scripts.render_shapes")
local popup         = require("scripts.popup")
local player_lookup = require("scripts.player_lookup")

-- The five v1 shapes, by name. An explicit set, not "does render_shapes have a
-- function of that name": the module also exports helpers, and an artifact
-- naming one of those must not reach it.
local SHAPES = { summary = true, notice = true, list = true, table = true, comparison = true }

-- `auto`: a shape that wants columns or a long list earns the popup.
local POPUP_SHAPES = { table = true, comparison = true }
local LIST_POPUP_THRESHOLD = 3

local M = {}

local function asker(question)
  local player = player_lookup.by_index(question.player_index)
  if player and player.valid and player.connected then return player end
  return nil
end

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

local function style_for(rendered)
  local setting = settings.global["aab-answer-style"]
  local style = setting and setting.value or "auto"
  if style == "chat" or style == "popup" then return style end
  if POPUP_SHAPES[rendered.shape] then return "popup" end
  if rendered.shape == "list" and #rendered.body > LIST_POPUP_THRESHOLD then return "popup" end
  return "chat"
end

--- Renders `artifact` for the asker of `question` (a row from
--- scripts/questions.lua). The popup needs a connected asker; without one the
--- answer goes to chat, to that player if they are still around and to
--- everyone if they are not.
function M.render(question, artifact)
  local rendered = compose(artifact)
  local player = asker(question)

  if player and style_for(rendered) == "popup" and popup.show(player, artifact, rendered) then
    return rendered
  end

  local text = "[AI Agent Bridge] " .. table.concat(rendered.lines, "\n")
  if player then
    player.print(text)
  else
    game.print(text)
  end
  return rendered
end

return M
