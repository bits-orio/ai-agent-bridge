-- AI Agent Bridge - tests/lua/aab_personality_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The bot's voice travels per question (the contract with the service):
-- the runtime-global setting aab-personality as it stood when the question
-- was asked, trimmed, at most 500 bytes, carried as `personality` on the
-- poll row and left out when blank. The service decides what it means. An
-- admin changes it in game with /aab-personality; nobody else may.

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

-- ── the setting, as the data stage declares it ────────────────────────
local declared = {}
_G.data = { extend = function(_, list) for _, p in ipairs(list) do declared[p.name] = p end end }
check("settings.lua loads", pcall(dofile, MOD .. "settings.lua"))
_G.data = nil
local setting = declared["aab-personality"] or {}
check("aab-personality is a runtime-global string setting, blank by default",
      setting.type == "string-setting" and setting.setting_type == "runtime-global" and setting.default_value == "",
      F.encode(setting))
check("a blank value is allowed and spaces are trimmed", setting.allow_blank == true and setting.auto_trim == true, F.encode(setting))

local function read_file(path)
  local f = assert(io.open(path, "r"))
  local text = f:read("a")
  f:close()
  return text
end
local locale = read_file(MOD .. "locale/en/locale.cfg")
check("the setting has a name in the locale", locale:find("\naab%-personality=Bot personality\n") ~= nil)
local description = locale:match("\naab%-personality=(How[^\n]*)") or ""
check("its description says blank, off, an example and the command",
      description:find("blank to use the operator's config", 1, true) ~= nil and description:find("off turns the voice off", 1, true) ~= nil
      and description:find("a cheerful pirate", 1, true) ~= nil and description:find("/aab-personality", 1, true) ~= nil, description)
check("the description has no em-dash", description:find("\u{2014}", 1, true) == nil, description)

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

local function ask(text)
  return remote.call("ai-agent-bridge-v1", "ask", { text = text or "q", force = "player", player_index = 1 })
end
--- The poll row for one question, and the raw reply it came in.
local function polled(qid)
  local reply, raw = rpc({ op = "poll", after = qid - 1, limit = 1 })
  return reply.ok and reply.r[1] or nil, raw
end

-- ── the poll row carries it ───────────────────────────────────────────
-- Mutation-tested: without the field on the row, or without the trim, or
-- without the clip, the matching check goes red.
local blank_q = ask("blank")
local blank_row, blank_raw = polled(blank_q)
check("a blank personality is left off the poll row", blank_row ~= nil and blank_row.personality == nil
      and blank_raw:find("personality", 1, true) == nil, blank_raw)

S.settings["aab-personality"].value = "  a cheerful pirate  "
local pirate_q = ask("pirate")
check("the poll row carries the setting, trimmed", (polled(pirate_q) or {}).personality == "a cheerful pirate",
      F.encode(polled(pirate_q)))
S.settings["aab-personality"].value = "off"
local off_q = ask("off")
check("off travels as off: the service owns what it means", (polled(off_q) or {}).personality == "off", F.encode(polled(off_q)))
check("a question keeps the voice it was asked in when the setting changes after",
      (polled(pirate_q) or {}).personality == "a cheerful pirate", F.encode(polled(pirate_q)))

