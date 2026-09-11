-- AI Agent Bridge - scripts/rpc.lua
-- Author: bits-orio
-- License: MIT
--
-- The aab-rpc-v1 protocol command (CONTEXT.md "Protocol", PLAN.md's op
-- table). One JSON object in, one JSON object out via rcon.print, always
-- exactly one reply, {ok=true,r=...} or {ok=false,e=...,m=...}. Phase 1 ops
-- live here; Phase 0 diagnostic ops live in scripts/rpc_selftest.lua and are
-- merged into the same dispatch table below.
--
-- Invariant (CONTEXT.md): this command never writes storage except the
-- `answer` op (via questions.record_answer, and only once rendering has
-- succeeded) and the Phase 0 `write` op. `answers` reads back what `answer`
-- stored. The three catalog ops live in scripts/rpc_catalog.lua and are merged
-- into the same dispatch table below.

local probe          = require("scripts.probe")
local questions      = require("scripts.questions")
local question_reads = require("scripts.question_reads")
local artifact_check = require("scripts.artifact_check")
local ask_command    = require("scripts.ask_command")
local remote_iface   = require("scripts.remote")
local render         = require("scripts.render")
local labels         = require("scripts.labels")
local events         = require("scripts.events")
local selftest       = require("scripts.rpc_selftest")
local catalog        = require("scripts.rpc_catalog")

local PROTOCOL_VERSION = 1
-- Reply caps by op. Tool results (call) and the legacy whole-catalog read
-- (tools) stay small because their bytes end up in the model's context;
-- catalog, question and answer reads are bookkeeping the service consumes,
-- so they get room to grow (RCON itself carries megabytes, TESTING.md 1.5).
-- The Phase 0 big op measures the transport and is never capped.
local DEFAULT_CAP = 65536
local CAPS = { call = 8000, tools = 8000, manifest = 32768 }

local function reply_cap(op)
  return CAPS[op] or DEFAULT_CAP
end

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
    last_id      = questions.last_id(),
    ask_command  = ask_command.active_name(),
  })
end

-- `a` is optional: probe.call substitutes the empty table for a missing one,
-- so a zero-argument tool called without arguments works.
function OPS.call(req, _cmd)
  return probe.call(req.i, req.f, req.a)
end

-- Pure read: what players call each force, from the labels providers. The
-- service reads it once per question and tells the model.
function OPS.labels(_req, _cmd)
  return ok_reply(labels.all())
end

function OPS.poll(req, _cmd)
  return ok_reply(question_reads.poll(req.after, req.limit))
end

-- The one op that writes storage, and the order matters: validate, render,
-- and only then mark the question answered. A rejected or unrenderable
-- artifact leaves the question pending, so the service can answer it again
-- rather than the asker being told nothing forever.
function OPS.answer(req, _cmd)
  if type(req.qid) ~= "number" or type(req.artifact) ~= "table" then
    return err_reply("bad_json", "answer requires qid (number) and artifact (object)")
  end
  local wrong = artifact_check.problem(req.artifact)
  if wrong then
    return err_reply("bad_artifact", wrong)
  end
  local question = questions.find(req.qid)
  if not question then
    return err_reply("no_question", "no question with id " .. tostring(req.qid))
  end
  -- Already answered: the reply to the first answer was lost in transit. Say
  -- yes again without rendering a second time, so a retry costs the asker
  -- nothing (CONTEXT.md invariant 2).
  if question.answered then
    return ok_reply(true)
  end

  local drawn, rendered = pcall(render.render, question, req.artifact)
  if not drawn then
    -- First line of the error only: the rest is a Lua traceback, and this
    -- reply has a byte cap.
    local why = tostring(rendered):match("^[^\n]*")
    return err_reply("bad_artifact", "the artifact could not be rendered: " .. why)
  end

  questions.record_answer(question, rendered)
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
  return ok_reply(question_reads.answers(req.after, req.limit))
end

for _, module in ipairs({ catalog, selftest }) do
  for name, fn in pairs(module.ops) do
    OPS[name] = fn
  end
end

local M = {}

--- The aab-rpc command handler. `cmd` is Factorio's CustomCommandData.
function M.handle(cmd)
  -- The service's command, over RCON, and the server console: both arrive
  -- with no player_index. A player typing it into their own console gets a
  -- one-line refusal and nothing runs. Without this a player could forge an
  -- answer in the bot's name, run any tool, or build a multi-megabyte reply
  -- on the server thread.
  if cmd.player_index then
    local player = game.get_player(cmd.player_index)
    if player and player.valid then player.print("[AI Agent Bridge] /aab-rpc is the service's command, over RCON.") end
    return
  end

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
  local op_name = type(req) == "table" and req.op or nil
  local cap = reply_cap(op_name)
  if op_name ~= "big" and #json > cap then
    json = helpers.table_to_json(err_reply("too_large", "result exceeds " .. cap .. " bytes"))
  end

  rcon.print(json)
end

return M
