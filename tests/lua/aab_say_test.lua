-- AI Agent Bridge - tests/lua/aab_say_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The `say` op: one unprompted line the service prints for everyone, a
-- milestone toast that answers no question. Rendered like an answer, printed
-- with the companion's head and no tag, logged as an event line without text,
-- raised as on_say, and never written to storage.

local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local S = F.install()

local passes, fails = 0, 0
local function check(name, ok, detail)
  if ok then passes = passes + 1; print("PASS " .. name)
  else fails = fails + 1; print("FAIL " .. name .. ": " .. tostring(detail)) end
end

local loaded, err = pcall(dofile, MOD .. "control.lua")
check("companion control.lua loads", loaded, err)
if not loaded then os.exit(1) end
S.on_init()

local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies]), S.rcon_replies[#S.rcon_replies]
end
local function say(artifact) return rpc({ op = "say", artifact = artifact }) end
local function events_text() return S.files["ai-agent-bridge/events.jsonl"] or "" end
local function say_lines()
  local out = {}
  for line in events_text():gmatch("[^\n]+") do
    local row = F.decode(line)
    if row.event == "say" then out[#out + 1] = row end
  end
  return out
end

local SUMMARY = { shape = "summary", title = "Milestone", lines = { "The 100th rocket went up." } }

-- ── a summary prints once to everyone, with the head and nothing else ──
S.printed = {}
local reply = say(SUMMARY)
check("SAY1 a summary is accepted: ok and r = true", reply.ok == true and reply.r == true, F.encode(reply))
check("SAY1 it prints exactly once, to the whole game", #S.printed == 1 and S.printed[1].who == "*", F.encode(S.printed))
local text = S.printed[1] and S.printed[1].text or ""
check("SAY1 the line opens with the companion's head and the title",
      text:sub(1, 20) == "[AI Agent Bridge]: M", text)
check("SAY1 the title and the body are on their own lines", text == "[AI Agent Bridge]: Milestone\nThe 100th rocket went up.", text)
check("SAY1 no tag and no session marker", not text:find("new session", 1, true) and not text:find("] [", 1, true), text)

-- A session field is not honoured: a toast belongs to no session.
S.printed = {}
say({ shape = "summary", lines = { "hello" }, session = { fresh = true, name = "iron" } })
check("SAY1 a session marker is never printed", S.printed[1] and not S.printed[1].text:find("session", 1, true),
      F.encode(S.printed))

-- ── a table renders the way an answer's table does ──
local table_art = { shape = "table", columns = { "Item", "Made" }, rows = { { "iron-plate", "100" } } }
S.printed = {}
say(table_art)
local say_table = S.printed[1] and S.printed[1].text or ""
local render = require("scripts.render")
local expected = "[AI Agent Bridge]: " .. table.concat(render.compose(table_art).lines, "\n")
check("SAY2 a table prints the lines the answer renderer composes", say_table == expected,
      say_table .. " | " .. expected)

-- ── labels are swapped in, as in an answer ──
remote.add_interface("test-say-labels", { force_labels_v1 = function()
  return { ["team-3"] = "[color=red]Team Losers[/color]" }
end })
S.tick = S.tick + 1  -- the label scan is cached per tick
S.printed = {}
say({ shape = "summary", lines = { "team-3 launched a rocket" } })
local labelled = S.printed[1] and S.printed[1].text or ""
check("SAY3 a force name prints as its label", labelled:find("[color=red]Team Losers[/color] launched a rocket", 1, true) ~= nil, labelled)
S.interfaces["test-say-labels"] = nil
S.tick = S.tick + 1

-- ── bad_artifact ──
local function refused(name, artifact)
  S.printed = {}
  local r = say(artifact)
  check(name, r.ok == false and r.e == "bad_artifact" and #S.printed == 0, F.encode(r) .. " printed " .. #S.printed)
end
refused("SAY4 no artifact is bad_artifact", nil)
refused("SAY4 an artifact that is not an object is bad_artifact", "hello")
refused("SAY4 a summary with no lines is bad_artifact", { shape = "summary", lines = {} })
refused("SAY4 blank lines print nothing: bad_artifact", { shape = "summary", lines = { "  ", "" } })
refused("SAY4 a to_asker field makes no sense here: bad_artifact", { shape = "summary", lines = { "x" }, to_asker = true })
refused("SAY4 even to_asker = false is refused", { shape = "summary", lines = { "x" }, to_asker = false })
refused("SAY4 an unknown shape is bad_artifact", { shape = "poem", lines = { "x" } })
local big = {}
for i = 1, 200 do big[i] = string.rep("x", 100) end
refused("SAY4 over the 16000 byte ceiling is bad_artifact", { shape = "summary", lines = big })

-- ── the setting turns it off ──
settings.global["aab-allow-say"] = { value = false }
S.printed = {}
local before_events = #say_lines()
local off = say(SUMMARY)
check("SAY5 with aab-allow-say off the op is bad_op", off.ok == false and off.e == "bad_op", F.encode(off))
check("SAY5 and nothing is printed or logged", #S.printed == 0 and #say_lines() == before_events, F.encode(S.printed))
settings.global["aab-allow-say"] = { value = true }
check("SAY5 turned back on it works again", say(SUMMARY).ok == true)

-- ── the event line: shape, no text ──
local rows = say_lines()
local last = rows[#rows] or {}
check("SAY6 an event line `say` is written per accepted say", #rows >= 3 and last.event == "say", #rows)
check("SAY6 it carries the shape", last.data and last.data.shape == "summary", F.encode(last))
check("SAY6 it carries no text", last.data and next(last.data) == "shape" and next(last.data, "shape") == nil
      and not events_text():find("100th rocket", 1, true), F.encode(last))

-- ── on_say ──
local on_answer_id = remote.call("ai-agent-bridge-v1", "get_event_id", "on_answer")
local on_say_id = remote.call("ai-agent-bridge-v1", "get_event_id", "on_say")
check("SAY7 get_event_id on_say returns an id other than on_answer's", on_say_id ~= nil and on_say_id ~= on_answer_id,
      tostring(on_say_id) .. " vs " .. tostring(on_answer_id))
check("SAY7 get_event_id on_answer is unchanged: still an id", type(on_answer_id) == "number")
check("SAY7 an unknown event name is still nil", remote.call("ai-agent-bridge-v1", "get_event_id", "on_nothing") == nil)
S.raised = nil
say(SUMMARY)
local raised
for _, one in ipairs(S.raised or {}) do if one.id == on_say_id then raised = one.data end end
check("SAY7 on_say is raised with the artifact, shape and lines",
      raised and raised.shape == "summary" and raised.artifact ~= nil
      and raised.lines[1] == "Milestone" and raised.lines[2] == "The 100th rocket went up.", F.encode(raised))
check("SAY7 on_say's artifact is the one sent", raised and raised.artifact.title == "Milestone", F.encode(raised))
local answered_raised = false
for _, one in ipairs(S.raised or {}) do if one.id == on_answer_id then answered_raised = true end end
check("SAY7 say raises no on_answer", not answered_raised)

S.handlers[on_say_id] = function() error("subscriber broke") end
S.logs = {}
S.printed = {}
local survives = say(SUMMARY)
check("SAY8 a failing subscriber does not turn the reply into an error", survives.ok == true and survives.r == true, F.encode(survives))
check("SAY8 the line was still printed", #S.printed == 1)
local logged = false
for _, line in ipairs(S.logs) do if line:find("subscriber broke", 1, true) then logged = true end end
check("SAY8 the failure is logged", logged, F.encode(S.logs))
S.handlers[on_say_id] = nil

-- ── no storage, no question ring ──
local function snapshot() return F.encode(storage.aab or {}) end
local questions = require("scripts.questions")
local pending_before, last_before = questions.pending_count(), questions.last_id()
local storage_before = snapshot()
say(SUMMARY)
say(table_art)
check("SAY9 storage.aab is byte for byte unchanged", snapshot() == storage_before, snapshot())
check("SAY9 the question ring is untouched", questions.pending_count() == pending_before and questions.last_id() == last_before)

-- ── gate: a player typing it gets the refusal and nothing runs ──
S.printed = {}
S.rcon_replies = {}
S.commands["aab-rpc"]({ parameter = F.encode({ v = 1, op = "say", artifact = SUMMARY }), player_index = 1, tick = S.tick })
check("SAY10 a player's own console cannot make the bot speak",
      #S.rcon_replies == 0 and #S.printed == 1 and S.printed[1].who == "Bob" and S.printed[1].text:find("service's command", 1, true),
      F.encode(S.printed))

print(passes .. " passed, " .. fails .. " failed")
if fails > 0 then os.exit(1) end
