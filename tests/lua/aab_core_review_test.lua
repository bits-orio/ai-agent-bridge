-- AI Agent Bridge - tests/lua/aab_core_review_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The companion-core review fixes, one section each (C1 to C12, S4):
--   C1  a refused player index never crashes delivery, and a delivery
--       failure is never reported as bad_artifact
--   C2  a remote ask is cleaned like a provider's answer
--   C3  /aab-ask always, /ask unless the startup setting says no
--   C4  the /ask echo prints the stored, stripped text
--   C5  a failing on_answer subscriber does not undo an answer
--   C6  a private force audience also keeps the force's index
--   C7  the ring drops an answered row before an unanswered one
--   C8  the session name and the tag are stripped
--   C9  the diagnostic ops are off unless a startup setting turns them on
--   C10 a player's cooldown survives a leave
--   C11 remote asks have a server-wide cap
--   C12 the artifact check enforces a total size
--   S4  a global question shown to its asker alone says so
-- Each section builds its own game so a setting or a swapped force in one
-- cannot leak into the next.

local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local S

local passes, fails = 0, 0
local function check(name, ok, detail)
  if ok then passes = passes + 1; print("PASS " .. name)
  else fails = fails + 1; print("FAIL " .. name .. ": " .. tostring(detail)) end
end

local EVENTS = "ai-agent-bridge/events.jsonl"

--- A new game with the companion loaded on it. `before` runs between the
--- fake install and control.lua, to seed a command or a startup setting.
local function fresh(before)
  S = F.install()
  S.force.index, S.team3.index = 1, 2
  for name in pairs(package.loaded) do
    if name:sub(1, 8) == "scripts." then package.loaded[name] = nil end
  end
  if before then before(S) end
  local loaded, err = pcall(dofile, MOD .. "control.lua")
  check("companion control.lua loads", loaded, err)
  if not loaded then os.exit(1) end
  S.on_init()
  return S
end

