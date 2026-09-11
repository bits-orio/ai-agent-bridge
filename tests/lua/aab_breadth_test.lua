-- AI Agent Bridge - tests/lua/aab_breadth_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The eight breadth-addendum engine tools, driven through the aab-rpc call op
-- against the fake game. Companion to aab_test.lua, which covers the protocol
-- and the original tools; this file only exercises the new ones.

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
  return F.decode(S.rcon_replies[#S.rcon_replies])
end
local function call(fn, args) return rpc({ op = "call", i = "ai-agent-bridge-tools", f = fn, a = args }) end

-- ── the manifest carries all eight, with usable params ────────────────
-- Read through the manifest op, one provider's entry on its own, which is how
-- the service builds its catalog now (second review-fix contract 1).
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
local manifest = mine.ok and mine.r.tools or nil
check("the companion still has a manifest", manifest ~= nil, F.encode(mine))
check("the companion's own manifest fits one reply with room to spare",
      #F.encode(mine.r) < 7000, #F.encode(mine.r))
local NEW = { "research_queue", "tech_status", "logistics_summary", "entity_count",
              "evolution", "rockets", "game_time", "pollution" }
for _, name in ipairs(NEW) do
  local entry = manifest and manifest[name]
  check("manifest lists " .. name, entry ~= nil and type(entry.desc) == "string" and #entry.desc > 40,
        entry and entry.desc)
end
-- Every declared param follows the "<type>[!] <description>" grammar, and none
-- of them declares the reserved force parameter.
for _, name in ipairs(NEW) do
  for param, spec in pairs((manifest[name] or {}).params or {}) do
    check(name .. "." .. param .. " parses",
          spec:match("^(string|integer|number|boolean)!? ") ~= nil or
          spec:match("^%a+!? .") ~= nil, spec)
    check(name .. " does not declare force", param ~= "force", param)
  end
end

-- ── research_queue ────────────────────────────────────────────────────
local queue = call("research_queue", { force = "player" })
check("research_queue ok", queue.ok, F.encode(queue))
check("research_queue names the running technology", queue.ok and queue.r.researching == "logistics",
      queue.ok and F.encode(queue.r))
check("research_queue reports both entries", queue.ok and queue.r.queued == 2 and #queue.r.queue == 2)
check("research_queue keeps engine order", queue.ok and queue.r.queue[1].tech == "logistics"
      and queue.r.queue[2].tech == "logistics-2")
check("the head's progress comes from the force", queue.ok and queue.r.queue[1].progress == 0.25,
      queue.ok and queue.r.queue[1].progress)
check("a queued entry keeps its saved progress", queue.ok and queue.r.queue[2].progress == 0.5,
      queue.ok and queue.r.queue[2].progress)
check("research_queue carries research unit counts", queue.ok and queue.r.queue[2].units == 200)

local capped = call("research_queue", { force = "player", limit = 1 })
check("research_queue cuts to limit and keeps the total",
      capped.ok and capped.r.shown == 1 and capped.r.queued == 2, F.encode(capped))

-- A level-based technology queued three times is three entries resolving to one
-- LuaTechnology, so every row would otherwise report level 4, 2000 units and the
-- force's progress, and a model asked "how long is the plan" would understate the
-- cost by two thirds (second review-fix contract 5).
local repeated = { S.technologies.logistics, S.technologies["mining-productivity"],
                   S.technologies["mining-productivity"], S.technologies["mining-productivity"] }
for i = 1, 4 do S.research_queue[i] = repeated[i] end
local levels = call("research_queue", { force = "player" })
check("a repeated technology gets one row per queue entry",
      levels.ok and levels.r.queued == 4, F.encode(levels))
check("each repeat names the level it will research",
      levels.ok and levels.r.queue[2].level == 4 and levels.r.queue[3].level == 5
      and levels.r.queue[4].level == 6, levels.ok and F.encode(levels.r.queue))
check("only the first repeat carries units",
      levels.ok and levels.r.queue[2].units == 2000 and levels.r.queue[3].units == nil
      and levels.r.queue[4].units == nil, levels.ok and F.encode(levels.r.queue))
check("only the first repeat carries progress",
      levels.ok and levels.r.queue[2].progress == 0.2 and levels.r.queue[3].progress == nil,
      levels.ok and F.encode(levels.r.queue))
check("a summed unit count cannot double-count a repeat", (function()
  if not levels.ok then return false end
  local total = 0
  for _, row in ipairs(levels.r.queue) do total = total + (row.units or 0) end
  return total == 2020
end)(), levels.ok and F.encode(levels.r.queue))
S.research_queue[4] = nil
S.research_queue[3] = nil
S.research_queue[2] = S.technologies["logistics-2"]

-- TechnologyID is a union, so a queue of plain names has to work too.
S.research_queue[2] = "logistics-2"
local as_names = call("research_queue", { force = "player" })
check("research_queue reads a queue of plain names",
      as_names.ok and as_names.r.queue[2].tech == "logistics-2" and as_names.r.queue[2].units == 200,
      F.encode(as_names))
S.research_queue[2] = S.technologies["logistics-2"]

-- ── tech_status ───────────────────────────────────────────────────────
local blocked = call("tech_status", { force = "player", tech = "logistics-2" })
check("tech_status ok", blocked.ok and blocked.r.found == true, F.encode(blocked))
check("tech_status counts missing prerequisites", blocked.ok and blocked.r.prerequisites_missing == 1,
      blocked.ok and F.encode(blocked.r))
check("a technology with a missing prerequisite is not available", blocked.ok and blocked.r.available == false)
check("tech_status sorts prerequisites by name", blocked.ok and blocked.r.prerequisites[1].name == "automation")
check("tech_status carries level and units", blocked.ok and blocked.r.level == 1 and blocked.r.units == 200)

local ready = call("tech_status", { force = "player", tech = "logistics" })
check("a technology with every prerequisite met is available", ready.ok and ready.r.available == true,
      F.encode(ready))
check("the running technology's progress comes from the force", ready.ok and ready.r.progress == 0.25)

local no_tech = call("tech_status", { force = "player", tech = "wizardry" })
check("an unknown technology is a found=false result, not an error",
      no_tech.ok and no_tech.r.found == false and no_tech.r.reason:find("no technology"), F.encode(no_tech))
local no_name = call("tech_status", { force = "player" })
check("a missing tech argument is a provider_error", (not no_name.ok) and no_name.e == "provider_error",
      F.encode(no_name))

-- ── logistics_summary ─────────────────────────────────────────────────
S.contents_reads = 0
local logi = call("logistics_summary", { force = "player", surface = "nauvis" })
check("logistics_summary ok", logi.ok and logi.r.found == true, F.encode(logi))
check("logistics_summary reports both networks", logi.ok and logi.r.total == 2 and logi.r.shown == 2)
check("the busiest network comes first", logi.ok and logi.r.networks[1].id == 1, F.encode(logi.r))
check("logistics_summary carries robot counts",
      logi.ok and logi.r.networks[1].logistic_robots == 120
      and logi.r.networks[1].logistic_robots_available == 30
      and logi.r.networks[1].construction_robots == 40)
check("logistics_summary counts cells", logi.ok and logi.r.networks[1].cells == 3)
local rows = logi.ok and logi.r.networks[1].contents or {}
check("contents stop at eight rows", logi.ok and logi.r.networks[1].contents_shown == 8,
      logi.ok and logi.r.networks[1].contents_shown)
check("contents count distinct item names, not name and quality pairs",
      logi.ok and logi.r.networks[1].distinct_items == 12, logi.ok and logi.r.networks[1].distinct_items)
check("one item held at two qualities is one row, summed",
      rows[1] and rows[1].name == "iron-plate" and rows[1].count == 10500, F.encode(rows[1] or {}))
check("a row held at more than one quality carries the breakdown",
      rows[1] and rows[1].qualities and rows[1].qualities.normal == 9000
      and rows[1].qualities.uncommon == 1500, F.encode((rows[1] or {}).qualities or {}))
check("summing the breakdown gives the row's count", (function()
  local total = 0
  for _, count in pairs((rows[1] or {}).qualities or {}) do total = total + count end
  return total == (rows[1] or {}).count
end)())
check("normal quality alone says nothing about quality",
      rows[2] and rows[2].name == "copper-plate" and rows[2].quality == nil
      and rows[2].qualities == nil, F.encode(rows[2] or {}))
check("a single quality worth naming is still named",
      rows[3] and rows[3].name == "steel-plate" and rows[3].quality == "uncommon"
      and rows[3].qualities == nil, F.encode(rows[3] or {}))
check("aggregated rows stay sorted by count", (function()
  for i = 2, #rows do if rows[i - 1].count < rows[i].count then return false end end
  return true
end)(), F.encode(rows))

S.contents_reads = 0
local one_network = call("logistics_summary", { force = "player", surface = "nauvis", limit = 1 })
check("logistics_summary cuts to limit and keeps the total",
      one_network.ok and one_network.r.shown == 1 and one_network.r.total == 2, F.encode(one_network))
check("contents are only read for the networks that come back", S.contents_reads == 1, S.contents_reads)

local capped_networks = call("logistics_summary", { force = "player", surface = "nauvis", limit = 99 })
check("logistics_summary never returns more than five networks",
      capped_networks.ok and capped_networks.r.shown <= 5, F.encode(capped_networks.r))

local empty = call("logistics_summary", { force = "player", surface = "platform-1" })
check("a surface with no networks answers an empty list",
      empty.ok and empty.r.found == true and empty.r.total == 0 and #empty.r.networks == 0, F.encode(empty))

-- ── entity_count ──────────────────────────────────────────────────────
local labs = call("entity_count", { force = "player", surface = "nauvis", name = "lab" })
check("entity_count ok", labs.ok and labs.r.found == true and labs.r.count == 12, F.encode(labs))
check("entity_count passes the force name to the engine filter",
      S.last_entity_filter and S.last_entity_filter.force == "player", F.encode(S.last_entity_filter or {}))
local no_proto = call("entity_count", { force = "player", surface = "nauvis", name = "nonesuch" })
check("an unknown prototype is a found=false result, not an error",
      no_proto.ok and no_proto.r.found == false and no_proto.r.reason:find("no entity prototype"),
      F.encode(no_proto))
local no_entity_name = call("entity_count", { force = "player", surface = "nauvis" })
check("a missing name argument is a provider_error",
      (not no_entity_name.ok) and no_entity_name.e == "provider_error", F.encode(no_entity_name))

-- ── evolution ─────────────────────────────────────────────────────────
local evo = call("evolution", { force = "player", surface = "nauvis" })
check("evolution ok", evo.ok and evo.r.found == true, F.encode(evo))
check("evolution reports the factor", evo.ok and evo.r.evolution_factor == 0.42)
check("evolution reports all three parts",
      evo.ok and evo.r.by_time == 0.2 and evo.r.by_pollution == 0.15 and evo.r.by_killing_spawners == 0.07,
      evo.ok and F.encode(evo.r))
check("evolution asks about the surface it was given", S.last_evolution_surface == "nauvis")

-- Four decimals is where an evolution factor stops meaning anything to a player,
-- and without the rounding each of these four doubles reaches the model as a
-- fifty-four character literal (second review-fix contract 7).
S.force.get_evolution_factor = function() return 1 / 3 end
local long_evo = call("evolution", { force = "player", surface = "nauvis" })
check("evolution rounds the factor to four decimals",
      long_evo.ok and long_evo.r.evolution_factor == 0.3333, long_evo.ok and long_evo.r.evolution_factor)
check("a rounded factor is short on the wire",
      #F.encode(long_evo.r.evolution_factor) <= 6, F.encode(long_evo.r.evolution_factor))

-- ── pollution ─────────────────────────────────────────────────────────
local poll = call("pollution", { force = "player", surface = "nauvis" })
check("pollution ok", poll.ok and poll.r.found == true, F.encode(poll))
check("pollution totals the surface", poll.ok and poll.r.total_pollution == 1234.5)
check("pollution names the pollutant", poll.ok and poll.r.pollutant == "pollution"
      and poll.r.pollution_enabled == true)
S.pollution.nauvis = 1234.56789
local long_pollution = call("pollution", { force = "player", surface = "nauvis" })
check("pollution rounds its total to two decimals",
      long_pollution.ok and long_pollution.r.total_pollution == 1234.57,
      long_pollution.ok and long_pollution.r.total_pollution)
S.pollution.nauvis = 1234.5

local clean = call("pollution", { force = "player", surface = "platform-1" })
check("a surface with no pollutant says so",
      clean.ok and clean.r.pollution_enabled == false and clean.r.pollutant == nil, F.encode(clean))

-- ── rockets ───────────────────────────────────────────────────────────
local rockets = call("rockets", { force = "player" })
check("rockets ok", rockets.ok and rockets.r.rockets_launched == 7, F.encode(rockets))
check("rockets ranks items by count", rockets.ok and rockets.r.items[1].name == "space-science-pack"
      and rockets.r.items[1].count == 1000, rockets.ok and F.encode(rockets.r.items))
check("rockets reports how many kinds went up", rockets.ok and rockets.r.distinct_items == 3
      and rockets.r.shown == 3)
local one_item = call("rockets", { force = "player", limit = 1 })
check("rockets cuts to limit and keeps the total",
      one_item.ok and one_item.r.shown == 1 and one_item.r.distinct_items == 3, F.encode(one_item))

-- ── game_time ─────────────────────────────────────────────────────────
S.ticks_played = 216000 * 3 + 108000 -- three and a half hours
local clock = call("game_time", { force = "player" })
check("game_time ok", clock.ok, F.encode(clock))
check("game_time turns ticks into hours", clock.ok and clock.r.hours == 3.5, clock.ok and clock.r.hours)
check("game_time reports the tick and the ticks played",
      clock.ok and clock.r.tick == S.tick and clock.r.ticks_played == S.ticks_played)
check("game_time counts connected players",
      clock.ok and clock.r.connected_players == 1 and clock.r.force_connected_players == 1)

-- ── every tool refuses an unknown force, and none of them writes storage ──
for _, name in ipairs(NEW) do
  local bad = call(name, { force = "no-such-force", surface = "nauvis", tech = "logistics", name = "lab" })
  check(name .. " refuses an unknown force", (not bad.ok) and bad.e == "provider_error", F.encode(bad))
end

local missing_surface = { logistics_summary = true, entity_count = true, evolution = true, pollution = true }
for name in pairs(missing_surface) do
  local bad = call(name, { force = "player", surface = "atlantis", name = "lab" })
  check(name .. " answers found=false for an unknown surface",
        bad.ok and bad.r.found == false and bad.r.reason:find("list_surfaces"), F.encode(bad))
  local absent = call(name, { force = "player", name = "lab" })
  check(name .. " answers found=false when surface is missing",
        absent.ok and absent.r.found == false, F.encode(absent))
end

-- ── a surface index is as good as a surface name ──────────────────────
-- list_surfaces publishes an index on every row and game.surfaces accepts one,
-- so passing back what the model was just shown has to work (second review-fix
-- contract 8).
for name in pairs(missing_surface) do
  local by_index = call(name, { force = "player", surface = 1, tech = "logistics", name = "lab" })
  check(name .. " takes a surface index", by_index.ok and by_index.r.found == true
        and by_index.r.surface == "nauvis", F.encode(by_index))
  local as_text = call(name, { force = "player", surface = "1", name = "lab" })
  check(name .. " takes an index sent as a string", as_text.ok and as_text.r.found == true,
        F.encode(as_text))
  local absent = call(name, { force = "player", name = "lab" })
  check(name .. " reserves \"required\" for a missing argument",
        absent.ok and absent.r.reason:find("surface is required") ~= nil, F.encode(absent))
end

-- ── a tool called with no argument table at all ───────────────────────
for _, name in ipairs(NEW) do
  local bare = rpc({ op = "call", i = "ai-agent-bridge-tools", f = name })
  check(name .. " with no argument table says force is required",
        (not bare.ok) and bare.e == "provider_error" and bare.m == "force is required", F.encode(bare))
end

local before = F.encode(storage)
for _, name in ipairs(NEW) do
  call(name, { force = "player", surface = "nauvis", tech = "logistics", name = "lab" })
end
check("no breadth tool writes storage", F.encode(storage) == before)

-- ── every reply fits the rpc byte cap with room to spare ──────────────
for _, name in ipairs(NEW) do
  S.rcon_replies = {}
  rpc({ op = "call", i = "ai-agent-bridge-tools", f = name,
        a = { force = "player", surface = "nauvis", tech = "logistics-2", name = "lab" } })
  local bytes = #S.rcon_replies[#S.rcon_replies]
  check(name .. " reply is small (" .. bytes .. " bytes)", bytes < 4000, bytes)
end

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
