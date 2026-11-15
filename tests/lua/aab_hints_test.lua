-- AI Agent Bridge - tests/lua/aab_hints_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Teaching watchers how to ask (scripts/ask_hints.lua, setting aab-ask-hints):
--   H0 the default is echo only, no hints
--   H1 the /ask echo names the command
--   H2 a watcher is hinted once, at the echo or the answer, never the asker
--   H3 audience rules: asker mode and private answers
--   H4 the echo and off values of the setting
--   H5 the join hint, once
--   H6 removal clears the entry
--   H7 the fallback command name and the chat prefix show in the hint
-- Each section builds its own game; three players are connected: Bob (1),
-- Ann (2) and Cy (3).

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

local HINT = "[AI Agent Bridge] You can ask too: /ask <your question>"
local NOTE = { shape = "notice", text = "hello" }

local bob, ann, cy

--- A new game, three players connected, the companion loaded.
local function fresh(before, settings)
  S = F.install()
  S.force.index, S.team3.index = 1, 2
  for name in pairs(package.loaded) do
    if name:sub(1, 8) == "scripts." then package.loaded[name] = nil end
  end
  -- Hints are on for every section but H0, which tests the shipped default.
  S.settings["aab-ask-hints"] = { value = "all" }
  for k, v in pairs(settings or {}) do S.settings[k] = { value = v } end
  if settings and settings.__default then S.settings["aab-ask-hints"] = { value = "echo" } end
  if before then before(S) end
  bob, ann = game.get_player(1), game.get_player(2)
  ann.connected = true
  ann.force = bob.force
  cy = {
    index = 3, name = "Cy", valid = true, connected = true, admin = false,
    surface = bob.surface, force = bob.force,
    print = function(text) S.printed[#S.printed + 1] = { who = "Cy", text = text } end,
  }
  local lookup = game.get_player
  game.get_player = function(i)
    if i == 3 or i == "Cy" then return cy end
    return lookup(i)
  end
  game.connected_players = { bob, ann, cy }
  game.players = { bob, ann, cy }
  bob.force.connected_players = { bob, ann }
  local loaded, err = pcall(dofile, MOD .. "control.lua")
  check("companion control.lua loads", loaded, err)
  if not loaded then os.exit(1) end
  S.on_init()
end

local function rpc(req)
  S.rcon_replies = {}
  req.v = 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies])
end

local function ask(index, text, cmd)
  S.commands[cmd or "ask"]({ player_index = index, parameter = text })
end
local function direct_ask(spec) return require("scripts.questions").ask(spec) end

--- How many "You can ask too" lines this player has been printed.
local function hints_to(who)
  local n = 0
  for _, p in ipairs(S.printed) do
    if p.who == who and p.text:find("You can ask too", 1, true) then n = n + 1 end
  end
  return n
end

local function last_hint(who)
  local out
  for _, p in ipairs(S.printed) do
    if p.who == who and p.text:find("You can ask too", 1, true) then out = p.text end
  end
  return out
end

local function any_hint()
  return hints_to("Bob") + hints_to("Ann") + hints_to("Cy") + hints_to("*")
end

local function taught(index)
  local h = storage.aab.hints
  return h ~= nil and h.taught[index] == true
end

-- ── H0: the shipped default is echo only ─────────────────────────────
fresh(nil, { __default = true })
do
  ask(1, "q")
  local echo
  for _, p in ipairs(S.printed) do if p.who == "*" then echo = p.text end end
  check("H0 the default echoes the command", echo ~= nil and echo:find("Bob asked with /ask: q", 1, true) ~= nil, echo)
  rpc({ op = "answer", qid = 1, artifact = NOTE })
  S.handlers["event:on_player_joined_game"]({ player_index = 3 })
  check("H0 the default prints no hint to anyone", any_hint() == 0, any_hint())
  check("H0 watching and joining teach nobody", not taught(2) and not taught(3))
end

