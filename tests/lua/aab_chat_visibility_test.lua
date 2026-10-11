-- AI Agent Bridge - tests/lua/aab_chat_visibility_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Every chat line in events.jsonl says who could read it
-- (companion-mod/scripts/chat_visibility.lua): a scope provider's answer when
-- one gives it, the engine's own force rule when none does, and public for
-- the server console. A question asked with the chat prefix takes its line's
-- scope, so its text and its answer reach no more players than the line did.
--
-- Mutation-tested, one break at a time, each turning a check below red:
--   * questions.lua resolving a chat-prefix question from the providers
--     alone, as it did before: the no-provider prefix checks
--   * chat_visibility's engine rule without an audience: the answer checks
--   * the /ask command taking the chat rule too: the /ask check
--   * scope.lua cutting a tag by bytes, not characters: the wide-provider
--     tag check
--   * scope.lookup no longer saying a provider failed, chat_visibility
--     ignoring it, a later answer clearing it, or a failure overruling a
--     private answer too: the failing-provider checks
--   * /ask failing closed as well: the /ask check beside them
--   * the engine's rule keyed by the force name with a provider installed
--     (as before), or by the global key with none; scope.lookup saying a
--     provider is installed only past one, or always; chat_visibility
--     ignoring what it says: the failing, silent and no-provider checks
--   * a long key cut again, or kept whole; refused already at 64 bytes, or
--     only past 65; leaving an answer that said public public; losing its
--     audience; refused with no log line: the long-key checks
--   * a "#" stripped from a key: the opaque-key check
--   * sanitize dropping a players or a force audience: the crew and
--     provider-audience checks
--   * sanitize reading private == true again, nil or false read as private,
--     or only 1 and "true" read as private: the private-flag checks
--   * questions.ask reading a handed-in scope's private == true: the
--     handed-in check
--   * a keyless private answer taking a readable key: the keyless checks
--   * the tag cut by bytes alone or never cut, or richtext.fit without
--     tidy, without closers, or with the closers outside the budget: the
--     torn-tag checks
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

local EVENTS = "ai-agent-bridge/events.jsonl"
local chat = S.handlers[defines.events.on_console_chat]

local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies])
end

