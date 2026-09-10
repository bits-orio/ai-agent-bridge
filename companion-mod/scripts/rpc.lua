-- The aab-rpc-v1 protocol command (CONTEXT.md "Protocol", PLAN.md's op
-- table). One JSON object in, one JSON object out via rcon.print -- always
-- exactly one reply, {ok=true,r=...} or {ok=false,e=...,m=...}. Phase 1 ops
-- live here; Phase 0 diagnostic ops live in scripts/rpc_selftest.lua and are
-- merged into the same dispatch table below.
--
-- Invariant (CONTEXT.md): this command never writes storage except the
-- `answer` op (via questions.mark_answered and questions.record_render) and
-- the Phase 0 `write` op. `answers` reads back what `answer` stored.

local probe          = require("scripts.probe")
local questions      = require("scripts.questions")
local ask_command    = require("scripts.ask_command")
local remote_iface   = require("scripts.remote")
local render         = require("scripts.render")
local events         = require("scripts.events")
local selftest       = require("scripts.rpc_selftest")

local PROTOCOL_VERSION = 1
local MAX_RESULT_BYTES = 8000

local function ok_reply(r) return { ok = true, r = r } end
local function err_reply(e, m) return { ok = false, e = e, m = m } end

local OPS = {}

function OPS.status(_req, _cmd)
  return ok_reply({
    protocol     = PROTOCOL_VERSION,
    mod_version  = script.active_mods["ai-agent-bridge"],
    tick         = game.tick,
    player_count = #game.connected_players,
    pending      = questions.pending_count(),
    ask_command  = ask_command.active_name(),
  })
end

function OPS.tools(_req, _cmd)
  return ok_reply(probe.catalog())
end

function OPS.call(req, _cmd)
  return probe.call(req.i, req.f, req.a)
end

function OPS.poll(req, _cmd)
  return ok_reply(questions.poll(req.after))
end

function OPS.answer(req, _cmd)
  if type(req.qid) ~= "number" or type(req.artifact) ~= "table" then
    return err_reply("bad_json", "answer requires qid (number) and artifact (object)")
  end
  local question = questions.mark_answered(req.qid)
  if not question then
    return err_reply("no_question", "no question with id " .. tostring(req.qid))
  end
  local rendered = render.render(question, req.artifact)
  questions.record_render(question, rendered)
  events.write("answer", { qid = req.qid, shape = rendered.shape })
  remote_iface.raise_answer({
    qid = req.qid, question = question.text, artifact = req.artifact,
    shape = rendered.shape, lines = rendered.lines,
  })
  return ok_reply(true)
end

-- Pure read: what the companion rendered for questions already answered. The
-- service uses it to confirm an answer reached the game, and the end-to-end
-- harness asserts on the lines a player would have seen.
function OPS.answers(req, _cmd)
  return ok_reply(questions.answers(req.after))
end

for name, fn in pairs(selftest.ops) do
  OPS[name] = fn
end

local M = {}

--- The aab-rpc command handler. `cmd` is Factorio's CustomCommandData.
function M.handle(cmd)
  local parsed_ok, req = pcall(helpers.json_to_table, cmd.parameter or "")
  local reply

  if not parsed_ok or type(req) ~= "table" then
    reply = err_reply("bad_json", "parameter must be one JSON object")
  elseif req.v ~= 1 then
    reply = err_reply("bad_version", "expected v=1, got " .. tostring(req.v))
  else
    local op = OPS[req.op]
    if not op then
      reply = err_reply("bad_op", tostring(req.op))
    else
      local ran_ok, result = pcall(op, req, cmd)
      reply = ran_ok and result or err_reply("provider_error", tostring(result))
    end
  end

  -- A provider may return something table_to_json cannot encode (a LuaObject
  -- passes through remote.call intact). Refuse it as bad_result rather than
  -- letting the encode error swallow the one reply this command owes.
  local encoded, json = pcall(helpers.table_to_json, reply)
  if not encoded then
    json = helpers.table_to_json(err_reply("bad_result", "result is not plain data: " .. tostring(json)))
  end

  -- "big" exists to measure the RCON transport's own size limit (Phase 0),
  -- so it must bypass this cap rather than be capped by it.
  local is_big = type(req) == "table" and req.op == "big"
  if not is_big and #json > MAX_RESULT_BYTES then
    json = helpers.table_to_json(err_reply("too_large", "result exceeds " .. MAX_RESULT_BYTES .. " bytes"))
  end

  rcon.print(json)
end

return M
