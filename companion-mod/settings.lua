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
        -- Reserved for a Phase 2 chat trigger (PLAN.md open question 2): a
        -- prefix that turns an ordinary chat message into a question. Off
        -- (blank) by default so ordinary chat never reaches the model.
        -- Unused by any control-stage code until that lands.
        type = "string-setting",
        name = "aab-chat-prefix",
        setting_type = "runtime-global",
        default_value = "",
        allow_blank = true,
        order = "a-b",
    },
})
