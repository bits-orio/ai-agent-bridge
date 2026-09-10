-- The answer popup (PLAN.md decision 3, "Chat rendering first, a popup frame
-- second"). One frame per player in player.gui.screen, auto-centred, with a
-- draggable titlebar, a close button and either a column of labels or a GUI
-- table for the `table` shape.
--
-- API names verified against the official docs (fetched 2026-09-10):
-- LuaGui::screen ("For showing a GUI somewhere on the entire screen"),
-- LuaGuiElement::add with types frame, flow, label, table, line and
-- sprite-button, LuaGuiElement::auto_center, drag_target,
-- ignored_by_interaction, destroy, LuaPlayer::opened,
-- defines.events.on_gui_click (fields element, player_index) and
-- on_gui_closed. The vanilla style and sprite names used here
-- (frame_title, draggable_space_header, frame_action_button, utility/close,
-- default-bold) are the ones a shipping 2.0 mod uses.

local shapes = require("scripts.render_shapes")

local FRAME = "aab_answer_frame"
local CLOSE = "aab_answer_close"
local DEFAULT_TITLE = "AI Agent Bridge"
local MAX_LABEL_WIDTH = 540

local M = {}

local function existing(player)
  local frame = player.gui.screen[FRAME]
  if frame and frame.valid then return frame end
  return nil
end

--- Closes this player's answer popup if one is open.
function M.close(player)
  local frame = existing(player)
  if frame then frame.destroy() end
end

local function add_titlebar(frame, caption)
  local bar = frame.add{ type = "flow", direction = "horizontal" }
  bar.drag_target = frame
  bar.style.vertical_align = "center"
  bar.style.bottom_margin = 4

  local title = bar.add{ type = "label", caption = caption, style = "frame_title" }
  title.ignored_by_interaction = true

  local spacer = bar.add{ type = "empty-widget", style = "draggable_space_header" }
  spacer.style.horizontally_stretchable = true
  spacer.style.height = 24
  spacer.drag_target = frame

  bar.add{
    type    = "sprite-button",
    name    = CLOSE,
    sprite  = "utility/close",
    style   = "frame_action_button",
    tooltip = "Close",
  }
end

local function add_label(parent, text)
  local label = parent.add{ type = "label", caption = text }
  label.style.single_line = false
  label.style.maximal_width = MAX_LABEL_WIDTH
  return label
end

-- The `table` shape as a real GUI table. Returns false when the artifact has
-- no columns, so the caller falls back to plain lines.
local function add_grid(parent, artifact)
  local grid = shapes.grid(artifact)
  if #grid.columns == 0 then return false end

  local gui_table = parent.add{
    type                  = "table",
    column_count          = #grid.columns,
    draw_horizontal_lines = true,
  }
  gui_table.style.horizontal_spacing = 12
  gui_table.style.vertical_spacing = 2

  for _, column in ipairs(grid.columns) do
    local header = gui_table.add{ type = "label", caption = column }
    header.style.font = "default-bold"
  end
  for _, row in ipairs(grid.rows) do
    for index = 1, #grid.columns do
      gui_table.add{ type = "label", caption = row[index] or "" }
    end
  end
  return true
end

local function build(player, artifact, rendered)
  M.close(player)
  local frame = player.gui.screen.add{ type = "frame", name = FRAME, direction = "vertical" }
  frame.auto_center = true

  add_titlebar(frame, rendered.title or DEFAULT_TITLE)
  frame.add{ type = "line" }

  local body = frame.add{ type = "flow", direction = "vertical" }
  body.style.top_margin = 6
  if not (rendered.shape == "table" and add_grid(body, artifact)) then
    for _, text in ipairs(rendered.body) do add_label(body, text) end
    if #rendered.body == 0 then add_label(body, "(no answer content)") end
  end

  -- Esc closes it, the way every vanilla window behaves.
  player.opened = frame
end

--- Shows one artifact to one connected player. Returns false if the frame
--- could not be built, so the caller falls back to chat and the answer is
--- never lost to a GUI problem.
function M.show(player, artifact, rendered)
  local built = pcall(build, player, artifact, rendered)
  if not built then
    M.close(player)
    log("[ai-agent-bridge] answer popup failed, falling back to chat")
    return false
  end
  return true
end

function M.register()
  script.on_event(defines.events.on_gui_click, function(e)
    if not (e.element and e.element.valid and e.element.name == CLOSE) then return end
    local player = game.get_player(e.player_index)
    if player then M.close(player) end
  end)

  script.on_event(defines.events.on_gui_closed, function(e)
    if not (e.element and e.element.valid and e.element.name == FRAME) then return end
    e.element.destroy()
  end)
end

return M
