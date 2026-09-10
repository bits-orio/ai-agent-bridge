-- Renders one answered question's artifact to chat (ADR 0003: "the model
-- fills a shape; the companion renders it"). Chat only for now -- a popup
-- frame is Phase 2; render_popup below is a stub so callers already have a
-- stable hook to switch to once it exists.

local shapes = require("scripts.render_shapes")

local M = {}

local function target(question)
  local player = question.player_index and game.get_player(question.player_index)
  if player and player.valid and player.connected then return player end
  return nil
end

--- Renders `artifact` (one of PLAN.md's five shapes) for the asker of
--- `question` (a row from scripts/questions.lua), or to everyone if the
--- asker is no longer around to receive it.
function M.render(question, artifact)
  local lines
  if type(artifact) ~= "table" or type(shapes[artifact.shape]) ~= "function" then
    lines = { "(unrecognised answer shape)" }
  else
    lines = shapes[artifact.shape](artifact)
  end
  local text = "[AI Agent Bridge] " .. table.concat(lines, "\n")
  local player = target(question)
  if player then
    player.print(text)
  else
    game.print(text)
  end
end

--- Popup GUI frame for an artifact. Not built yet (PLAN.md Phase 2); named
--- here so render.render's callers already have a stable hook to switch to.
function M.render_popup(_question, _artifact)
end

return M
