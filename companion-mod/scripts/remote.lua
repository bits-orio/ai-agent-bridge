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
local ask_rate  = require("scripts.ask_rate")

local INTERFACE = "ai-agent-bridge-v1"

local on_answer_event = script.generate_event_name()
-- A second id, for the unprompted lines of scripts/say.lua. Generated after
-- on_answer's so nothing that already holds that id sees it move.
local on_say_event = script.generate_event_name()

local M = {}

--- Raises on_answer for any subscriber (CONTEXT.md "Answer"). `data` is
--- whatever shape the caller wants subscribers to see; the aab-rpc `answer`
--- op calls this.
function M.raise_answer(data)
  script.raise_event(on_answer_event, data)
end

--- Raises on_say for any subscriber: {artifact, shape, lines} of one line the
--- service printed on its own. Called by the aab-rpc `say` op.
function M.raise_say(data)
  script.raise_event(on_say_event, data)
end

local function get_event_id(name)
  if name == "on_answer" then return on_answer_event end
  if name == "on_say" then return on_say_event end
  return nil
end

function M.register()
  remote.add_interface(INTERFACE, {
    -- A mod's ask() is rate limited as a whole, since no player is behind
    -- it; nil is the same answer a bad spec gets.
    ask = function(spec)
      if not ask_rate.allow_remote() then return nil end
      return questions.ask(spec)
    end,
    get_event_id = get_event_id,
  })
end

return M