local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies]), S.rcon_replies[#S.rcon_replies]
end

local function remote_ask(spec) return S.interfaces["ai-agent-bridge-v1"].ask(spec) end
local function direct_ask(spec) return require("scripts.questions").ask(spec) end
local function row(qid) return require("scripts.questions").find(qid) end

local function event_lines(kind)
  local out = {}
  for line in (S.files[EVENTS] or ""):gmatch("[^\n]+") do
    local e = F.decode(line)
    if e.event == kind then out[#out + 1] = e.data end
  end
  return out
end

local function printed_to(who)
  local out = {}
  for _, p in ipairs(S.printed) do if p.who == who then out[#out + 1] = p.text end end
  return out
end

local function logged(fragment)
  for _, l in ipairs(S.logs) do if l:find(fragment, 1, true) then return true end end
  return false
end

local NOTE = { shape = "notice", text = "hello" }

-- ── C1: a player index the engine refuses ─────────────────────────────
fresh()
do
  local id = direct_ask({ text = "q", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { players = { 1 } } } })
  -- Fixed on the row after creation: the sanitiser would refuse these now,
  -- and delivery must hold on its own for a row that already has them.
  row(id).audience = { players = { 70000, 0, 1.5, 1 } }
  local reply = rpc({ op = "answer", qid = id, artifact = NOTE })
  check("C1 a private audience holding an index the engine refuses still answers",
        reply.ok == true, F.encode(reply))
  local seen = printed_to("Bob")
  check("C1 and the line reaches the audience member who is valid",
        #seen == 1 and seen[1]:find("hello", 1, true) ~= nil, F.encode(seen))

  local id2 = direct_ask({ text = "q2", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { players = { 70000 } } } })
  row(id2).audience = { players = { 70000 } }
  S.printed = {}
  local reply2 = rpc({ op = "answer", qid = id2, artifact = NOTE })
  check("C1 an audience of only refused indices falls back to the asker",
        reply2.ok == true and #printed_to("Bob") == 1, F.encode(reply2))

  -- A print that raises is not the artifact's fault.
  local id3 = direct_ask({ text = "q3", player_index = 1, force = "player" })
  local real_print = S.bob.print
  S.bob.print = function() error("print refused") end
  local real_game_print = game.print
  game.print = function() error("game.print refused") end
  local reply3 = rpc({ op = "answer", qid = id3, artifact = NOTE })
  S.bob.print, game.print = real_print, real_game_print
  check("C1 a failing print is not reported as bad_artifact", reply3.ok == true, F.encode(reply3))
  check("C1 and the answer is recorded", row(id3).answered == true)
  check("C1 and the failure is logged", logged("delivery"), F.encode(S.logs))

  -- A render error is still the artifact's fault.
  local id4 = direct_ask({ text = "q4", player_index = 1, force = "player" })
  local shapes = require("scripts.render_shapes")
  local real_notice = shapes.notice
  shapes.notice = function() error("cannot draw") end
  local reply4 = rpc({ op = "answer", qid = id4, artifact = NOTE })
  shapes.notice = real_notice
  check("C1 a render error stays bad_artifact and leaves the question pending",
        reply4.ok == false and reply4.e == "bad_artifact" and row(id4).answered == false, F.encode(reply4))

  local sc = direct_ask({ text = "q5", player_index = 1,
    scope = { key = "k", private = true, audience = { players = { 70000, 0, -3, 1.5, 2, "3", 65536 } } } })
  check("C1 an audience keeps only integers from 1 to 65536",
        F.encode(row(sc).audience.players) == F.encode({ 2, 65536 }), F.encode(row(sc).audience))
end

-- ── C2: a remote ask is cleaned ───────────────────────────────────────
fresh()
do
  local big = string.rep("x", 100000)
  local id = remote_ask({ text = "hi", player_index = 1, force = big, viewing_force = big, watching_force = big,
    scope = { key = big, private = false, tag = big, label = big, audience = "x" } })
  local r = row(id)
  check("C2 a 100 KB force name is dropped", r.force == nil and r.viewing_force == nil and r.watching_force == nil,
        F.encode({ r.force and #r.force, r.viewing_force and #r.viewing_force }))
  check("C2 a 100 KB key is refused whole: private under the global key",
        r.scope == "global" and r.private == true, F.encode({ r.scope and #r.scope, r.private }))
  check("C2 the tag is fitted to 200 bytes", r.tag ~= nil and #r.tag <= 200, r.tag and #r.tag)
  check("C2 a non-table audience is ignored", r.audience == nil, F.encode(r.audience))
  local _, raw = rpc({ op = "poll", after = 0 })
  check("C2 poll returns the hostile row under the size cap", raw ~= nil and #raw < 2000 and raw:find('"ok":true') ~= nil,
        raw and #raw)
  local q = event_lines("question")
  check("C2 and so is the logged question line", #q == 1 and #F.encode(q[1]) < 1500, F.encode(q):sub(1, 100))

  local good = remote_ask({ text = "hi", player_index = 1, force = "team-3", viewing_force = "player",
    watching_force = "nonesuch", scope = { key = string.rep("k", 64), private = true, tag = "[T]" } })
  local g = row(good)
  check("C2 a real force is kept, a made-up one is dropped",
        g.force == "team-3" and g.viewing_force == "player" and g.watching_force == nil,
        F.encode({ g.force, g.viewing_force, g.watching_force }))
  check("C2 a key of exactly 64 bytes is kept", g.scope == string.rep("k", 64) and g.private == true)

  local torn = remote_ask({ text = "hi", player_index = 1,
    scope = { key = "k", private = false, tag = string.rep("a", 195) .. "[color=red]xx[/color]" } })
  check("C2 a tag is not cut inside a rich text tag",
        #row(torn).tag <= 200 and not row(torn).tag:find("%[[^%]]*$"), row(torn).tag)
end

-- ── C3: /aab-ask always, /ask unless the setting says no ──────────────
fresh()
do
  check("C3 both names are registered by default",
        S.commands.ask ~= nil and S.commands["aab-ask"] ~= nil)
  check("C3 and they are the same handler", S.commands.ask == S.commands["aab-ask"])
  check("C3 status reports the preferred name", rpc({ op = "status" }).r.ask_command == "ask")
end
fresh(function(g) g.commands.ask = function() end end)
do
  check("C3 with /ask taken, /aab-ask is still registered", S.commands["aab-ask"] ~= nil)
  check("C3 status reports the fallback", rpc({ op = "status" }).r.ask_command == "aab-ask",
        rpc({ op = "status" }).r.ask_command)
  S.commands["aab-ask"]({ player_index = 1, parameter = "where is iron" })
  check("C3 the fallback asks", require("scripts.questions").pending_count() == 1)
end
fresh(function(g) g.settings["aab-claim-ask-command"].value = false end)
do
  check("C3 with the setting off, /ask is left alone", S.commands.ask == nil)
  check("C3 /aab-ask is registered", S.commands["aab-ask"] ~= nil)
  check("C3 status reports aab-ask", rpc({ op = "status" }).r.ask_command == "aab-ask")
end

-- ── C4: the /ask echo prints the stored, stripped text ────────────────
fresh()
do
  local typed = "line one\nline two\27[0m " .. string.rep("y", 5000)
  S.commands.ask({ player_index = 1, parameter = typed })
  local echo = S.printed[#S.printed]
  local stored = require("scripts.questions").find(1).text
  local flat_stored = (stored:gsub("%c+", " "))
  check("C4 the echo has no control characters", echo ~= nil and not echo.text:find("%c"), echo and F.encode(echo.text))
  check("C4 the echo is the stored text, not the typed text",
        echo ~= nil and #echo.text < 600 and echo.text:find(flat_stored, 1, true) ~= nil,
        echo and #echo.text)
end

-- ── C5: a failing subscriber does not undo an answer ──────────────────
fresh()
do
  local id = direct_ask({ text = "q", player_index = 1, force = "player" })
  local event = S.interfaces["ai-agent-bridge-v1"].get_event_id("on_answer")
  S.handlers[event] = function() error("subscriber broke") end
  local reply = rpc({ op = "answer", qid = id, artifact = NOTE })
  check("C5 the answer still returns ok", reply.ok == true and reply.r == true, F.encode(reply))
  check("C5 the answer stays recorded", row(id).answered == true)
  check("C5 the failure is logged", logged("subscriber broke"), F.encode(S.logs))
end

-- ── C6: a force audience keeps the force's index ──────────────────────
fresh()
do
  local id = direct_ask({ text = "q", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { force = "team-3" } } })
  check("C6 the audience stores force_index", row(id).audience.force == "team-3" and row(id).audience.force_index == 2,
        F.encode(row(id).audience))
  S.printed = {}
  rpc({ op = "answer", qid = id, artifact = NOTE })
  check("C6 an unchanged force still hears the answer", #printed_to("force:team-3") == 1, F.encode(S.printed))

  -- The name is reused by another force: the old audience is gone.
  local id2 = direct_ask({ text = "q2", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { force = "team-3" } } })
  local imposter = { name = "team-3", index = 9, valid = true, players = {}, connected_players = {},
    print = function(t) S.printed[#S.printed + 1] = { who = "imposter", text = t } end }
  local original = game.forces["team-3"]
  game.forces["team-3"] = imposter
  S.printed = {}
  rpc({ op = "answer", qid = id2, artifact = NOTE })
  check("C6 a force that took the name later hears nothing",
        #printed_to("imposter") == 0 and #printed_to("force:team-3") == 0, F.encode(S.printed))
  check("C6 the asker gets the answer instead", #printed_to("Bob") == 1, F.encode(S.printed))

  -- A row from before force_index existed behaves as it always did.
  local id3 = direct_ask({ text = "q3", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { force = "team-3" } } })
  row(id3).audience = { force = "team-3" }
  S.printed = {}
  rpc({ op = "answer", qid = id3, artifact = NOTE })
  check("C6 an old row without force_index prints to the named force", #printed_to("imposter") == 1,
        F.encode(S.printed))
  game.forces["team-3"] = original
end
fresh()
do
  S.team3.players = { { name = "other" } }
  S.settings["aab-chat-prefix"].value = "?"
  S.handlers[defines.events.on_console_chat]({ player_index = 1, message = "?where" })
  local r = require("scripts.questions").find(1)
  check("C6 a chat line's engine scope stores the speaker's force index",
        r and r.audience and r.audience.force == "player" and r.audience.force_index == 1,
        r and F.encode(r.audience))
end

-- ── C7: the ring drops an answered row first ──────────────────────────
fresh()
do
  for i = 1, 64 do direct_ask({ text = "q" .. i, player_index = 1 }) end
  rpc({ op = "answer", qid = 5, artifact = NOTE })
  rpc({ op = "answer", qid = 9, artifact = NOTE })
  direct_ask({ text = "q65", player_index = 1 })
  check("C7 the ring holds 64", #storage.aab.q.ring == 64, #storage.aab.q.ring)
  check("C7 the oldest answered row went, not the oldest row",
        row(1) ~= nil and row(5) == nil and row(9) ~= nil)
  check("C7 an answered row was dropped, so no event", #event_lines("question_dropped") == 0)
  direct_ask({ text = "q66", player_index = 1 })
  check("C7 then the next answered row", row(9) == nil and row(1) ~= nil)
  local gone = rpc({ op = "answer", qid = 5, artifact = NOTE })
  check("C7 answering an evicted question is no_question", gone.ok == false and gone.e == "no_question", F.encode(gone))
  direct_ask({ text = "q67", player_index = 1 })
  check("C7 with all 64 unanswered the oldest goes", row(1) == nil and row(2) ~= nil and #storage.aab.q.ring == 64)
  local dropped = event_lines("question_dropped")
  check("C7 and a question_dropped event names it", #dropped == 1 and dropped[1].qid == 1, F.encode(dropped))
  check("C7 and it is logged", logged("question 1"), F.encode(S.logs))
end

-- ── C8: the session name and the tag ──────────────────────────────────
fresh()
do
  local function line_for(session, tag)
    local id = direct_ask({ text = "q", player_index = 1, scope = tag and { key = "k", private = false, tag = tag } or nil })
    S.printed = {}
    rpc({ op = "answer", qid = id, artifact = { shape = "notice", text = "hello", session = session } })
    return S.printed[1] and S.printed[1].text or ""
  end
  check("C8 a good name is shown", line_for({ fresh = true, name = "iron-1_a" }):find("(new session #iron-1_a)", 1, true) ~= nil)
  local forged = line_for({ fresh = true, name = "x\n[AI Agent Bridge]: fake" })
  check("C8 a name with a newline is not shown and breaks no line",
        not forged:find("fake", 1, true) and forged:find("(new session)", 1, true) ~= nil, F.encode(forged))
  check("C8 a 17-byte name is not shown", not line_for({ fresh = true, name = string.rep("a", 17) }):find("#", 1, true))
  check("C8 a 16-byte name is shown", line_for({ fresh = true, name = string.rep("a", 16) }):find("#aaaa", 1, true) ~= nil)
  check("C8 an upper-case name is not shown", not line_for({ fresh = true, name = "Iron" }):find("#", 1, true))
  local tagged = line_for(nil, "[T]\nforged")
  check("C8 a tag's control characters are stripped", not tagged:find("%c") and tagged:find("forged", 1, true) ~= nil,
        F.encode(tagged))
end

-- ── C9: diagnostic ops are off by default ─────────────────────────────
fresh()
do
  for _, op in ipairs({ "big", "write", "pcall_test", "ping" }) do
    local reply = rpc({ op = op, kb = 1 })
    check("C9 " .. op .. " is bad_op by default", reply.ok == false and reply.e == "bad_op", F.encode(reply))
  end
  check("C9 no counter was written", storage.aab.test_counter == nil)
end
fresh(function(g) g.settings["aab-selftest-ops"].value = true end)
do
  check("C9 ping works with the setting on", rpc({ op = "ping" }).ok == true)
  check("C9 big works with the setting on", rpc({ op = "big", kb = 1 }).r.bytes == 1024)
  S.raised = nil
  local w = rpc({ op = "write" })
  check("C9 write keeps its counter", w.ok and w.r.counter == 1 and storage.aab.test_counter == 1, F.encode(w))
  check("C9 write raises no on_answer", S.raised == nil, F.encode(S.raised))
  local p = rpc({ op = "pcall_test" })
  check("C9 pcall_test catches boom", p.ok and p.r.caught == true and tostring(p.r.message):find("boom") ~= nil, F.encode(p))
end

-- ── C10: a cooldown survives a leave ──────────────────────────────────
fresh()
do
  S.settings["aab-ask-cooldown-seconds"].value = 5
  local ask_rate = require("scripts.ask_rate")
  check("C10 the first ask passes", ask_rate.check(S.bob) == nil)
  S.handlers[defines.events.on_player_left_game]({ player_index = 1 })
  S.tick = S.tick + 60
  local refused = ask_rate.check(S.bob)
  check("C10 leaving and rejoining does not reset the cooldown", refused ~= nil and refused:find("wait") ~= nil, refused)
  check("C10 the entry is kept after a leave", storage.aab.rate.last[1] ~= nil)
  S.handlers[defines.events.on_player_removed]({ player_index = 1 })
  check("C10 on_player_removed forgets the entry", storage.aab.rate.last[1] == nil)

  storage.aab.rate.last[40] = S.tick - 3600
  storage.aab.rate.last[41] = S.tick - 3600
  S.tick = S.tick + 600
  storage.aab.rate.last[42] = S.tick - 10
  ask_rate.check(S.bob)
  check("C10 expired entries are pruned when the table is touched",
        storage.aab.rate.last[40] == nil and storage.aab.rate.last[41] == nil and storage.aab.rate.last[1] ~= nil,
        F.encode(storage.aab.rate.last))
  check("C10 an entry still in its cooldown is kept", storage.aab.rate.last[42] ~= nil)
end

-- ── C11: remote asks have their own cap ───────────────────────────────
fresh()
do
  local made, refused = 0, 0
  for i = 1, 70 do
    if remote_ask({ text = "r" .. i }) then made = made + 1 else refused = refused + 1 end
  end
  check("C11 60 remote asks a minute pass, the rest are refused", made == 60 and refused == 10, made .. "/" .. refused)
  local lines = 0
  for _, l in ipairs(S.logs) do if l:find("remote ask", 1, true) then lines = lines + 1 end end
  check("C11 the refusal is logged once a minute", lines == 1, lines)
  check("C11 the player path has its own bucket", require("scripts.ask_rate").check(S.bob) == nil)
  S.tick = S.tick + 3600
  check("C11 the next minute opens again", remote_ask({ text = "again" }) ~= nil)
end

-- ── S4: a global question shown to its asker alone says so ───────────
fresh()
do
  S.settings["aab-answer-audience"].value = "asker"
  local a = direct_ask({ text = "mine", player_index = 1, force = "player" })
  check("S4 the row says to_asker", row(a).to_asker == true)
  local q = event_lines("question")
  check("S4 the question event says to_asker", q[1] and q[1].to_asker == true, F.encode(q))
  local polled = rpc({ op = "poll", after = 0 }).r
  check("S4 the poll row says to_asker", polled[1].to_asker == true, F.encode(polled))
  S.settings["aab-answer-audience"].value = "server"
  local b = direct_ask({ text = "all", player_index = 1, force = "player" })
  polled = rpc({ op = "poll", after = 0 }).r
  check("S4 a later setting change does not rewrite the first row", polled[1].to_asker == true)
  check("S4 server mode leaves it absent", polled[2].to_asker == nil and row(b).to_asker == nil)
  S.settings["aab-answer-audience"].value = "asker"
  local c = direct_ask({ text = "no asker" })
  local d = direct_ask({ text = "private", player_index = 1, scope = { key = "k", private = true } })
  check("S4 a question with no player is not to_asker", row(c).to_asker == nil)
  check("S4 a private question is not to_asker", row(d).to_asker == nil)
  check("S4 the events carry it only where set",
        event_lines("question")[3].to_asker == nil and event_lines("question")[4].to_asker == nil)
end

-- ── C12: the artifact check enforces a total size ───────────────────────────
fresh()
do
  local function answer(artifact)
    local id = direct_ask({ text = "q", player_index = 1 })
    return rpc({ op = "answer", qid = id, artifact = artifact })
  end
  local long = answer({ shape = "summary", lines = { string.rep("x", 1281) } })
  check("C12 a cell over 1280 bytes is answered and cut by the printer, not refused", long.ok == true, F.encode(long))
  check("C12 and the stored line is within the 1280-byte net",
        #rpc({ op = "answers", after = 0 }).r[1].lines[1] == 1280)
  check("C12 a cell of 1280 bytes passes whole", answer({ shape = "summary", lines = { string.rep("x", 1280) } }).ok == true)
  local cells = {}
  for i = 1, 8 do cells[i] = { string.rep("a", 1000), string.rep("b", 1000), string.rep("c", 1000) } end
  local big = answer({ shape = "table", columns = { "a", "b", "c" }, rows = cells })
  check("C12 an artifact over the total cap is bad_artifact", big.ok == false and big.e == "bad_artifact"
        and big.m:find("bytes") ~= nil, F.encode(big))
end

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
