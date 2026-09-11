-- Renders one answered question's artifact into chat (ADR 0003: "the model
-- fills a shape; the companion renders it"). Chat is the one destination:
-- the question was asked there, and a window opening over whatever the
-- player was doing is an interruption.
--
-- Who reads the answer is the question's scope, fixed when it was asked
-- (docs/design/phase3-spec.md, part 2): a private question prints to its
-- audience, a global one to the server, or to the asker alone when the
-- aab-answer-audience setting says so.
--
-- render() returns what it rendered, {shape, title, body, lines}, so the
-- `answer` op can store the lines on the question and the `answers` op can
-- hand back exactly what the player saw without recomputing anything.

local shapes        = require("scripts.render_shapes")
local player_lookup = require("scripts.player_lookup")

local NAME = "[AI Agent Bridge]"

-- The five v1 shapes, by name. An explicit set, not "does render_shapes have a
-- function of that name": the module also exports helpers, and an artifact
-- naming one of those must not reach it.
local SHAPES = { summary = true, notice = true, list = true, table = true, comparison = true }

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
  local head = NAME
  if type(question.tag) == "string" and question.tag ~= "" then head = head .. " " .. question.tag end
  local marker = session_marker(artifact)
  if marker then head = head .. " " .. marker end
  return head .. ": " .. table.concat(rendered.lines, "\n")
end

local function print_to_players(indices, text)
  local reached = false
  for _, index in ipairs(indices) do
    local player = game.get_player(index)
    if player and player.valid and player.connected then
      player.print(text)
      reached = true
    end
  end
  return reached
end

--- Prints to the question's audience. A private question whose audience
--- has gone (a force removed since it was asked) falls back to the asker
--- alone: privacy over reach.
local function deliver(question, text)
  if question.private then
    local audience = type(question.audience) == "table" and question.audience or {}
    if audience.force then
      local force = game.forces[audience.force]
      if force and force.valid then force.print(text) return end
    elseif audience.players and print_to_players(audience.players, text) then
      return
    end
    local player = asker(question)
    if player then player.print(text) end
    return
  end

  local setting = settings.global["aab-answer-audience"]
  local mode = setting and setting.value or "server"
  if mode == "asker" then
    local player = asker(question)
    if player then player.print(text) return end
  end
  game.print(text)
end

--- Renders `artifact` for `question` (a row from scripts/questions.lua).
function M.render(question, artifact)
  local rendered = compose(artifact)
  deliver(question, chat_text(question, artifact, rendered))
  return rendered
end

return M
