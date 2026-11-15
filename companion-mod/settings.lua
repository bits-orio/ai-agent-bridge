-- AI Agent Bridge - settings.lua
-- Author: bits-orio
-- License: MIT
--
-- Runtime-global settings apply server-wide and change without restarting.
-- The two startup settings at the top are read when the mod loads.

data:extend({
    {
        -- Whether the mod takes the /ask command name. /aab-ask always
        -- exists; turn this off when another mod's /ask must stay. Read
        -- when the mod loads, so it is a startup setting.
        type = "bool-setting",
        name = "aab-claim-ask-command",
        setting_type = "startup",
        default_value = true,
        order = "b-a",
    },
    {
        -- The Phase 0 transport checks (ping, big, write, pcall_test), for
        -- the test rigs only. Off, they answer bad_op.
        type = "bool-setting",
        name = "aab-selftest-ops",
        setting_type = "startup",
        default_value = false,
        order = "b-b",
    },
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
        -- How much the mod teaches players about asking; see
        -- scripts/ask_hints.lua. echo names the command in the question
        -- echo, all adds one private hint per player who has not asked, off
        -- leaves the echo as it was.
        type = "string-setting",
        name = "aab-ask-hints",
        setting_type = "runtime-global",
        default_value = "echo",
        allowed_values = { "all", "echo", "off" },
        order = "a-c2",
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
    {
        -- How the bot sounds. Fixed on each question when it is asked and
        -- read by the service, which owns what it means; see
        -- scripts/personality.lua. Blank leaves it to the operator's config.
        type = "string-setting",
        name = "aab-personality",
        setting_type = "runtime-global",
        default_value = "",
        allow_blank = true,
        auto_trim = true,
        order = "a-f",
    },
    {
        -- Whether the service may print a line nobody asked for (a milestone
        -- toast, the `say` op). Answers to questions never read it.
        type = "bool-setting",
        name = "aab-allow-say",
        setting_type = "runtime-global",
        default_value = true,
        order = "a-g",
    },
})
