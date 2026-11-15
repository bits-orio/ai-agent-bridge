-- AI Agent Bridge - scripts/say.lua
-- Author: bits-orio
-- License: MIT
--
-- The `say` op of aab-rpc-v1: one unprompted line the service prints for
-- everyone, a milestone toast that answers no question. It is an answer's
-- artifact without the answer: validated by artifact_check, composed by the
-- same renderer, printed through audience's guarded print, and always global.
-- There is no question, so no scope, no audience, no tag and no session
-- marker, and nothing is stored: the op never touches `storage` or the
-- question ring.
--
-- Merged into rpc.lua's op table at load time, the way rpc_catalog is.

local artifact_check = require("scripts.artifact_check")
local render         = require("scripts.render")
local audience       = require("scripts.audience")
local events         = require("scripts.events")
local remote_iface   = require("scripts.remote")

local M = { ops = {} }

local function err_reply(e, m) return { ok = false, e = e, m = m } end

--- Whether the aab-allow-say setting lets the service speak unprompted. A
--- missing setting reads as allowed: the default is on.
local function allowed()
  local setting = settings.global["aab-allow-say"]
  return setting == nil or setting.value ~= false
end

function M.ops.say(req, _cmd)
  if not allowed() then
    return err_reply("bad_op", "say is off: the aab-allow-say setting is false")
  end
  local artifact = req.artifact
  if type(artifact) ~= "table" then
    return err_reply("bad_artifact", "say requires artifact (object)")
  end
  -- to_asker means "print to whoever asked", and nobody asked.
  if artifact.to_asker ~= nil then
    return err_reply("bad_artifact", "to_asker makes no sense for say: it has no asker")
  end
  local wrong = artifact_check.problem(artifact)
  if wrong then return err_reply("bad_artifact", wrong) end

  -- Composing is the only step that can fault the artifact; delivery is
  -- guarded on its own, so a failed print is never reported as bad_artifact.
  local drawn, rendered = pcall(render.compose, artifact)
  if not drawn then
    return err_reply("bad_artifact", "the artifact could not be rendered: " .. tostring(rendered):match("^[^\n]*"))
  end

  audience.print_all(audience.NAME .. ": " .. table.concat(rendered.lines, "\n"))
  -- The shape only, never the text: the history the service tails keeps what
  -- was said in its own records, and the log should not hold chat.
  events.write("say", { shape = rendered.shape })
  -- Under its own pcall, like on_answer: the line is printed and logged, and
  -- a subscriber's failure must not turn that into an error reply.
  local raised, why = pcall(remote_iface.raise_say, {
    artifact = artifact, shape = rendered.shape, lines = rendered.lines,
  })
  if not raised then
    log("[ai-agent-bridge] on_say subscriber failed: " .. tostring(why):match("^[^\n]*"))
  end
  return { ok = true, r = true }
end

return M
