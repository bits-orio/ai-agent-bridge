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
        -- Who reads a global answer: everyone on the server (the session is
        -- shared, so others can follow up) or the asker alone. A private
        -- question always prints to its audience regardless; see
        -- scripts/render.lua.
        type = "string-setting",
        name = "aab-answer-audience",
        setting_type = "runtime-global",
        default_value = "server",
        allowed_values = { "server", "asker" },
        order = "a-c",
    },
})
