-- AI Agent Bridge - tests/lua/aab_test.lua
-- Author: bits-orio
-- License: MIT
--
local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
local PROVIDER = REPO_DIR .. "tests/provider-mod/aab-test-provider/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local S = F.install()

local passes, fails = 0, 0
local function check(name, ok, detail)
  if ok then passes = passes + 1; print("PASS " .. name)
  else fails = fails + 1; print("FAIL " .. name .. ": " .. tostring(detail)) end
end

-- ── Load both mods the way the engine would ───────────────────────────
local loaded, err = pcall(dofile, MOD .. "control.lua")
check("companion control.lua loads", loaded, err)
if not loaded then os.exit(1) end
check("test provider control.lua loads", pcall(dofile, PROVIDER .. "control.lua"))
S.on_init()

local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies])
end

-- ── status and the catalog ────────────────────────────────────────────
local status = rpc({ op = "status" })
check("status ok", status.ok and status.r.protocol == 1, F.encode(status))
check("status names the live ask command", status.r.ask_command == "ask", status.r.ask_command)

local tools = rpc({ op = "tools" })
local by_iface = {}
check("the tools op answered at all", tools.ok, F.encode(tools))
for _, p in ipairs(tools.r or {}) do by_iface[p.iface] = p.tools end
check("catalog has the companion's tools", by_iface["ai-agent-bridge-tools"] ~= nil)
check("catalog has the test provider", by_iface["aab-test-provider"] ~= nil)
local want = { "list_forces", "list_players", "current_research", "list_surfaces",
               "item_rate", "top_items", "production_since" }
for _, name in ipairs(want) do
  check("manifest lists " .. name, by_iface["ai-agent-bridge-tools"][name] ~= nil)
end
check("provider manifest lists hello", (by_iface["aab-test-provider"] or {}).hello ~= nil)

-- ── every engine tool answers through the call op ─────────────────────
local function call(iface, fn, args) return rpc({ op = "call", i = iface, f = fn, a = args }) end

local surfaces = call("ai-agent-bridge-tools", "list_surfaces", { force = "player" })
check("list_surfaces ok", surfaces.ok, F.encode(surfaces))
check("list_surfaces returns both surfaces", surfaces.ok and #surfaces.r.surfaces == 2)
check("list_surfaces reports the planet", surfaces.ok and surfaces.r.surfaces[1].planet == "nauvis",
      surfaces.ok and F.encode(surfaces.r.surfaces[1]))
check("list_surfaces counts this force's players", surfaces.ok and surfaces.r.surfaces[1].force_players == 2)

-- The fake game makes the same plate at two qualities, normal at the full rate
-- and uncommon at half of it, so every flow figure below is the SUM: a tool that
-- still passed a bare item name would report the normal-quality part alone
-- (second review-fix contract 6).
S.qualities_asked = {}
local rate = call("ai-agent-bridge-tools", "item_rate",
  { force = "player", surface = "nauvis", item = "iron-plate", window = "one_minute" })
check("item_rate ok", rate.ok and rate.r.net_per_min == 60, F.encode(rate))
check("item_rate sums both qualities", rate.ok and rate.r.produced_per_min == 90
      and rate.r.consumed_per_min == 30, rate.ok and F.encode(rate.r))
check("item_rate asked the engine for every quality",
      S.qualities_asked.normal == 2 and S.qualities_asked.uncommon == 2, F.encode(S.qualities_asked))
check("item_rate says it summed every quality", rate.ok and rate.r.all_qualities == true)

local by_index = call("ai-agent-bridge-tools", "item_rate",
  { force = "player", surface = 1, item = "iron-plate", window = "one_minute" })
check("item_rate takes the surface index list_surfaces published",
      by_index.ok and by_index.r.found == true and by_index.r.surface == "nauvis", F.encode(by_index))
local no_surface = call("ai-agent-bridge-tools", "item_rate",
  { force = "player", surface = "atlantis", item = "iron-plate", window = "one_minute" })
check("item_rate answers found=false for an unknown surface, never an error",
      no_surface.ok and no_surface.r.found == false and no_surface.r.reason:find("list_surfaces"),
      F.encode(no_surface))

local top = call("ai-agent-bridge-tools", "top_items",
  { force = "player", surface = "nauvis", window = "ten_minutes", n = 5 })
check("top_items ok", top.ok and #top.r.items == 2, F.encode(top))
check("top_items sums every quality too", top.ok and top.r.items[1].produced_per_min == 90
      and top.r.all_qualities == true, top.ok and F.encode(top.r.items))
local top_nowhere = call("ai-agent-bridge-tools", "top_items",
  { force = "player", surface = "atlantis", window = "ten_minutes" })
check("top_items answers found=false for an unknown surface",
      top_nowhere.ok and top_nowhere.r.found == false, F.encode(top_nowhere))

local since = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = "nauvis", item = "iron-plate", since_tick = S.tick - 600 })
check("production_since ok", since.ok, F.encode(since))
-- 600 ticks elapsed: the one_minute window (3600 ticks) is the smallest that
-- covers it, 12 ticks a sample, so 50 samples at 2 produced each.
check("production_since picks the smallest covering window", since.ok and since.r.window == "one_minute", since.ok and since.r.window)
check("production_since sums the right sample count", since.ok and since.r.samples == 50, since.ok and since.r.samples)
check("production_since sums produced over both qualities", since.ok and since.r.produced == 150,
      since.ok and since.r.produced)
check("production_since sums consumed over both qualities, rounded",
      since.ok and since.r.consumed == 75 and since.r.net == 75, since.ok and F.encode(since.r))

-- since_tick 0 is the whole game: exact from the engine's lifetime counters,
-- one lookup per quality, no samples.
local whole = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = "nauvis", item = "iron-plate", since_tick = 0 })
check("production_since since 0 is exact and summed over qualities", whole.ok and whole.r.produced == 1500
      and whole.r.consumed == 600 and whole.r.net == 900 and whole.r.method == "lifetime counters, exact"
      and whole.r.window == nil, F.encode(whole))
-- No surface: every surface the force has made the item on, with a breakdown;
-- the platform has never made any and is left out.
local everywhere = call("ai-agent-bridge-tools", "production_since",
  { force = "player", item = "iron-plate", since_tick = 0 })
check("production_since with no surface counts every producing surface", everywhere.ok and everywhere.r.surface == "all"
      and everywhere.r.produced == 1500 and everywhere.r.surfaces_counted == 1
      and everywhere.r.surfaces[1].surface == "nauvis" and everywhere.r.surfaces[1].produced == 1500, F.encode(everywhere))
local everywhere_sampled = call("ai-agent-bridge-tools", "production_since",
  { force = "player", item = "iron-plate", since_tick = S.tick - 600 })
check("production_since with no surface and a recent tick samples the producing surfaces only",
      everywhere_sampled.ok and everywhere_sampled.r.produced == 150 and everywhere_sampled.r.surfaces_counted == 1
      and everywhere_sampled.r.method == "flow samples" and everywhere_sampled.r.samples == 50, F.encode(everywhere_sampled))
check("production_since reports full coverage", since.ok and since.r.covers_full_period == true)
check("production_since covered_ticks >= elapsed", since.ok and since.r.covered_ticks >= since.r.elapsed_ticks)