-- ── H1: the echo names the command ───────────────────────────────────
fresh()
do
  ask(1, "what forces are there")
  local echo
  for _, p in ipairs(S.printed) do if p.who == "*" then echo = p.text end end
  check("H1 the default echo names /ask", echo ~= nil and echo:find("Bob asked with /ask: what forces are there", 1, true) ~= nil, echo)
end

-- ── H2: a watcher is hinted once; the asker and the taught never ─────
fresh()
do
  ask(1, "first")
  check("H2 a watcher is hinted at the echo, with the exact line", last_hint("Ann") == HINT, last_hint("Ann"))
  check("H2 the other watcher too", hints_to("Cy") == 1, hints_to("Cy"))
  check("H2 the asker is never hinted", hints_to("Bob") == 0, hints_to("Bob"))
  check("H2 the hint is private, not a server print", hints_to("*") == 0)
  check("H2 hinted watchers are marked taught", taught(2) and taught(3))
  check("H2 the asker is marked taught", taught(1))
  ask(1, "second")
  rpc({ op = "answer", qid = 1, artifact = NOTE })
  rpc({ op = "answer", qid = 2, artifact = NOTE })
  check("H2 still exactly one hint across two questions and two answers",
        hints_to("Ann") == 1 and hints_to("Cy") == 1 and hints_to("Bob") == 0,
        hints_to("Ann") .. "/" .. hints_to("Cy") .. "/" .. hints_to("Bob"))
end

fresh()
do
  ask(2, "Ann asks first")
  local before = hints_to("Ann")
  ask(1, "then Bob")
  check("H2 a player who has asked is not hinted", before == 0 and hints_to("Ann") == 0, hints_to("Ann"))
  check("H2 the bystander still is", hints_to("Cy") == 1, hints_to("Cy"))
end

-- A remote ask has no echo, so the answer is where watchers learn.
fresh()
do
  local id = direct_ask({ text = "from a mod", player_index = 1, force = "player" })
  check("H2 no hint before the answer when there was no echo", any_hint() == 0, any_hint())
  rpc({ op = "answer", qid = id, artifact = NOTE })
  check("H2 an answer hints the watchers who were not taught",
        hints_to("Ann") == 1 and hints_to("Cy") == 1 and hints_to("Bob") == 0,
        hints_to("Ann") .. "/" .. hints_to("Cy") .. "/" .. hints_to("Bob"))
  local id2 = direct_ask({ text = "again", player_index = 1, force = "player" })
  rpc({ op = "answer", qid = id2, artifact = NOTE })
  check("H2 a second answer adds none", hints_to("Ann") == 1 and hints_to("Cy") == 1)
  -- A refusal addressed to the asker alone teaches nobody.
  fresh()
  local id3 = direct_ask({ text = "refused", player_index = 1, force = "player" })
  rpc({ op = "answer", qid = id3, artifact = { shape = "notice", text = "no", to_asker = true } })
  check("H2 an answer meant for the asker alone hints nobody", any_hint() == 0, any_hint())
end

-- ── H3: audience rules ───────────────────────────────────────────────
fresh(nil, { ["aab-answer-audience"] = "asker" })
do
  ask(1, "private chat")
  rpc({ op = "answer", qid = 1, artifact = NOTE })
  check("H3 asker mode: no watcher is hinted", any_hint() == 0, any_hint())
  check("H3 asker mode: nobody is marked taught by watching", not taught(2) and not taught(3))
end

fresh()
do
  local id = direct_ask({ text = "crew only", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { players = { 1, 2 } } } })
  rpc({ op = "answer", qid = id, artifact = NOTE })
  check("H3 private players answer: a listed member is hinted", hints_to("Ann") == 1, hints_to("Ann"))
  check("H3 private players answer: a non-member gets nothing", hints_to("Cy") == 0 and not taught(3), hints_to("Cy"))
end

fresh()
do
  local id = direct_ask({ text = "force only", player_index = 1, force = "player",
    scope = { key = "crew", private = true, audience = { force = "player" } } })
  rpc({ op = "answer", qid = id, artifact = NOTE })
  check("H3 private force answer: a force member is hinted", hints_to("Ann") == 1, hints_to("Ann"))
  check("H3 private force answer: outsiders get nothing", hints_to("Cy") == 0, hints_to("Cy"))
