-- AI Agent Bridge - settings.lua
-- Author: bits-orio
-- License: MIT
--
-- Runtime-global settings: apply server-wide, changeable without restarting.

data:extend({
    {
        type = "bool-setting",
        name = "aab-events-enabled",
        setting_type = "runtime-global",
        default_value = true,
        order = "a-a",
    },
    {
        -- The chat trigger (PLAN.md open question 2). Blank means off, so
        -- ordinary chat never reaches the model. Matched literally, spaces
        -- included, by scripts/chat.lua.
        type = "string-setting",
        name = "aab-chat-prefix",
        setting_type = "runtime-global",
        default_value = "",
        allow_blank = true,
        order = "a-b",
    },
    {
        -- Where an answer appears. "auto" puts the shapes that want columns
        -- or room into a popup and everything else in chat; see
        -- scripts/render.lua.
        type = "string-setting",
        name = "aab-answer-style",
        setting_type = "runtime-global",
        default_value = "auto",
        allowed_values = { "auto", "chat", "popup" },
        order = "a-c",
    },
})