--- Every line of events.jsonl, decoded, oldest first.
local function lines()
  local out = {}
  for line in (S.files[EVENTS] or ""):gmatch("[^\n]+") do out[#out + 1] = F.decode(line) end
  return out
end

--- Says one line in chat and returns the console_chat row it logged.
local function say(player_index, message)
  local before = #lines()
  chat({ player_index = player_index, message = message })
  local after = lines()
  if #after ~= before + 1 then return nil end
  return after[#after].data
end

local function is_public(row)
  return row ~= nil and row.private == false and row.scope == "global"
end

local function is_private_to(row, key)
  return row ~= nil and row.private == true and row.scope == key
end

-- A second force with one player on it, online or not. LuaForce::players
-- lists every member, LuaForce::connected_players only the online ones.
local tia = { index = 3, name = "Tia", valid = true, connected = true, force = S.team3 }
local function split_forces(connected)
  tia.connected = connected
  S.team3.players = { tia }
  S.team3.connected_players = connected and { tia } or {}
end
local function one_force()
  S.team3.players = {}
  S.team3.connected_players = {}
end

-- ── the first line of a session ───────────────────────────────────────
-- The first write of a session puts the session event naming the map ahead
-- of the line itself (aab_map_id_test.lua covers the id).
chat({ player_index = 1, message = "first words" })
local opening = lines()
check("the first chat line of a session comes after the session event",
      #opening == 2 and opening[1].event == "session" and opening[2].event == "console_chat"
      and opening[2].data.message == "first words", F.encode(opening))

-- ── no provider: the engine's own rule ───────────────────────────────
local row = say(1, "hello all")
check("no provider, one force with players: public", is_public(row), F.encode(row))
check("the row keeps player, force and message",
      row and row.player == "Bob" and row.force == "player" and row.message == "hello all", F.encode(row))

split_forces(true)
row = say(1, "our plan")
check("no provider, two forces with players: private to the speaker's force", is_private_to(row, "player"), F.encode(row))
split_forces(false)
row = say(1, "our plan again")
check("a second force with only offline players still makes the line private", is_private_to(row, "player"), F.encode(row))

row = say(nil, "restart in five")
check("a server console line is public", is_public(row), F.encode(row))
check("and is logged as Server with no force", row and row.player == "Server" and row.force == nil, F.encode(row))
one_force()

-- ── no provider: a chat-prefix question takes its line's scope ────────
-- The line reached the speaker's force alone once a second force has
-- players, so the question made from it, its text and its answer, stay in
-- that force too. Before this, the line was logged private and the question
-- global: its text public in history and its answer printed to everyone.
S.settings["aab-chat-prefix"].value = "?"

--- The poll row for one question id.
local function polled(qid)
  local reply = rpc({ op = "poll", after = qid - 1, limit = 1 })
  return reply.ok and reply.r[1] or nil
end

--- Asks through the chat prefix as Bob. Returns the line's event, the
--- question's event and the question's poll row.
local function ask_in_chat(message)
  chat({ player_index = 1, message = message })
  local all = lines()
  local line, question = all[#all - 1], all[#all]
  local qid = question and question.event == "question" and question.data.qid
  return line, question, qid and polled(qid) or nil
end

--- Answers one question with a notice and returns who it printed to, and
--- how many lines were printed.
local function answer(qid, text)
  local before = #S.printed
  local reply = rpc({ op = "answer", qid = qid, artifact = { shape = "notice", text = text } })
  return reply.ok and S.printed[#S.printed].who or F.encode(reply), #S.printed - before
end

split_forces(true)
local line, question, poll = ask_in_chat("?where is our outpost")
check("no provider, two forces: the prefixed line is private to the force",
      line and line.event == "console_chat" and is_private_to(line.data, "player"), F.encode(line))
check("and so is the question made from it",
      question and question.event == "question" and is_private_to(question.data, "player")
      and question.data.text == "where is our outpost", F.encode(question))
check("and its poll row", poll and poll.scope == "player" and poll.private == true, F.encode(poll))
local who, printed = answer(poll and poll.id or 0, "outpost at 300,40")
check("its answer prints to the speaker's force alone", who == "force:player" and printed == 1, who)
-- A private question prints to its own audience whatever aab-answer-audience
-- says, the same as one a chat privacy mod made private.
S.settings["aab-answer-audience"].value = "asker"
line, question, poll = ask_in_chat("?asked in asker mode")
who = answer(poll and poll.id or 0, "still the force")
check("in asker mode the answer still goes to the force that saw the line", who == "force:player", who)
S.settings["aab-answer-audience"].value = "server"

split_forces(false)
line, question, poll = ask_in_chat("?and the other one")
check("a second force with only offline players keeps the question private too",
      is_private_to(line and line.data, "player") and is_private_to(question and question.data, "player")
      and poll and poll.private == true, F.encode({ line, question, poll }))
who = answer(poll and poll.id or 0, "nothing there")
check("and its answer stays in the force", who == "force:player", who)

one_force()
line, question, poll = ask_in_chat("?how much iron")
check("one force with players: the line and the question are both public",
      is_public(line and line.data) and is_public(question and question.data)
      and poll and poll.scope == "global" and poll.private == nil, F.encode({ line, question, poll }))
who = answer(poll and poll.id or 0, "plenty")
check("and the answer prints to everyone", who == "*", who)

-- An /ask command is not a chat line. With no provider it stays global even
-- with two forces populated, as before: its echo shows everyone the question.
split_forces(true)
local before_ask = #S.printed
S.commands["ask"]({ parameter = "where is our outpost", player_index = 1, tick = S.tick })
local ask_qid = tonumber(rpc({ op = "status" }).r.last_id)
local ask_row = polled(ask_qid)
check("an /ask question with no provider stays global with two forces populated",
      ask_row and ask_row.scope == "global" and ask_row.private == nil, F.encode(ask_row))
check("and its echo prints to everyone",
      #S.printed == before_ask + 1 and S.printed[#S.printed].who == "*", F.encode(S.printed[#S.printed]))
answer(ask_qid, "done")
one_force()
S.settings["aab-chat-prefix"].value = ""

-- ── a scope provider answers ─────────────────────────────────────────
-- A team-chat provider: private to the speaker's channel unless the line
-- is a "!" shout or the channel is global.
local channel_private = true
local asked = {}
remote.add_interface("test-scope", {
  chat_scope_v1 = function(player_index, text)
    asked[#asked + 1] = { player_index = player_index, text = text }
    if channel_private and text:sub(1, 1) ~= "!" then
      return { key = "team-x", private = true, audience = { force = "player" }, tag = "[TEAM]" }
    end
    return { key = "global", private = false, tag = "[GLOBAL]" }
  end,
})

row = say(1, "team only")
check("a provider's private answer: private to its key", is_private_to(row, "team-x"), F.encode(row))
check("the provider is asked with the player index and the line as typed",
      asked[#asked].player_index == 1 and asked[#asked].text == "team only", F.encode(asked[#asked]))
row = say(1, "!everyone hear this")
check("a provider's shout rule: public", is_public(row), F.encode(row))
channel_private = false
split_forces(true)
row = say(1, "said in global")
check("a provider's global answer stands even with two forces populated", is_public(row), F.encode(row))
one_force()

-- A chat-prefix question logs its line and the question with the same scope,
-- which is how the service drops the echo for that channel and nobody else.
channel_private = true
S.settings["aab-chat-prefix"].value = "?"
chat({ player_index = 1, message = "?how much iron" })
local all = lines()
local echo, question = all[#all - 1], all[#all]
check("a chat-prefix question logs the line, then the question",
      echo.event == "console_chat" and question.event == "question", F.encode({ echo, question }))
check("both private to the same channel",
      is_private_to(echo.data, "team-x") and question.data.private == true and question.data.scope == "team-x",
      F.encode({ echo.data, question.data }))
S.settings["aab-chat-prefix"].value = ""

-- Two providers: the private answer wins over a global one, as for questions.
remote.add_interface("test-scope-global", {
  chat_scope_v1 = function() return { key = "global", private = false } end,
})
row = say(1, "two providers")
check("the private answer wins over another provider's global one", is_private_to(row, "team-x"), F.encode(row))
S.interfaces["test-scope-global"] = nil

-- A provider that says private but names no key: logged private under the
-- global key, which the service shows to nobody rather than to everyone.
S.interfaces["test-scope"] = nil
remote.add_interface("test-scope-keyless", {
  chat_scope_v1 = function() return { private = true, audience = { force = "player" } } end,
})
row = say(1, "keyless")
check("a private answer with no key stays private", row and row.private == true and row.scope == "global", F.encode(row))
S.interfaces["test-scope-keyless"] = nil

-- ── providers that do not answer fall back to the engine's rule ──────
-- The force name is the engine rule's key only with no provider installed.
-- With one installed, the force may have held earlier groups whose private
-- rows history keeps under that same name, so a line the engine's rule
-- decides, and a question asked with it, stay private to the speaker's force
-- under the global key instead: the service never reads private rows under
-- it, so nobody reads the line back and the question reads public rows
-- alone. The answer still reaches the speaker's force.
local function is_unreadable(data)
  return is_private_to(data, "global")
end

remote.add_interface("test-scope-broken", { chat_scope_v1 = function() error("scope boom") end })
row = say(1, "broken provider, one force")
check("a failing provider with one force populated: public", is_public(row), F.encode(row))
split_forces(true)
row = say(1, "broken provider, two forces")
check("a failing provider with two forces populated: private, under the global key nobody reads with",
      is_unreadable(row), F.encode(row))
S.settings["aab-chat-prefix"].value = "?"
line, question, poll = ask_in_chat("?broken provider question")
check("a chat-prefix question past a failing provider is private under the global key, line and question alike",
      is_unreadable(line and line.data) and is_unreadable(question and question.data)
      and poll and poll.private == true and poll.scope == "global", F.encode({ line, question, poll }))
who = answer(poll and poll.id or 0, "ok")
check("and its answer still prints to the speaker's force alone", who == "force:player", who)
S.interfaces["test-scope-broken"] = nil
remote.add_interface("test-scope-silent", { chat_scope_v1 = function() return nil end })
row = say(1, "silent provider")
check("a provider answering nil leaves the force rule in place, under the global key nobody reads with",
      is_unreadable(row), F.encode(row))
line, question, poll = ask_in_chat("?silent provider question")
check("and a chat-prefix question past it is private under the global key",
      is_unreadable(question and question.data) and poll and poll.private == true and poll.scope == "global",
      F.encode({ question, poll }))
who = answer(poll and poll.id or 0, "ok")
check("and its answer prints to the speaker's force alone", who == "force:player", who)
S.interfaces["test-scope-silent"] = nil
row = say(1, "no provider again")
check("with every provider gone, the force name is the key again", is_private_to(row, "player"), F.encode(row))
line, question, poll = ask_in_chat("?no provider again")
check("and so it is for a chat-prefix question",
      is_private_to(question and question.data, "player") and poll and poll.scope == "player", F.encode({ question, poll }))
answer(poll and poll.id or 0, "ok")
S.settings["aab-chat-prefix"].value = ""
one_force()

-- ── a failing provider beside one that answers global ────────────────
-- The provider that failed may be the one that knew the line was private,
-- so another provider's global answer does not open the line up on its
-- own: the engine's rule decides, under the global key nobody reads with,
-- as above. Providers are asked in name order, so the failure goes first
-- and then last. A private answer still stands.
local function failing_provider(name)
  remote.add_interface(name, { chat_scope_v1 = function() error("scope boom") end })
end
local function open_provider(name)
  remote.add_interface(name, {
    chat_scope_v1 = function() return { key = "global", private = false, tag = "[OPEN]" } end,
  })
end

for _, order in ipairs({
  { label = "failing first", failing = "test-scope-a-failing", open = "test-scope-b-open" },
  { label = "failing last", failing = "test-scope-c-failing", open = "test-scope-b-open" },
}) do
  failing_provider(order.failing)
  open_provider(order.open)
  split_forces(true)
  row = say(1, "beside a global answer")
  check(order.label .. ": a global answer beside a failing provider, two forces populated: private, unreadable",
        is_unreadable(row), F.encode(row))
  one_force()
  row = say(1, "beside a global answer, one force")
  check(order.label .. ": and public while one force has players", is_public(row), F.encode(row))
  S.interfaces[order.failing], S.interfaces[order.open] = nil, nil
end

failing_provider("test-scope-a-failing")
open_provider("test-scope-b-open")
split_forces(true)
S.settings["aab-chat-prefix"].value = "?"
line, question, poll = ask_in_chat("?where is our base")
check("a chat-prefix question past a failure and a global answer is private and unreadable, line and question alike",
      is_unreadable(line and line.data) and is_unreadable(question and question.data)
      and poll and poll.private == true and poll.scope == "global", F.encode({ line, question, poll }))
who = answer(poll and poll.id or 0, "base at 0,0")
check("its answer prints to the force alone, without the global tag",
      who == "force:player" and S.printed[#S.printed].text:find("[OPEN]", 1, true) == nil, S.printed[#S.printed].text)
S.settings["aab-chat-prefix"].value = ""
-- An /ask question is not a chat line, and a global question reads no
-- private row, so it keeps the global answer as before.
S.commands["ask"]({ parameter = "where is our base", player_index = 1, tick = S.tick })
local open_ask = polled(tonumber(rpc({ op = "status" }).r.last_id))
check("an /ask question past the same two providers keeps the global answer",
      open_ask and open_ask.scope == "global" and open_ask.private == nil, F.encode(open_ask))
who = answer(open_ask and open_ask.id or 0, "done")
check("and its answer prints to everyone", who == "*", who)
remote.add_interface("test-scope-c-team", {
  chat_scope_v1 = function() return { key = "team-x", private = true, audience = { force = "player" } } end,
})
row = say(1, "beside a private answer")
check("a private answer beside a failing provider still stands", is_private_to(row, "team-x"), F.encode(row))
S.interfaces["test-scope-a-failing"], S.interfaces["test-scope-b-open"], S.interfaces["test-scope-c-team"] = nil, nil, nil
one_force()

-- ── a provider's private flag: private unless false or absent ────────
-- The service reads any private value but a literal false as private
-- (history's declared()), so the companion does too: a 1 or a "true" keeps
-- the line private rather than opening it to everyone.
local flag
remote.add_interface("test-scope-flag", {
  chat_scope_v1 = function() return { key = "team-x", private = flag, audience = { force = "player" } } end,
})
for _, case in ipairs({ { 1, "1" }, { "true", '"true"' }, { 0, "0" }, { "false", '"false"' }, { {}, "a table" } }) do
  flag = case[1]
  row = say(1, "private is " .. case[2])
  check("private = " .. case[2] .. " logs the line private to its key", is_private_to(row, "team-x"), F.encode(row))
end
flag = false
row = say(1, "private is false")
check("private = false logs the line public", row and row.private == false and row.scope == "team-x", F.encode(row))
flag = nil
row = say(1, "private is absent")
check("no private field logs the line public", row and row.private == false and row.scope == "team-x", F.encode(row))
flag = 1
S.commands["ask"]({ parameter = "flagged", player_index = 1, tick = S.tick })
local flagged = polled(tonumber(rpc({ op = "status" }).r.last_id))
check("an /ask question under private = 1 polls private under the provider's key",
      flagged and flagged.private == true and flagged.scope == "team-x", F.encode(flagged))
who = answer(flagged and flagged.id or 0, "kept in")
check("and its answer prints to the provider's audience", who == "force:player", who)
S.interfaces["test-scope-flag"] = nil

-- A private answer with no usable key keeps the global key, which the
-- service never reads private rows under: the line is read back by nobody,
-- and the question reads public rows alone.
remote.add_interface("test-scope-flag-keyless", {
  chat_scope_v1 = function() return { key = 42, private = 1 } end,
})
row = say(1, "keyless and flagged")
check("private = 1 with a key that is not a string logs private under the global key",
      row and row.private == true and row.scope == "global", F.encode(row))
S.commands["ask"]({ parameter = "keyless and flagged", player_index = 1, tick = S.tick })
local keyless = polled(tonumber(rpc({ op = "status" }).r.last_id))
check("and an /ask question under it polls private under the global key",
      keyless and keyless.private == true and keyless.scope == "global", F.encode(keyless))
who = answer(keyless and keyless.id or 0, "for you")
check("with no audience its answer reaches the asker alone", who == "Bob", who)
S.interfaces["test-scope-flag-keyless"] = nil

-- An audience of player indices keeps the whole numbers and reaches those
-- players alone, the asker too only when listed. Ann is listed and online;
-- Bob, who asks, is not listed.
local ann = game.get_player(2)
ann.connected = true
remote.add_interface("test-scope-crew", {
  chat_scope_v1 = function() return { key = "crew-1", private = true, audience = { players = { 2, 1.5, "x" } } } end,
})
local before_crew = #S.printed
S.commands["ask"]({ parameter = "crew only", player_index = 1, tick = S.tick })
check("a players audience gets the echo of an /ask question, and nobody else does",
      #S.printed == before_crew + 1 and S.printed[#S.printed].who == "Ann", F.encode(S.printed[#S.printed]))
local crew = polled(tonumber(rpc({ op = "status" }).r.last_id))
who = answer(crew and crew.id or 0, "for the crew")
check("and the answer", crew and crew.scope == "crew-1" and who == "Ann", who)
ann.connected = false
S.interfaces["test-scope-crew"] = nil

-- A mod handing in a scope of its own is read the same way.
local handed = remote.call("ai-agent-bridge-v1", "ask", { text = "handed in", player_index = 1,
  scope = { key = "team-3", private = 1, audience = { force = "team-3" } } })
local handed_row = polled(handed)
check("a scope handed in with private = 1 polls private",
      handed_row and handed_row.private == true and handed_row.scope == "team-3", F.encode(handed_row))
who = answer(handed, "for team 3")
check("and its answer prints to that scope's audience", who == "force:team-3", who)

-- ── a provider's long key is refused, never cut ───────────────────────
-- A key is the read key of private history, compared whole. Cut to 64
-- bytes, two keys sharing their first 64 bytes would read each other's
-- private lines, and a generation at the end of a long name would be the
-- part cut away. A key past 64 bytes is refused: the answer is private
-- under the global key nobody reads with, whatever it said about private,
-- and still prints to the audience it named. A tag past 200 bytes is still
-- cut, on a character boundary, since it is only printed.
local E_ACUTE = "\u{E9}"
local wide = { private = true, audience = { force = "player" } }
remote.add_interface("test-scope-wide", {
  chat_scope_v1 = function()
    return { key = wide.key, private = wide.private, audience = wide.audience,
             tag = "t" .. string.rep(E_ACUTE, 150) }
  end,
})

--- Says one line while the provider answers with this key.
local function say_keyed(key, message)
  wide.key = key
  return say(1, message)
end

--- Whether any log line since `from` holds `text`.
local function logged_since(from, text)
  for i = from + 1, #S.logs do
    if S.logs[i]:find(text, 1, true) then return true end
  end
  return false
end

local logs_before = #S.logs
row = say_keyed("k" .. string.rep(E_ACUTE, 40), "wide")
check("an 81-byte key is refused, not cut: the line is private under the global key",
      is_unreadable(row), F.encode(row))
check("and the refusal is logged with the provider's name and the key's size",
      logged_since(logs_before, "provider test-scope-wide gave a key of 81 bytes, past the 64-byte limit"),
      F.encode(S.logs))

-- A channel name of 64 bytes taken twice, each group with its generation:
-- cut, both keys were the bare name, and the later group read the first's.
local name64 = "north-shore-builders-guild-of-the-iron-plateau-and-the-quiet-sea"
local first = say_keyed(name64 .. "@51840", "first group")
local later = say_keyed(name64 .. "@99000", "later group")
check("two keys sharing their first 64 bytes are both refused, never stored as that shared prefix",
      #name64 == 64 and is_unreadable(first) and is_unreadable(later), F.encode({ first, later }))

local exact = "k" .. string.rep(E_ACUTE, 31) .. "x"
row = say_keyed(exact, "exactly 64 bytes")
check("a key of exactly 64 bytes is kept whole", #exact == 64 and is_private_to(row, exact), F.encode(row))
row = say_keyed(exact .. "y", "65 bytes")
check("a key of 65 bytes is refused", is_unreadable(row), F.encode(row))

-- A key is opaque: no character in it means anything to the companion, so
-- one with "#" and "@" in it is stored whole, line and question alike.
S.settings["aab-chat-prefix"].value = "?"
wide.key = "guild#ops@51840"
line, question, poll = ask_in_chat("?opaque key")
check("a key with # and @ in it is kept whole on the line, the question and its poll row",
      is_private_to(line and line.data, "guild#ops@51840") and is_private_to(question and question.data, "guild#ops@51840")
      and poll and poll.scope == "guild#ops@51840", F.encode({ line, question, poll }))
answer(poll and poll.id or 0, "ok")
S.settings["aab-chat-prefix"].value = ""

-- Refused under a private answer with an audience: the question is private
-- under the global key and still reaches that audience, tag and all.
wide.key = "k" .. string.rep(E_ACUTE, 40)
local before_wide = #S.printed
S.commands["ask"]({ parameter = "wide", player_index = 1, tick = S.tick })
local echo_line = S.printed[#S.printed]
check("a 301-byte tag is cut to 199 bytes, the last whole character before 200",
      #S.printed == before_wide + 1 and echo_line.text:find("[AI Agent Bridge] t" .. string.rep(E_ACUTE, 99) .. " Bob asked: wide", 1, true) == 1,
      echo_line.text)
check("an /ask question under a refused key is echoed to the audience the provider named",
      echo_line.who == "force:player", echo_line.who)
local wide_poll = polled(tonumber(rpc({ op = "status" }).r.last_id))
check("and polls private under the global key",
      wide_poll and wide_poll.private == true and wide_poll.scope == "global", F.encode(wide_poll))
who = answer(wide_poll and wide_poll.id or 0, "ok")
check("and its answer prints to that audience", who == "force:player", who)

-- Refused under an answer that said public: the long key was a mistake, and
-- the safe side of a mistake is private. With no audience named, the
-- answer goes to the asker alone.
wide.private, wide.audience = false, nil
row = say_keyed(name64 .. "@51840", "said public")
check("a refused key makes an answer that said public private under the global key",
      is_unreadable(row), F.encode(row))
S.commands["ask"]({ parameter = "said public", player_index = 1, tick = S.tick })
local said_public = polled(tonumber(rpc({ op = "status" }).r.last_id))
check("and an /ask question under it polls private under the global key",
      said_public and said_public.private == true and said_public.scope == "global", F.encode(said_public))
who = answer(said_public and said_public.id or 0, "for you")
check("and with no audience named its answer reaches the asker alone", who == "Bob", who)
S.interfaces["test-scope-wide"] = nil

-- ── a provider's long tag never tears a rich-text tag ────────────────
-- The tag is printed on the answer's first line, and Factorio draws a whole
-- line raw when any tag in it is torn. A cut that lands inside a tag drops
-- it back to its bracket, and a colour span the cut leaves open is closed,
-- all within the 200 bytes.
local tag_text
remote.add_interface("test-scope-tagged", {
  chat_scope_v1 = function() return { key = "global", private = false, tag = tag_text } end,
})
--- Asks with /ask and answers it. Returns the echo and the answer's text.
local function tagged_lines(question_text)
  S.commands["ask"]({ parameter = question_text, player_index = 1, tick = S.tick })
  local echoed = S.printed[#S.printed].text
  answer(tonumber(rpc({ op = "status" }).r.last_id), "ok")
  return echoed, S.printed[#S.printed].text
end
-- 15 + 180 + 8 = 203 bytes: the cut at 200 lands inside the closing [/color].
tag_text = "[color=1,0.5,0]" .. string.rep("x", 180) .. "[/color]"
local closed_tag = "[color=1,0.5,0]" .. string.rep("x", 177) .. "[/color]"
local echoed, answered_text = tagged_lines("torn")
check("a cut inside the tag's own [/color] keeps the span whole instead, in 200 bytes",
      #closed_tag == 200 and echoed == "[AI Agent Bridge] " .. closed_tag .. " Bob asked: torn", echoed)
check("and the answer's first line carries the same tag",
      answered_text == "[AI Agent Bridge] " .. closed_tag .. ": ok", answered_text)
-- 11 + 300 = 311 bytes: the cut lands after [color=red] opened, before it closed.
tag_text = "[color=red]" .. string.rep("y", 300)
local open_tag = "[color=red]" .. string.rep("y", 181) .. "[/color]"
echoed = tagged_lines("open")
check("a cut that leaves a colour open closes it, in 200 bytes",
      #open_tag == 200 and echoed == "[AI Agent Bridge] " .. open_tag .. " Bob asked: open", echoed)
-- 190 + 21 = 211 bytes: the cut tears an img tag, which opens no span, so
-- only dropping the torn tag back to its bracket saves the line.
tag_text = string.rep("z", 190) .. "[img=item.iron-plate]"
echoed = tagged_lines("img")
check("a cut inside an img tag drops the tag",
      echoed == "[AI Agent Bridge] " .. string.rep("z", 190) .. " Bob asked: img", echoed)
S.interfaces["test-scope-tagged"] = nil

-- ── nothing is asked when nothing is logged ──────────────────────────
local calls = 0
remote.add_interface("test-scope-count", {
  chat_scope_v1 = function() calls = calls + 1; return { key = "global", private = false } end,
})
S.settings["aab-events-enabled"].value = false
local size = #(S.files[EVENTS] or "")
chat({ player_index = 1, message = "quiet" })
check("with events off nothing is written", #(S.files[EVENTS] or "") == size)
check("and no scope provider is asked", calls == 0, calls)
S.settings["aab-events-enabled"].value = true
S.interfaces["test-scope-count"] = nil

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
