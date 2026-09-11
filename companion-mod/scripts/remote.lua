-- AI Agent Bridge - scripts/remote.lua
-- Author: bits-orio
-- License: MIT
--
-- The frozen remote interface CONTEXT.md calls the "Protocol" companion:
-- ai-agent-bridge-v1. ask() lets any other mod submit a question the same
-- way /ask does; get_event_id() lets any mod resolve on_answer.
--
-- The event id is generated fresh every load into a module-local, never
-- storage: a script.generate_event_name() id is only valid to raise in the
-- session that generated it, so persisting it and skipping regeneration
-- after a load would leave a stale id that errors on raise_event. Consumers
-- fetch it every session through get_event_id.

local questions = require("scripts.questions")

local INTERFACE = "ai-agent-bridge-v1"

local on_answer_event = script.generate_event_name()

local M = {}

--- Raises on_answer for any subscriber (CONTEXT.md "Answer"). `data` is
--- whatever shape the caller wants subscribers to see; the aab-rpc `answer`
--- op and the Phase 0 `write` test op both call this.
function M.raise_answer(data)
  script.raise_event(on_answer_event, data)
end

local function get_event_id(name)
  if name == "on_answer" then return on_answer_event end
  return nil
end

function M.register()
  remote.add_interface(INTERFACE, {
    ask = function(spec) return questions.ask(spec) end,
    get_event_id = get_event_id,
  })
end

return M