local far = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = "nauvis", item = "iron-plate", since_tick = 1 })
check("production_since clamps a very old tick", far.ok and far.r.samples <= 300 and far.r.method == "flow samples", F.encode(far))

local future = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = "nauvis", item = "iron-plate", since_tick = S.tick + 10 })
check("production_since refuses a future tick", (not future.ok) and future.e == "provider_error", F.encode(future))

local since_nowhere = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = "atlantis", item = "iron-plate", since_tick = 0 })
check("production_since answers found=false for an unknown surface rather than erroring",
      since_nowhere.ok and since_nowhere.r.found == false and since_nowhere.r.reason:find("list_surfaces"),
      F.encode(since_nowhere))
local since_by_index = call("ai-agent-bridge-tools", "production_since",
  { force = "player", surface = 1, item = "iron-plate", since_tick = S.tick - 600 })
check("production_since takes a surface index as well",
      since_by_index.ok and since_by_index.r.surface == "nauvis", F.encode(since_by_index))

local bad_window = call("ai-agent-bridge-tools", "item_rate",
  { force = "player", surface = "nauvis", item = "iron-plate", window = "one_fortnight" })
check("an unknown window is a provider_error", (not bad_window.ok) and bad_window.e == "provider_error", F.encode(bad_window))

local hello = call("aab-test-provider", "hello", { force = "player", name = "Bob" })
check("the test provider answers", hello.ok and hello.r.greeting:find("Bob"), F.encode(hello))
local boom = call("aab-test-provider", "boom", { force = "player" })
check("the test provider's error is a provider_error", (not boom.ok) and boom.e == "provider_error", F.encode(boom))

-- ── ask, poll, answer, answers ────────────────────────────────────────
local qid = remote.call("ai-agent-bridge-v1", "ask", { text = "what forces are there", force = "player", player_index = 1 })
check("ask returns an id", qid == 1, qid)
check("ask writes a question line", (S.files["ai-agent-bridge/events.jsonl"] or ""):find('"event":"question"') ~= nil,
      S.files["ai-agent-bridge/events.jsonl"])

local polled = rpc({ op = "poll", after = 0 })
check("poll returns the question", polled.ok and polled.r[1].text == "what forces are there", F.encode(polled))

local answered = rpc({ op = "answer", qid = qid, artifact = {
  shape = "summary", title = "Forces", lines = { "one force, player", "two players" } } })
