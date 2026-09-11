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
    {
        -- Rate limits on asking, checked before anything else happens; see
        -- scripts/ask_rate.lua. Zero turns a limit off.
        type = "int-setting",
        name = "aab-ask-cooldown-seconds",
        setting_type = "runtime-global",
        default_value = 5,
        minimum_value = 0,
        maximum_value = 3600,
        order = "a-d",
    },
    {
        type = "int-setting",
        name = "aab-asks-per-minute",
        setting_type = "runtime-global",
        default_value = 30,
        minimum_value = 0,
        maximum_value = 10000,
        order = "a-e",
    },
})