-- The settings dialog takes any length; a question carries 500 bytes of it,
-- cut on a character boundary. 200 euro signs are 600 bytes; 166 fit.
S.settings["aab-personality"].value = string.rep("\u{20AC}", 200)
local long_q = ask("long")
local long_voice = (polled(long_q) or {}).personality or ""
check("a personality past 500 bytes is cut to fit, on a character boundary",
      #long_voice == 498 and long_voice == string.rep("\u{20AC}", 166), #long_voice)
-- A cut that lands just after a space leaves no trailing space behind.
S.settings["aab-personality"].value = string.rep("x", 499) .. " y"
local edge_q = ask("edge")
check("the cut is trimmed again: no trailing space", (polled(edge_q) or {}).personality == string.rep("x", 499),
      #((polled(edge_q) or {}).personality or ""))
S.settings["aab-personality"].value = ""

-- Sixteen questions, the service's page, at the longest text and the
-- longest personality still fit one poll reply under the 65536-byte cap.
S.settings["aab-personality"].value = string.rep("v", 500)
local first_full = ask(string.rep("t", 400))
for _ = 2, 16 do ask(string.rep("t", 400)) end
local page, page_raw = rpc({ op = "poll", after = first_full - 1, limit = 16 })
check("sixteen questions at both caps fit one poll reply (" .. #page_raw .. " bytes)",
      page.ok and #page.r == 16 and #page_raw < 65536, #page_raw)
S.settings["aab-personality"].value = ""
-- Answer the backlog so later checks read a clean ring.
for id = first_full, first_full + 15 do rpc({ op = "answer", qid = id, artifact = { shape = "notice", text = "ok" } }) end

-- ── /aab-personality ──────────────────────────────────────────────────
local command = S.commands["aab-personality"]
check("/aab-personality is registered", type(command) == "function")
local function run(parameter, player_index)
  S.printed = {}
  S.rcon_replies = {}
  command({ name = "aab-personality", parameter = parameter, player_index = player_index, tick = S.tick })
  return S.printed[#S.printed]
end
local function current() return S.settings["aab-personality"].value end

local shown = run(nil, 1)
check("with no argument it shows the value, and says blank means the operator's config",
      shown ~= nil and shown.who == "Bob" and shown.text:find("operator's config", 1, true) ~= nil, shown and shown.text)

-- Mutation-tested: without the admin check, Ann's set lands.
S.bob.admin = false
local writes_before = #S.setting_writes
local refused = run("a grumpy dwarf", 1)
check("a player who is not an admin is refused, politely, and nothing changes",
      refused ~= nil and refused.who == "Bob" and refused.text:find("Only an admin", 1, true) ~= nil
      and current() == "" and #S.setting_writes == writes_before, refused and refused.text)
local viewed = run("   ", 1)
check("anyone may still look", viewed ~= nil and viewed.text:find("operator's config", 1, true) ~= nil, viewed and viewed.text)
S.bob.admin = true

local set = run("a cheerful pirate", 1)
check("an admin sets it, through settings.global as the engine wants it",
      current() == "a cheerful pirate" and S.setting_writes[#S.setting_writes].name == "aab-personality"
      and S.setting_writes[#S.setting_writes].value == "a cheerful pirate", F.encode(S.setting_writes[#S.setting_writes]))
check("and is told what it is now, and from when", set ~= nil and set.who == "Bob"
      and set.text == "[AI Agent Bridge] Personality set, from the next question on: a cheerful pirate", set and set.text)
check("the change is logged with who made it", (S.log or ""):find("changed by Bob", 1, true) ~= nil, S.log)
local pirate_after = ask("after the change")
check("the next question carries the new voice", (polled(pirate_after) or {}).personality == "a cheerful pirate")
check("no argument now shows the value", (run("", 1) or {}).text == "[AI Agent Bridge] Personality: a cheerful pirate",
      (run("", 1) or {}).text)

-- The settings dialog trims on its own (auto_trim); a value padded some
-- other way still shows trimmed.
S.settings["aab-personality"].value = "  a padded pirate  "
check("the value is shown trimmed", (run(nil, 1) or {}).text == "[AI Agent Bridge] Personality: a padded pirate",
      (run(nil, 1) or {}).text)

local off = run("Off", 1)
check("off is stored as off, whatever its case", current() == "off", current())
check("and confirmed as no voice", off ~= nil and off.text:find("Personality off: the bot answers with no voice", 1, true) ~= nil,
      off and off.text)
check("and shown as no voice", ((run(nil, 1) or {}).text or ""):find("no voice", 1, true) ~= nil)
local reset = run("RESET", 1)
check("reset clears it back to the operator's config", current() == "" and reset ~= nil
      and reset.text:find("reset", 1, true) ~= nil, reset and reset.text)

-- Mutation-tested: without the length check, the 501-byte value lands.
local exact = string.rep("x", 500)
run(exact, 1)
check("500 bytes is accepted", current() == exact, #current())
local too_long = run(exact .. "y", 1)
check("501 bytes is refused, with the limit, and nothing changes", current() == exact and too_long ~= nil
      and too_long.text:find("501 bytes", 1, true) ~= nil and too_long.text:find("500", 1, true) ~= nil, too_long and too_long.text)

-- The server console and RCON arrive with no player_index and may change it.
run("a stern quartermaster", nil)
check("the server console or RCON may change it, and is answered over RCON",
      current() == "a stern quartermaster" and (S.rcon_replies[#S.rcon_replies] or ""):find("stern quartermaster", 1, true) ~= nil,
      S.rcon_replies[#S.rcon_replies])
check("and in the log", (S.log or ""):find("stern quartermaster", 1, true) ~= nil, S.log)
local before_gone = current()
run("a ghost", 99)
check("a player_index with no player behind it does nothing", current() == before_gone and #S.printed == 0)

-- Another mod that took the name first costs this command, never the load.
S.logs = {}
local registered_twice = pcall(require("scripts.personality_command").register)
local said_taken = false
for _, line in ipairs(S.logs) do
  if line:find("is taken", 1, true) then said_taken = true end
end
check("a name already taken is logged, not raised", registered_twice and said_taken, table.concat(S.logs, " | "))

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