check("answer ok", answered.ok, F.encode(answered))
check("summary went to chat", S.printed[#S.printed].text:find("one force, player") ~= nil,
      S.printed[#S.printed] and S.printed[#S.printed].text)
check("chat put the title first", S.printed[#S.printed].text:find("Forces\none force") ~= nil,
      S.printed[#S.printed].text)
check("answer writes an answer line", S.files["ai-agent-bridge/events.jsonl"]:find('"event":"answer"') ~= nil)

local answers = rpc({ op = "answers", after = 0 })
check("answers returns the rendered lines", answers.ok and answers.r[1].lines[1] == "Forces", F.encode(answers))
check("answers reports the shape", answers.ok and answers.r[1].shape == "summary")
check("answers reports the asker", answers.ok and answers.r[1].player_index == 1)
check("answers after a cursor is empty", (function()
  local later = rpc({ op = "answers", after = 99 })
  return later.ok and next(later.r) == nil
end)())

-- ── chat only: a table prints to the whole server ─────────────────────
local qid2 = remote.call("ai-agent-bridge-v1", "ask", { text = "table of players", force = "player", player_index = 1 })
local before = #S.printed
local a2 = rpc({ op = "answer", qid = qid2, artifact = {
  shape = "table", title = "Players", columns = { "name", "online" },
  rows = { { "Bob", "yes" }, { "Ann", "no" } } } })
check("table answer ok", a2.ok, F.encode(a2))
check("a table prints to chat, once", #S.printed == before + 1, #S.printed .. " vs " .. before)
check("a global answer goes to the whole server", S.printed[#S.printed].who == "*", S.printed[#S.printed].who)
check("the table is one line per row with columns first",
      S.printed[#S.printed].text:find("Players\nname | online\nBob | yes\nAnn | no", 1, true) ~= nil,
      S.printed[#S.printed].text)
-- The test provider is also a scope provider, so every answer here carries
-- its global badge between the name and the colon.
check("the line opens with the companion's name, the badge, then a colon",
      S.printed[#S.printed].text:find("^%[AI Agent Bridge%] %[color=[^%]]+%]%[GLOBAL%]%[/color%]: Players\n") ~= nil,
      S.printed[#S.printed].text)
check("no popup frame exists any more", S.bob.gui.screen["aab_answer_frame"] == nil)

-- ── audience setting: asker only ──────────────────────────────────────
S.settings["aab-answer-audience"].value = "asker"
local qid4 = remote.call("ai-agent-bridge-v1", "ask", { text = "t", force = "player", player_index = 1 })
before = #S.printed
rpc({ op = "answer", qid = qid4, artifact = { shape = "table", columns = { "a" }, rows = { { "b" } } } })
check("audience asker prints to the asker alone", #S.printed == before + 1 and S.printed[#S.printed].who == "Bob",
      S.printed[#S.printed].who)
S.settings["aab-answer-audience"].value = "server"

-- ── the session marker ────────────────────────────────────────────────
local qid5 = remote.call("ai-agent-bridge-v1", "ask", { text = "n", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid5, artifact = { shape = "notice", text = "all good", session = { name = "", fresh = true } } })
check("a fresh session is marked", S.printed[#S.printed].text:find("[/color] (new session): all good", 1, true) ~= nil,
      S.printed[#S.printed].text)
local qid5b = remote.call("ai-agent-bridge-v1", "ask", { text = "n", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid5b, artifact = { shape = "notice", text = "iron", session = { name = "iron", fresh = true } } })
check("a fresh named session names itself", S.printed[#S.printed].text:find("(new session #iron): iron", 1, true) ~= nil,
      S.printed[#S.printed].text)
local qid5c = remote.call("ai-agent-bridge-v1", "ask", { text = "n", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid5c, artifact = { shape = "notice", text = "same", session = { name = "iron", fresh = false } } })
check("a continued session carries no marker", S.printed[#S.printed].text:find("new session", 1, true) == nil
      and S.printed[#S.printed].text:find("[/color]: same", 1, true) ~= nil, S.printed[#S.printed].text)
local qid5d = remote.call("ai-agent-bridge-v1", "ask", { text = "n", force = "player", player_index = 1 })
local bad2 = rpc({ op = "answer", qid = qid5d, artifact = { shape = "notice", text = "x", session = "iron" } })
check("a session field that is not an object is refused", not bad2.ok and bad2.e == "bad_artifact", F.encode(bad2))

-- ── chat scope: private questions stay inside their audience ──────────
-- Asks as Bob through the /ask command, the way a player does.
local function ask_cmd(text)
  local before_id = tonumber(rpc({ op = "status" }).r.last_id)
  S.commands["ask"]({ parameter = text, player_index = 1, tick = S.tick })
  local after_id = tonumber(rpc({ op = "status" }).r.last_id)
  check("/ask " .. text .. " created a question", after_id == before_id + 1)
  return after_id
end
-- A scope provider that puts Bob's force in team mode, with a badge on
-- both states the way a team-chat mod stamps its own lines.
local team_mode = false
remote.add_interface("test-scope", {
  chat_scope_v1 = function(player_index, text)
    local player = game.get_player(player_index)
    if not player then return nil end
    if team_mode and text:sub(1, 1) ~= "!" then
      return { key = player.force.name, private = true, audience = { force = player.force.name },
               label = "Team", tag = "[TEAM]" }
    end
    return { key = "global", private = false, tag = "[GLOBAL]" }
  end,
})
local qid7 = ask_cmd("who is online")
local poll7 = rpc({ op = "poll", after = qid7 - 1, limit = 1 })
check("a global question polls with scope global and no private flag",
      poll7.ok and poll7.r[1].scope == "global" and poll7.r[1].private == nil, F.encode(poll7))
check("a player's question polls with the surface they are looking at, and the physical one when it differs",
      poll7.r[1].surface == "nauvis" and poll7.r[1].physical_surface == "platform-1", F.encode(poll7))
rpc({ op = "answer", qid = qid7, artifact = { shape = "notice", text = "Bob" } })
check("the global tag sits after the name (the first provider by name supplies it)",
      S.printed[#S.printed].text:find("^%[AI Agent Bridge%] %[color=[^%]]+%]%[GLOBAL%]%[/color%]: Bob$") ~= nil,
      S.printed[#S.printed].text)
check("and the answer went to the server", S.printed[#S.printed].who == "*")

team_mode = true
local qid8 = ask_cmd("who is online")
local poll8 = rpc({ op = "poll", after = qid8 - 1, limit = 1 })
check("a private question polls with its force as scope and private true",
      poll8.ok and poll8.r[1].scope == "player" and poll8.r[1].private == true, F.encode(poll8))
team_mode = false  -- the team flips back before the answer lands
rpc({ op = "answer", qid = qid8, artifact = { shape = "notice", text = "Bob" } })
check("a private answer prints to the force it was asked in, whatever the channel is now",
      S.printed[#S.printed].who == "force:player", S.printed[#S.printed].who)
check("with the team tag", S.printed[#S.printed].text == "[AI Agent Bridge] [TEAM]: Bob", S.printed[#S.printed].text)

team_mode = true
local qid9 = ask_cmd("!shout")
local poll9 = rpc({ op = "poll", after = qid9 - 1, limit = 1 })
check("the provider sees the line as typed, so a shout is global", poll9.ok and poll9.r[1].scope == "global", F.encode(poll9))

-- A provider whose force is gone falls back to the asker alone.
local qid10 = remote.call("ai-agent-bridge-v1", "ask", { text = "q", player_index = 1,
  scope = { key = "team-9", private = true, audience = { force = "team-9" }, tag = "[TEAM]" } })
rpc({ op = "answer", qid = qid10, artifact = { shape = "notice", text = "secret" } })
check("a private answer with no such force reaches the asker only", S.printed[#S.printed].who == "Bob", S.printed[#S.printed].who)

-- A mod may hand in a scope of its own through the interface.
local qid11 = remote.call("ai-agent-bridge-v1", "ask", { text = "q", force = "team-3",
  scope = { key = "team-3", private = true, audience = { force = "team-3" } } })
rpc({ op = "answer", qid = qid11, artifact = { shape = "notice", text = "for team 3" } })
check("an interface caller's scope prints to that force", S.printed[#S.printed].who == "force:team-3", S.printed[#S.printed].who)

-- Two providers: the private one wins; a broken one is skipped.
remote.add_interface("test-scope-broken", { chat_scope_v1 = function() error("scope boom") end })
remote.add_interface("test-scope-global", { chat_scope_v1 = function() return { key = "global", private = false, tag = "[G2]" } end })
team_mode = true
local qid12 = ask_cmd("again")
local poll12 = rpc({ op = "poll", after = qid12 - 1, limit = 1 })
check("the private provider wins over a global one and a broken one",
      poll12.ok and poll12.r[1].private == true and poll12.r[1].scope == "player", F.encode(poll12))
team_mode = false
S.interfaces["test-scope"] = nil
S.interfaces["test-scope-broken"] = nil
S.interfaces["test-scope-global"] = nil

-- ── the question echo, sprites for bare names, force labels ───────────
before = #S.printed
local qid_echo = ask_cmd("what forces are there")
check("a /ask question is echoed to its audience with the asker's name",
      #S.printed == before + 1 and S.printed[#S.printed].who == "*"
      and S.printed[#S.printed].text:find("Bob asked: what forces are there", 1, true) ~= nil,
      S.printed[#S.printed].text)
check("the echo carries the channel tag after the name",
      S.printed[#S.printed].text:find("^%[AI Agent Bridge%] %[color=[^%]]+%]%[GLOBAL%]%[/color%] Bob asked") ~= nil,
      S.printed[#S.printed].text)
rpc({ op = "answer", qid = qid_echo, artifact = { shape = "summary",
  lines = { "iron-ore/min 15, [img=item.iron-ore] again, coal, crude-oil, logistics-2, assembling-machine-2, no-such-thing, [color=red]iron-plate[/color]" } } })
local decorated = S.printed[#S.printed].text
check("a bare item name renders as its sprite", decorated:find("[img=item.iron-ore]/min 15", 1, true) ~= nil, decorated)
check("an existing sprite tag is left alone", decorated:find("[img=item.iron-ore] again", 1, true) ~= nil
      and decorated:find("[img=item.[img=", 1, true) == nil, decorated)
check("a single-word name stays prose", decorated:find(", coal,", 1, true) ~= nil, decorated)
check("fluids, technologies and entities decorate by class",
      decorated:find("[img=fluid.crude-oil], [img=technology.logistics-2], [img=entity.assembling-machine-2]", 1, true) ~= nil, decorated)
check("an unknown hyphenated word stays as it is", decorated:find(", no-such-thing,", 1, true) ~= nil, decorated)
check("a name inside a colour tag still decorates", decorated:find("[color=red][img=item.iron-plate][/color]", 1, true) ~= nil, decorated)

local labels_reply = rpc({ op = "labels" })
check("the labels op lists the test provider's label, rich text stripped",
      labels_reply.ok and labels_reply.r[1] and labels_reply.r[1].name == "player" and labels_reply.r[1].label == "The Engineers",
      F.encode(labels_reply))
local by_label = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "list_players", a = { force = "the engineers" } })
check("a tool called with a force's label resolves it to the force", by_label.ok and by_label.r.force == "player", F.encode(by_label))
local no_label = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "list_players", a = { force = "Team Nobody" } })
check("a label nobody has is still an unknown force", not no_label.ok and no_label.e == "provider_error"
      and tostring(no_label.m):find("unknown force", 1, true) ~= nil, F.encode(no_label))
check("the labels op lists team-3 as well", labels_reply.r[2] and labels_reply.r[2].name == "team-3" and labels_reply.r[2].label == "Team Losers",
      F.encode(labels_reply))
local qid_label = ask_cmd("who is quiet")
rpc({ op = "answer", qid = qid_label, artifact = { shape = "summary",
  lines = { "team-3 is quiet; the player force is not; [color=red]team-3[/color] again; team-30 is nobody" } } })
local labelled = S.printed[#S.printed].text
check("a force name renders as its label", labelled:find("Team Losers is quiet", 1, true) ~= nil, labelled)
check("a plain-word force name is never swapped", labelled:find("the player force is not", 1, true) ~= nil, labelled)
check("a force name inside a colour tag is still swapped", labelled:find("[color=red]Team Losers[/color]", 1, true) ~= nil, labelled)
check("a longer name is not a partial match", labelled:find("team-30 is nobody", 1, true) ~= nil, labelled)

-- ── where things are ──────────────────────────────────────────────────
local labs = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", name = "lab" } })
check("find_entities by name finds the labs with gps tags", labs.ok and labs.r.total == 2 and labs.r.shown == 2
      and labs.r.entities[1].gps == "[gps=10,-5,nauvis]" and labs.r.entities[2].x == 13, F.encode(labs))
-- Two engine passes: built first, then ghosts under what is left of the cap.
check("find_entities passes force and the scan cap to the engine, ghosts second",
      S.last_find_filter.force == "player" and S.last_find_filter.limit == 2000 - 2 and S.last_find_filter.ghost_name == "lab",
      F.encode(S.last_find_filter))
local repair = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", recipe = "repair-pack" } })
check("a recipe search scans crafters only, not every entity the force owns",
      repair.ok and repair.r.scanned == 6, F.encode(repair))
check("find_entities by recipe keeps only the machine on that recipe", repair.ok and repair.r.total == 1
      and repair.r.entities[1].name == "assembling-machine-2" and repair.r.entities[1].gps == "[gps=-20,33,nauvis]"
      and repair.r.entities[1].recipe == "repair-pack", F.encode(repair))
local furnaces = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", type = "furnace", limit = 1 } })
check("find_entities by type works and reports scanned", furnaces.ok and furnaces.r.total == 2 and furnaces.r.scanned == 2
      and furnaces.r.shown == 1 and furnaces.r.truncated == false, F.encode(furnaces))
local no_recipe = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", recipe = "repair-kit" } })
check("an unknown recipe is found=false with close names suggested", no_recipe.ok and no_recipe.r.found == false
      and no_recipe.r.suggestions[1] == "repair-pack", F.encode(no_recipe))
local by_product = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", product = "iron-gear-wheel" } })
check("find_entities by product finds the machine whose recipe makes it", by_product.ok and by_product.r.total == 1
      and by_product.r.entities[1].recipe == "iron-gear-wheel" and by_product.r.entities[1].x == -24, F.encode(by_product))
local no_product = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", product = "military science" } })
check("an unknown product suggests item names", no_product.ok and no_product.r.found == false
      and no_product.r.suggestions[1] == "military-science-pack", F.encode(no_product))
local no_filter = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis" } })
check("no filter at all is an error in the tool's own words", not no_filter.ok and tostring(no_filter.m):find("at least one", 1, true) ~= nil, F.encode(no_filter))
local ghosts = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", recipe = "iron-chest" } })
check("ghost assemblers match by the recipe set on them, and say they are ghosts", ghosts.ok and ghosts.r.total == 2
      and ghosts.r.entities[1].ghost == true and ghosts.r.entities[1].name == "assembling-machine-2"
      and ghosts.r.entities[1].recipe == "iron-chest" and ghosts.r.entities[1].gps == "[gps=-15,-38,nauvis]", F.encode(ghosts))
local by_type_all = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", type = "assembling-machine" } })
check("a type filter finds built machines and ghosts of that type", by_type_all.ok and by_type_all.r.total == 4, F.encode(by_type_all))
local only_ghosts = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", type = "assembling-machine", ghost = true } })
check("ghost=true keeps only ghosts", only_ghosts.ok and only_ghosts.r.total == 2 and only_ghosts.r.entities[2].ghost == true, F.encode(only_ghosts))
local only_built = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", name = "assembling-machine-2", ghost = false } })
check("ghost=false keeps only built entities", only_built.ok and only_built.r.total == 2 and only_built.r.entities[1].ghost == nil, F.encode(only_built))
local furnace_ghost = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "nauvis", type = "furnace" } })
check("a ghost with no recipe still lists, without one, after the built ones", furnace_ghost.ok and furnace_ghost.r.total == 2
      and furnace_ghost.r.entities[1].ghost == nil and furnace_ghost.r.entities[2].ghost == true
      and furnace_ghost.r.entities[2].recipe == nil, F.encode(furnace_ghost))
local elsewhere = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "find_entities", a = { force = "player", surface = "platform-1", name = "lab" } })
check("another surface has none", elsewhere.ok and elsewhere.r.total == 0 and elsewhere.r.found == true, F.encode(elsewhere))
local where_bob = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "locate_player", a = { force = "player", player = "Bob" } })
check("locate_player finds Bob's character with a gps tag, and says what he is looking at",
      where_bob.ok and where_bob.r.found == true and where_bob.r.gps == "[gps=2,3,platform-1]"
      and where_bob.r.surface == "platform-1" and where_bob.r.viewing == "nauvis" and where_bob.r.connected == true, F.encode(where_bob))
local where_nobody = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "locate_player", a = { force = "player", player = "Zed" } })
check("locate_player on an unknown name is found=false", where_nobody.ok and where_nobody.r.found == false, F.encode(where_nobody))

-- ── safety: the rpc command, ask rate limits, private refusals ─────────
local replies_before = #S.rcon_replies
before = #S.printed
S.commands["aab-rpc"]({ parameter = F.encode({ v = 1, op = "status" }), player_index = 1, tick = S.tick })
check("a player running /aab-rpc gets a refusal and no reply is produced",
      #S.rcon_replies == replies_before and #S.printed == before + 1 and S.printed[#S.printed].who == "Bob"
      and S.printed[#S.printed].text:find("over RCON", 1, true) ~= nil, S.printed[#S.printed].text)
S.commands["aab-rpc"]({ parameter = F.encode({ v = 1, op = "answer", qid = 1, artifact = { shape = "notice", text = "forged" } }), player_index = 1, tick = S.tick })
check("a player cannot forge an answer through /aab-rpc", S.printed[#S.printed].text:find("forged", 1, true) == nil)

S.settings["aab-ask-cooldown-seconds"].value = 5
S.tick = S.tick + 5 * 60  -- clear of Bob's earlier asks this tick
local qcount_before = tonumber(rpc({ op = "status" }).r.last_id)
before = #S.printed
S.commands["ask"]({ parameter = "first", player_index = 1, tick = S.tick })
S.commands["ask"]({ parameter = "second at once", player_index = 1, tick = S.tick })
check("a second ask inside the cooldown is refused privately, with no echo and no question",
      tonumber(rpc({ op = "status" }).r.last_id) == qcount_before + 1 and #S.printed == before + 2
      and S.printed[#S.printed].who == "Bob" and S.printed[#S.printed].text:find("wait", 1, true) ~= nil,
      S.printed[#S.printed].text)
S.tick = S.tick + 5 * 60
S.commands["ask"]({ parameter = "after the cooldown", player_index = 1, tick = S.tick })
check("after the cooldown the ask goes through", tonumber(rpc({ op = "status" }).r.last_id) == qcount_before + 2)
S.settings["aab-ask-cooldown-seconds"].value = 0

S.settings["aab-asks-per-minute"].value = 2
S.tick = S.tick + 60 * 60  -- a fresh minute
local minute_before = tonumber(rpc({ op = "status" }).r.last_id)
S.commands["ask"]({ parameter = "one", player_index = 1, tick = S.tick })
S.commands["ask"]({ parameter = "two", player_index = 1, tick = S.tick })
S.commands["ask"]({ parameter = "three", player_index = 1, tick = S.tick })
check("the server-wide per-minute cap refuses the third ask",
      tonumber(rpc({ op = "status" }).r.last_id) == minute_before + 2
      and S.printed[#S.printed].text:find("this minute", 1, true) ~= nil, S.printed[#S.printed].text)
S.tick = S.tick + 60 * 60
S.commands["ask"]({ parameter = "next minute", player_index = 1, tick = S.tick })
check("the next minute allows asking again", tonumber(rpc({ op = "status" }).r.last_id) == minute_before + 3)
S.settings["aab-asks-per-minute"].value = 0

local qid_private = tonumber(rpc({ op = "status" }).r.last_id)
before = #S.printed
rpc({ op = "answer", qid = qid_private, artifact = { shape = "notice", text = "over quota", to_asker = true } })
check("a to_asker answer prints to the asker alone", #S.printed == before + 1 and S.printed[#S.printed].who == "Bob"
      and S.printed[#S.printed].text:find("over quota", 1, true) ~= nil, S.printed[#S.printed].who)
local bad_flag = rpc({ op = "answer", qid = qid_private, artifact = { shape = "notice", text = "x", to_asker = "yes" } })
check("to_asker must be a boolean (already answered short-circuits after the check)", bad_flag.ok or bad_flag.e == "bad_artifact")

-- A question from a mod, with no player and no scope, goes to the server.
local qid6 = remote.call("ai-agent-bridge-v1", "ask", { text = "from a mod", force = "player" })
before = #S.printed
rpc({ op = "answer", qid = qid6, artifact = { shape = "notice", text = "no asker here" } })
check("an answer with no connected asker goes to the server", #S.printed == before + 1 and S.printed[#S.printed].who == "*")

-- A player who has left still gets the answer, to the server.
local qid7b = remote.call("ai-agent-bridge-v1", "ask", { text = "gone", force = "player", player_index = 2 })
rpc({ op = "answer", qid = qid7b, artifact = { shape = "table", columns = { "a" }, rows = { { "b" } } } })
check("a disconnected asker's answer goes to the server", S.printed[#S.printed].who == "*")

-- ── the chat prefix ───────────────────────────────────────────────────
local chat_handler = S.handlers[defines.events.on_console_chat]
check("one on_console_chat handler is registered", chat_handler ~= nil)

local qcount = function() return #rpc({ op = "poll", after = 0 }).r end
local before_q = qcount()
chat_handler({ player_index = 1, message = "ordinary chat" })
check("chat is logged", S.files["ai-agent-bridge/events.jsonl"]:find('"message":"ordinary chat"') ~= nil)
check("no prefix means no question", qcount() == before_q)

S.settings["aab-chat-prefix"].value = "? "
chat_handler({ player_index = 1, message = "? how much iron" })
local polled2 = rpc({ op = "poll", after = 0 }).r
check("a prefixed line becomes a question", polled2[#polled2].text == "how much iron", F.encode(polled2[#polled2]))
check("the question carries the player and force",
      polled2[#polled2].player_index == 1 and polled2[#polled2].force == "player")

before_q = qcount()
chat_handler({ player_index = 1, message = "?" })
check("the prefix alone is not a question", qcount() == before_q)
chat_handler({ player_index = nil, message = "? from the server console" })
check("a server console line is not a question", qcount() == before_q)

-- ── rendering caps and sanitising ─────────────────────────────────────
local qid8 = remote.call("ai-agent-bridge-v1", "ask", { text = "x", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid8, artifact = { shape = "summary",
  lines = { "a\nSomeone: forged", "b", "c", "d" } } })
local rendered = rpc({ op = "answers", after = qid8 - 1 }).r[1]
check("summary clips to three lines", #rendered.lines == 3, F.encode(rendered.lines))
check("a newline inside a value cannot forge a line", rendered.lines[1] == "a Someone: forged", rendered.lines[1])

local qid9 = remote.call("ai-agent-bridge-v1", "ask", { text = "y", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid9, artifact = { shape = "summary", lines = { string.rep("na", 500) } } })
local long = rpc({ op = "answers", after = qid9 - 1 }).r[1]
check("a long line is clipped to 640 bytes", #long.lines[1] == 640, #long.lines[1])

-- 640 is not a multiple of three, so a line of three-byte characters has to
-- stop one character short rather than leave half a sequence behind.
local qid9b = remote.call("ai-agent-bridge-v1", "ask", { text = "y2", force = "player", player_index = 1 })
rpc({ op = "answer", qid = qid9b, artifact = { shape = "summary", lines = { string.rep("\u{20AC}", 300) } } })
local utf8_line = rpc({ op = "answers", after = qid9b - 1 }).r[1].lines[1]
check("a clip never splits a UTF-8 sequence", #utf8_line == 639 and #utf8_line % 3 == 0, #utf8_line)

local qid10 = remote.call("ai-agent-bridge-v1", "ask", { text = "z", force = "player", player_index = 1 })
local helper = rpc({ op = "answer", qid = qid10, artifact = { shape = "grid" } })
check("an artifact naming a helper is refused, not called",
      (not helper.ok) and helper.e == "bad_artifact" and helper.m:find("unknown shape") ~= nil, F.encode(helper))
check("a refused artifact leaves nothing behind in answers",
      next(rpc({ op = "answers", after = qid10 - 1 }).r) == nil)

-- ── errors ────────────────────────────────────────────────────────────
check("answering an unknown question is no_question", (function()
  local r = rpc({ op = "answer", qid = 9999, artifact = { shape = "notice", text = "x" } })
  return (not r.ok) and r.e == "no_question"
end)())
check("an unknown op is bad_op", (function()
  local r = rpc({ op = "nope" }); return (not r.ok) and r.e == "bad_op"
end)())
check("a bad version is bad_version", (function()
  local r = rpc({ op = "status", v = 2 }); return (not r.ok) and r.e == "bad_version"
end)())
check("an unknown provider is no_provider", (function()
  local r = call("nobody", "nothing", {}); return (not r.ok) and r.e == "no_provider"
end)())

-- ── the events setting really gates the file ──────────────────────────
S.settings["aab-events-enabled"].value = false
local size = #S.files["ai-agent-bridge/events.jsonl"]
remote.call("ai-agent-bridge-v1", "ask", { text = "quiet", force = "player" })
check("turning events off stops the file growing", #S.files["ai-agent-bridge/events.jsonl"] == size)


-- ── every shape prints to chat, once ──────────────────────────────────
local function answer_as_bob(artifact)
  local id = remote.call("ai-agent-bridge-v1", "ask", { text = "q", force = "player", player_index = 1 })
  local printed_before = #S.printed
  rpc({ op = "answer", qid = id, artifact = artifact })
  return #S.printed - printed_before, S.printed[#S.printed].text
end

local n, text = answer_as_bob({ shape = "comparison", columns = { "north", "south" },
                                rows = { { label = "iron", a = "1", b = "2" } } })
check("a comparison prints once, one line per row", n == 1 and text:find("- iron: 1 vs 2", 1, true) ~= nil, text)
n, text = answer_as_bob({ shape = "comparison", columns = { "", "team-3" }, rows = { { label = "iron", a = "1", b = "2" } } })
check("a blank comparison column falls back to a letter", n == 1 and text:find("A  vs  Team Losers", 1, true) ~= nil, text)
n, text = answer_as_bob({ shape = "list", items = { "a", "b", "c", "d" } })
check("a list of four prints once", n == 1 and text:find("- d", 1, true) ~= nil, text)
n = answer_as_bob({ shape = "summary", lines = { "one" } })
check("a summary prints once", n == 1)
n = answer_as_bob({ shape = "notice", text = "careful" })
check("a notice prints once", n == 1)

-- A short row is padded to the column count so the line still has every cell.
n, text = answer_as_bob({ shape = "table", columns = { "a", "b", "c" }, rows = { { "1" } } })
check("a short table row still prints", n == 1 and text:find("a | b | c\n1 |  | ", 1, true) ~= nil, text)

-- ── artifact validation, the answer op's gate ─────────────────────────
-- Contract item 5 and review findings 18/24: a bad artifact is bad_artifact,
-- nothing is rendered, and the question stays pending so it can be answered
-- again.
local shapes_mod = require("scripts.render_shapes")
check("a non-scalar leaf renders as a literal, never as a heap address",
      shapes_mod.summary({ lines = { {} } })[1] == "(unrenderable value)",
      F.encode(shapes_mod.summary({ lines = { {} } })))

local function ask_bob(text)
  return remote.call("ai-agent-bridge-v1", "ask", { text = text or "q", force = "player", player_index = 1 })
end

local function rejects(what, artifact)
  local id = ask_bob(what)
  local printed_before = #S.printed
  local reply = rpc({ op = "answer", qid = id, artifact = artifact })
  local still_pending = false
  for _, row in ipairs(rpc({ op = "poll", after = id - 1 }).r) do
    if row.id == id then still_pending = true end
  end
  check("a " .. what .. " artifact is bad_artifact", (not reply.ok) and reply.e == "bad_artifact", F.encode(reply))
  check("a " .. what .. " artifact prints nothing", #S.printed == printed_before)
  check("a " .. what .. " artifact leaves its question pending", still_pending)
  return reply
end

rejects("numeric comparison rows", { shape = "comparison", columns = { "a", "b" }, rows = { 1, 2 } })
rejects("table row of tables", { shape = "table", columns = { "a" }, rows = { { {} } } })
rejects("summary with a number for lines", { shape = "summary", lines = 7 })
rejects("notice with a table for text", { shape = "notice", text = { a = 1 } })
rejects("shapeless", { title = "no shape here" })
rejects("list-with-an-object", { shape = "list", items = { named = "not an array" } })

-- A question the service gives up on has to be able to end visibly: it refuses
-- the artifact, then delivers a notice to the same qid, which the companion
-- renders and marks answered so the question leaves the poll page (second
-- review-fix contract 3).
local refused = ask_bob("refused then told")
local printed_before_notice = #S.printed
check("the bad artifact is refused",
      (not rpc({ op = "answer", qid = refused, artifact = { shape = "nope" } }).ok))
local notice = rpc({ op = "answer", qid = refused, artifact = { shape = "notice",
  text = "I could not put that answer into a shape the game can show" } })
check("a notice after a refusal is accepted", notice.ok, F.encode(notice))
check("the asker is told something", #S.printed == printed_before_notice + 1
      and S.printed[#S.printed].text:find("could not put that answer") ~= nil,
      S.printed[#S.printed] and S.printed[#S.printed].text)
check("the question then leaves the poll page", (function()
  for _, row in ipairs(rpc({ op = "poll", after = refused - 1 }).r) do
    if row.id == refused then return false end
  end
  return true
end)())

local good = ask_bob("fine")
check("a valid artifact is still accepted",
      rpc({ op = "answer", qid = good, artifact = { shape = "notice", text = "all clear" } }).ok)
local printed_after = #S.printed
check("answering the same question twice renders once",
      rpc({ op = "answer", qid = good, artifact = { shape = "notice", text = "again" } }).ok
      and #S.printed == printed_after)

-- ── poll: unanswered only, bounded, with the asker's name ─────────────
check("an answered question drops out of poll", (function()
  for _, row in ipairs(rpc({ op = "poll", after = good - 1 }).r) do
    if row.id == good then return false end
  end
  return true
end)())

local named = remote.call("ai-agent-bridge-v1", "ask", { text = "named", force = "player", player_index = 1 })
local named_row
for _, row in ipairs(rpc({ op = "poll", after = named - 1 }).r) do
  if row.id == named then named_row = row end
end
check("poll carries the asker's name", named_row and named_row.player_name == "Bob", F.encode(named_row or {}))
check("poll still carries the index and the force",
      named_row and named_row.player_index == 1 and named_row.force == "player")

local from_gone = remote.call("ai-agent-bridge-v1", "ask", { text = "gone asked", force = "player", player_index = 2 })
local gone_row
for _, row in ipairs(rpc({ op = "poll", after = from_gone - 1 }).r) do
  if row.id == from_gone then gone_row = row end
end
check("a disconnected asker's name is still on the row", gone_row and gone_row.player_name == "Ann",
      F.encode(gone_row or {}))

-- A LuaObject or a wrong type in a spec must never reach storage: one bad row
-- would make every later poll unencodable (review finding 25).
local poisoned = remote.call("ai-agent-bridge-v1", "ask",
  { text = "poisoned", force = S.force, player_index = "bob" })
local poisoned_reply = rpc({ op = "poll", after = poisoned - 1 })
check("a LuaObject force is dropped rather than stored", poisoned_reply.ok, F.encode(poisoned_reply))
local poisoned_row
for _, row in ipairs(poisoned_reply.r) do if row.id == poisoned then poisoned_row = row end end
check("the poisoned row has no force and no player_index",
      poisoned_row and poisoned_row.force == nil and poisoned_row.player_index == nil,
      F.encode(poisoned_row or {}))

-- ── status last_id ────────────────────────────────────────────────────
local st = rpc({ op = "status" })
check("status reports the highest question id", st.r.last_id == poisoned, st.r.last_id .. " vs " .. poisoned)
check("status still reports the pending count", type(st.r.pending) == "number")

-- ── bounded enumerations ──────────────────────────────────────────────
local players = call("ai-agent-bridge-tools", "list_players", { force = "player" })
check("list_players defaults to connected players", players.ok and players.r.total == 1
      and players.r.players[1].name == "Bob", F.encode(players))
check("list_players reports how many the force has ever had", players.ok and players.r.known == 2)
check("list_players reports shown", players.ok and players.r.shown == 1)

local everyone = call("ai-agent-bridge-tools", "list_players", { force = "player", connected = false })
check("list_players connected=false lists every player", everyone.ok and everyone.r.total == 2
      and everyone.r.players[1].name == "Ann", F.encode(everyone))

local one_player = call("ai-agent-bridge-tools", "list_players",
  { force = "player", connected = false, limit = 1 })
check("list_players cuts to limit but still reports the total",
      one_player.ok and one_player.r.shown == 1 and one_player.r.total == 2
      and one_player.r.players[1].name == "Ann", F.encode(one_player))

local silly_limit = call("ai-agent-bridge-tools", "list_players",
  { force = "player", connected = false, limit = 0 })
check("list_players clamps a limit of zero up to one", silly_limit.ok and silly_limit.r.shown == 1)

-- ── list_players all=true: the sweep the phase4 follow-up contract added ──
-- Load-bearing per that contract: the sweep reply must carry no top-level
-- `force` field, since that absence is the only thing the service can use to
-- tell a 1.0.4 sweep from a 1.0.3 single-force reply. Mutation-tested: adding
-- `force = "player"` to the sweep's own return, the exact "for symmetry or
-- tidiness" mistake the contract names, turns every check below red.
local pl_all = call("ai-agent-bridge-tools", "list_players", { all = true })
check("list_players all=true carries no top-level force field",
      pl_all.ok and pl_all.r.force == nil, F.encode(pl_all))
check("list_players all=true returns connected players across every force",
      pl_all.ok and pl_all.r.total == 1 and pl_all.r.shown == 1
      and pl_all.r.players[1].name == "Bob" and pl_all.r.players[1].force == "player"
      and pl_all.r.players[1].connected == true, F.encode(pl_all))
check("list_players all=true row drops admin", pl_all.ok and pl_all.r.players[1].admin == nil,
      F.encode(pl_all))

local pl_all_everyone = call("ai-agent-bridge-tools", "list_players",
  { all = true, connected = false })
check("list_players all=true connected=false lists every player on every force, by name",
      pl_all_everyone.ok and pl_all_everyone.r.total == 2
      and pl_all_everyone.r.players[1].name == "Ann" and pl_all_everyone.r.players[1].force == "player"
      and pl_all_everyone.r.players[2].name == "Bob", F.encode(pl_all_everyone))

local one_surface = call("ai-agent-bridge-tools", "list_surfaces", { force = "player", limit = 1 })
check("list_surfaces cuts to limit and reports the total",
      one_surface.ok and one_surface.r.shown == 1 and one_surface.r.total == 2
      and one_surface.r.surfaces[1].name == "nauvis", F.encode(one_surface))

local forces = call("ai-agent-bridge-tools", "list_forces", { force = "player" })
check("list_forces reports total, shown and what it left out",
      forces.ok and forces.r.total == 1 and forces.r.shown == 1 and forces.r.empty == 1,
      F.encode(forces))

-- ── the probe drops what it cannot use ────────────────────────────────
S.logs = {}
remote.add_interface("aab-ragged-provider", {
  agent_tools_v1 = function()
    return { v = 1, tools = {
      good     = { desc = "A usable tool.", params = { name = "string! who" } },
      no_desc  = { desc = 5 },
      bad_args = { desc = "Params are wrong.", params = { name = 7 } },
      not_even = "a string, not an entry",
    } }
  end,
  good = function() return { fine = true } end,
  no_desc = function() return {} end,
  bad_args = function() return {} end,
})
remote.add_interface("aab-angry-provider", {
  agent_tools_v1 = function() error("probe exploded") end,
})
remote.add_interface("aab-empty-provider", {
  agent_tools_v1 = function() return "not a manifest" end,
})

local catalog = rpc({ op = "tools" })
local ifaces = {}
-- Guarded: an over-cap catalog answers { ok = false }, and indexing the missing
-- r used to abort the run with a nil index, taking every later check with it.
check("the catalog op answered at all", catalog.ok, F.encode(catalog))
for _, provider in ipairs(catalog.r or {}) do ifaces[provider.iface] = provider.tools end
check("the catalog survives a broken provider", catalog.ok and ifaces["ai-agent-bridge-tools"] ~= nil)
check("a provider whose probe errors is dropped", ifaces["aab-angry-provider"] == nil)
check("a provider that returns no manifest is dropped", ifaces["aab-empty-provider"] == nil)
check("the good tool of a ragged provider survives", (ifaces["aab-ragged-provider"] or {}).good ~= nil)
check("a tool with a non-string desc is dropped", (ifaces["aab-ragged-provider"] or {}).no_desc == nil)
check("a tool whose params are not strings is dropped", (ifaces["aab-ragged-provider"] or {}).bad_args == nil)
check("a tool entry that is not a table is dropped", (ifaces["aab-ragged-provider"] or {}).not_even == nil)

local dropped_lines = 0
for _, message in ipairs(S.logs) do
  if message:find("dropped") then dropped_lines = dropped_lines + 1 end
end
check("every drop is one log line", dropped_lines == 5, dropped_lines .. ": " .. table.concat(S.logs, " | "))

local dropped_call = call("aab-ragged-provider", "no_desc", { force = "player" })
check("a dropped tool cannot be called", (not dropped_call.ok) and dropped_call.e == "no_tool",
      F.encode(dropped_call))
local good_call = call("aab-ragged-provider", "good", { force = "player" })
check("the surviving tool of a ragged provider still answers", good_call.ok and good_call.r.fine == true,
      F.encode(good_call))

-- ── the catalog in two steps: providers, then one manifest each ───────
-- Second review-fix contract 1. One tools reply puts every provider under one
-- byte cap, so the most verbose mod on the server decided whether anybody's
-- tools arrived at all.
local providers = rpc({ op = "providers" })
check("providers ok", providers.ok, F.encode(providers))
local summary = {}
local order = {}
for _, row in ipairs(providers.r) do
  summary[row.iface] = row
  order[#order + 1] = row.iface
end
check("providers lists the companion and the test provider",
      summary["ai-agent-bridge-tools"] ~= nil and summary["aab-test-provider"] ~= nil, F.encode(order))
check("providers is sorted by interface name", (function()
  for i = 2, #order do if order[i - 1] > order[i] then return false end end
  return true
end)(), table.concat(order, ","))
check("providers carries the probe version", summary["aab-test-provider"].v == 1)
check("providers lists tool names, not manifests",
      type(summary["aab-test-provider"].tools) == "table"
      and summary["aab-test-provider"].tools[1] == "boom"
      and summary["aab-test-provider"].tools[2] == "hello",
      F.encode(summary["aab-test-provider"].tools))
check("providers drops the same providers tools does",
      summary["aab-angry-provider"] == nil and summary["aab-empty-provider"] == nil)
check("providers drops the unusable tools of a ragged provider", (function()
  local names = summary["aab-ragged-provider"].tools
  return #names == 1 and names[1] == "good"
end)(), F.encode(summary["aab-ragged-provider"].tools))
check("a providers reply is small whatever the descriptions say",
      #F.encode(providers.r) < 600, #F.encode(providers.r))

local one = rpc({ op = "manifest", i = "aab-test-provider" })
check("manifest returns one provider's manifest verbatim",
      one.ok and one.r.v == 1 and one.r.tools.hello.desc:find("Greets someone")
      and one.r.tools.hello.params.name == "string! who to greet", F.encode(one))
check("manifest does not echo the interface name back", one.ok and one.r.iface == nil)
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
check("manifest carries every engine tool", mine.ok and mine.r.tools.game_time ~= nil
      and mine.r.tools.production_since ~= nil)
check("manifest drops what the catalog drops",
      rpc({ op = "manifest", i = "aab-ragged-provider" }).r.tools.no_desc == nil)
check("manifest of an unknown provider is no_provider", (function()
  local r = rpc({ op = "manifest", i = "nobody-at-all" })
  return (not r.ok) and r.e == "no_provider"
end)())
check("manifest with no i is no_provider, not a crash", (function()
  local r = rpc({ op = "manifest" })
  return (not r.ok) and r.e == "no_provider" and r.m:find("interface name") ~= nil
end)())
check("manifest of a provider whose probe errors is no_provider", (function()
  local r = rpc({ op = "manifest", i = "aab-angry-provider" })
  return (not r.ok) and r.e == "no_provider"
end)())

-- The whole point of the split: one fat provider must cost itself its tools and
-- nobody else theirs. Before this, its bytes pushed the single tools reply over
-- the cap and the agent answered every question with no tools at all.
local FAT = {}
for i = 1, 160 do
  FAT["tool_" .. i] = { desc = string.rep("a very wordy description. ", 12) }
end
remote.add_interface("aab-verbose-provider", {
  agent_tools_v1 = function() return { v = 1, tools = FAT } end,
})
local fat_catalog = rpc({ op = "tools" })
check("one fat provider takes the whole tools reply down",
      (not fat_catalog.ok) and fat_catalog.e == "too_large", F.encode(fat_catalog))
local still = rpc({ op = "providers" })
local fat_names = {}
for _, row in ipairs(still.r) do fat_names[row.iface] = #row.tools end
check("providers still answers with the fat provider installed",
      still.ok and fat_names["ai-agent-bridge-tools"] ~= nil and fat_names["aab-verbose-provider"] == 160,
      F.encode(fat_names))
check("every other provider's manifest still arrives",
      rpc({ op = "manifest", i = "ai-agent-bridge-tools" }).ok
      and rpc({ op = "manifest", i = "aab-test-provider" }).ok)
local fat_manifest = rpc({ op = "manifest", i = "aab-verbose-provider" })
check("the fat provider's own manifest is the only reply refused",
      (not fat_manifest.ok) and fat_manifest.e == "too_large", F.encode(fat_manifest))
-- The engine has no way to remove an interface; the fake does, and the rest of
-- this file wants a catalog that fits.
S.interfaces["aab-verbose-provider"] = nil

-- ── a call with no argument table ─────────────────────────────────────
-- Second review-fix contract 9: a client may leave `a` out entirely, and the
-- service's own JSON omits it for a tool with no arguments.
local no_args = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "list_forces" })
check("a zero-argument tool called with no argument table works",
      no_args.ok and no_args.r.total == 1, F.encode(no_args))
local needs_force = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "game_time" })
check("a tool that needs force says so in its own words",
      (not needs_force.ok) and needs_force.e == "provider_error"
      and needs_force.m:find("force is required") ~= nil, F.encode(needs_force))
check("the reply never leaks a Lua file name at the caller",
      (not needs_force.ok) and needs_force.m:find("%.lua") == nil, needs_force.m)
local provider_no_args = rpc({ op = "call", i = "aab-test-provider", f = "hello" })
check("a third mod's tool is handed a table, never nil",
      provider_no_args.ok and provider_no_args.r.greeting:find("stranger") ~= nil,
      F.encode(provider_no_args))
local bad_args = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "game_time", a = 7 })
check("an `a` that is not an object is bad_json, not a traceback",
      (not bad_args.ok) and bad_args.e == "bad_json", F.encode(bad_args))

-- ── every double in a reply is rounded ────────────────────────────────
-- Second review-fix contract 7 and review finding 6: a raw double reaches the
-- model as fifty-odd digits and spends a third of the reply's budget on them.
S.force.research_progress = 1 / 3
local rounded = call("ai-agent-bridge-tools", "current_research", { force = "player" })
check("current_research rounds progress to two decimals", rounded.ok and rounded.r.progress == "0.33",
      F.encode(rounded))
local queue_rounded = call("ai-agent-bridge-tools", "research_queue", { force = "player" })
check("research_queue rounds the force's progress too",
      queue_rounded.ok and queue_rounded.r.progress == "0.33" and queue_rounded.r.queue[1].progress == "0.33",
      F.encode(queue_rounded.r))
-- A fraction leaves as the quoted short string, six bytes for "0.33", never
-- the fifty-digit expansion the engine's JSON writer gives a double.
check("a rounded figure is short on the wire",
      #F.encode(rounded.r.progress) <= 6, F.encode(rounded.r.progress))
S.force.research_progress = 0.25

-- ── the big selftest op is clamped ────────────────────────────────────
S.rcon_replies = {}
S.commands["aab-rpc"]({ parameter = F.encode({ v = 1, op = "big", kb = 1e9 }), tick = S.tick })
local raw = S.rcon_replies[#S.rcon_replies]
check("big clamps kb to 4096", raw:find('"kb":4096') ~= nil and #raw < 5 * 1024 * 1024,
      #raw .. " bytes")

-- ── poll and answers are bounded ──────────────────────────────────────
local backlog = {}
for i = 1, 20 do backlog[i] = ask_bob("backlog " .. i) end
local default_page = rpc({ op = "poll", after = backlog[1] - 1 })
check("poll defaults to 16 rows", #default_page.r == 16, #default_page.r)
check("poll returns the oldest first", default_page.r[1].id == backlog[1])
check("poll with a limit pages", #rpc({ op = "poll", after = backlog[1] - 1, limit = 5 }).r == 5)
check("poll clamps a limit above the maximum",
      #rpc({ op = "poll", after = backlog[1] - 1, limit = 9999 }).r == 20)
check("poll clamps a limit of zero up to one",
      #rpc({ op = "poll", after = backlog[1] - 1, limit = 0 }).r == 1)
check("a second page starts after the first",
      rpc({ op = "poll", after = default_page.r[16].id, limit = 5 }).r[1].id == backlog[17])
check("answers takes a limit too", #rpc({ op = "answers", after = 0, limit = 2 }).r == 2)

-- ── the storage invariant ─────────────────────────────────────────────
-- CONTEXT.md invariant 2: the rpc command never writes storage outside the
-- answer op (and the Phase 0 write op).
local function snapshot() return F.encode(storage) end

-- A refused answer must not touch storage either: nothing is marked, nothing
-- is recorded, and the question stays exactly as it was (review findings
-- 18/24, contract item 5).
local invariant_id = ask_bob("invariant")
local untouched = snapshot()
rpc({ op = "answer", qid = invariant_id, artifact = { shape = "nope" } })
check("a refused answer leaves storage alone", snapshot() == untouched)

for _, req in ipairs({
  { op = "status" }, { op = "tools" }, { op = "poll", after = 0 },
  { op = "providers" }, { op = "manifest", i = "ai-agent-bridge-tools" },
  { op = "answers", after = 0 }, { op = "ping" },
  { op = "call", i = "ai-agent-bridge-tools", f = "list_forces", a = { force = "player" } },
  { op = "call", i = "aab-test-provider", f = "boom", a = {} },
  { op = "nope" },
}) do
  local was = snapshot()
  rpc(req)
  check("op " .. req.op .. " leaves storage alone", snapshot() == was, req.op)
end

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