end

-- ── H4: the echo and off values ──────────────────────────────────────
fresh(nil, { ["aab-ask-hints"] = "echo" })
do
  ask(1, "q")
  rpc({ op = "answer", qid = 1, artifact = NOTE })
  local echo
  for _, p in ipairs(S.printed) do if p.who == "*" and p.text:find("asked", 1, true) then echo = p.text end end
  check("H4 echo setting: the echo names the command", echo ~= nil and echo:find("Bob asked with /ask: q", 1, true) ~= nil, echo)
  check("H4 echo setting: no hints anywhere", any_hint() == 0, any_hint())
  check("H4 echo setting: watching teaches nobody", not taught(2) and not taught(3))
end

fresh(nil, { ["aab-ask-hints"] = "off" })
do
  ask(1, "q")
  rpc({ op = "answer", qid = 1, artifact = NOTE })
  local echo
  for _, p in ipairs(S.printed) do if p.who == "*" and p.text:find("asked", 1, true) then echo = p.text end end
  check("H4 off: the echo is the old text", echo ~= nil and echo:find("[AI Agent Bridge] [color", 1, true) == nil
        or echo == "[AI Agent Bridge] Bob asked: q", echo)
  check("H4 off: the echo has no command in it", echo ~= nil and echo:find("asked with", 1, true) == nil
        and echo:find("Bob asked: q", 1, true) ~= nil, echo)
  check("H4 off: no hints", any_hint() == 0, any_hint())
  S.on_player_joined = S.handlers["event:on_player_joined_game"]
  S.on_player_joined({ player_index = 3 })
  check("H4 off: no join hint either", any_hint() == 0, any_hint())
end

-- ── H5: the join hint, once ──────────────────────────────────────────
fresh()
do
  local joined = S.handlers["event:on_player_joined_game"]
  joined({ player_index = 3 })
  check("H5 an untaught joiner gets the hint", last_hint("Cy") == HINT, last_hint("Cy"))
  check("H5 and is marked taught", taught(3))
  joined({ player_index = 3 })
  check("H5 a rejoin gets none", hints_to("Cy") == 1, hints_to("Cy"))
  check("H5 the join event is still logged", (S.files["ai-agent-bridge/events.jsonl"] or ""):find("player_joined", 1, true) ~= nil)
end

-- ── H6: removal clears the entry ─────────────────────────────────────
fresh()
do
  S.handlers["event:on_player_joined_game"]({ player_index = 3 })
  check("H6 taught before removal", taught(3))
  S.handlers["event:on_player_removed"]({ player_index = 3 })
  check("H6 removal drops the entry", not taught(3))
  check("H6 a rate-table entry is still forgotten too", storage.aab.rate == nil or storage.aab.rate.last[3] == nil)
end

-- ── H7: the fallback name and the chat prefix ────────────────────────
fresh(function(g) g.settings["aab-claim-ask-command"].value = false end)
do
  ask(1, "q", "aab-ask")
  local echo
  for _, p in ipairs(S.printed) do if p.who == "*" then echo = p.text end end
  check("H7 the echo names /aab-ask", echo ~= nil and echo:find("Bob asked with /aab-ask: q", 1, true) ~= nil, echo)
  check("H7 the hint names /aab-ask", last_hint("Ann") == "[AI Agent Bridge] You can ask too: /aab-ask <your question>", last_hint("Ann"))
end

fresh(nil, { ["aab-chat-prefix"] = "?\n\27[0m" })
do
  S.handlers["event:on_player_joined_game"]({ player_index = 3 })
  local line = last_hint("Cy")
  check("H7 the hint mentions the chat prefix, cleaned",
        line == "[AI Agent Bridge] You can ask too: /ask <your question>, or start a chat line with ? [0m"
        or (line ~= nil and line:find("start a chat line with ?", 1, true) ~= nil and not line:find("%c")), line and F.encode(line))
end

print(string.format("%d passed, %d failed", passes, fails))
if fails > 0 then os.exit(1) end
