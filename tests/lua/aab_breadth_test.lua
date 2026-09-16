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
-- 7000 was a guess made when this op did not exist, and the manifest outgrew it
-- honestly as tools were added. The cap that governs is rpc.lua's own
-- CAPS.manifest = 32768, the op the service actually builds its catalog from,
-- so assert at half of it: still a real guard against a runaway manifest, and no
-- longer a reason to trim descriptions the model needs.
check("the companion's own manifest fits one reply with room to spare",
      #F.encode(mine.r) < 16384, #F.encode(mine.r))
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
check("the head's progress comes from the force", queue.ok and queue.r.queue[1].progress == "0.25",
      queue.ok and queue.r.queue[1].progress)
check("a queued entry keeps its saved progress", queue.ok and queue.r.queue[2].progress == "0.5",
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
      levels.ok and levels.r.queue[2].progress == "0.2" and levels.r.queue[3].progress == nil,
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
check("the running technology's progress comes from the force", ready.ok and ready.r.progress == "0.25")

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
check("evolution reports the factor", evo.ok and evo.r.evolution_factor == "0.42")
check("evolution reports all three parts",
      evo.ok and evo.r.by_time == "0.2" and evo.r.by_pollution == "0.15" and evo.r.by_killing_spawners == "0.07",
      evo.ok and F.encode(evo.r))
check("evolution asks about the surface it was given", S.last_evolution_surface == "nauvis")

-- Four decimals is where an evolution factor stops meaning anything to a player,
-- and without the rounding each of these four doubles reaches the model as a
-- fifty-four character literal (second review-fix contract 7).
S.force.get_evolution_factor = function() return 1 / 3 end
local long_evo = call("evolution", { force = "player", surface = "nauvis" })
check("evolution rounds the factor to four decimals",
      long_evo.ok and long_evo.r.evolution_factor == "0.3333", long_evo.ok and long_evo.r.evolution_factor)
check("a rounded factor is short on the wire",
      #F.encode(long_evo.r.evolution_factor) <= 8, F.encode(long_evo.r.evolution_factor))

-- ── pollution ─────────────────────────────────────────────────────────
local poll = call("pollution", { force = "player", surface = "nauvis" })
check("pollution ok", poll.ok and poll.r.found == true, F.encode(poll))
check("pollution totals the surface", poll.ok and poll.r.total_pollution == "1234.5")
check("pollution names the pollutant", poll.ok and poll.r.pollutant == "pollution"
      and poll.r.pollution_enabled == true)
S.pollution.nauvis = 1234.56789
local long_pollution = call("pollution", { force = "player", surface = "nauvis" })
check("pollution rounds its total to two decimals",
      long_pollution.ok and long_pollution.r.total_pollution == "1234.57",
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

-- ── rockets all=true: the sweep the phase4 follow-up contract added ────
-- Load-bearing per that contract: no top-level `force` field (same reason as
-- list_players' sweep), and no `items` list in sweep mode, since fourteen
-- forces of item rows would blow the reply budget. Mutation-tested: adding
-- `force = "player"` to the row, or an `items` list, turns every check below
-- red.
local rockets_all = call("rockets", { all = true })
check("rockets all=true carries no top-level force field",
      rockets_all.ok and rockets_all.r.force == nil, F.encode(rockets_all))
check("rockets all=true returns one row per force with players or a launch",
      rockets_all.ok and rockets_all.r.total == 1 and rockets_all.r.shown == 1
      and rockets_all.r.forces[1].force == "player"
      and rockets_all.r.forces[1].rockets_launched == 7
      and rockets_all.r.forces[1].distinct_items == 3, F.encode(rockets_all))
check("rockets all=true carries no items list, single-force answer only",
      rockets_all.ok and rockets_all.r.forces[1].items == nil, F.encode(rockets_all))

-- Largest first, not alphabetical. bounded.cut keeps the FIRST rows, so a sweep
-- ordered by name discards the biggest launcher before the smallest, on the one
-- tool whose whole purpose is naming the biggest. team-3 sorts before player by
-- name and must sort after it by launches.
S.team3.rockets_launched = 99
local rockets_order = call("rockets", { all = true })
check("rockets all=true ranks by launches, not by force name",
      rockets_order.ok and rockets_order.r.forces[1].force == "team-3"
      and rockets_order.r.forces[1].rockets_launched == 99
      and rockets_order.r.forces[2].force == "player", F.encode(rockets_order))
S.team3.rockets_launched = 0

-- ── entity_count all=true ─────────────────────────────────────────────
-- Question 64 on 2026-09-15 asked how many solar panels each team had and
-- spent fourteen entity_count calls in one round, one per force. The sweep
-- carries the same engine work in one trip. As with the other two sweeps, the
-- absence of a top-level force field is what tells it from the single-force
-- shape, and both carry rows, so a mutation adding one back turns this red.
local count_all = call("entity_count", { all = true, name = "lab" })
check("entity_count all=true carries no top-level force field",
      count_all.ok and count_all.r.force == nil, F.encode(count_all))
check("entity_count all=true answers for every force with players, largest first",
      count_all.ok and count_all.r.found == true and count_all.r.total == 1
      and count_all.r.shown == 1 and count_all.r.forces[1].force == "player"
      and count_all.r.forces[1].count == 12, F.encode(count_all))

-- The sum across surfaces, with a real number on each so the assertion cannot
-- pass on a single term plus zeroes: 12 on nauvis and 5 on orbit is 17.
S.entity_counts_by_surface = { nauvis = { lab = 12 }, ["platform-1"] = { lab = 5 } }
local count_sum = call("entity_count", { all = true, name = "lab" })
check("entity_count all=true with no surface covers every surface the force has",
      count_sum.ok and count_sum.r.forces[1].count == 17, F.encode(count_sum))
S.entity_counts_by_surface = {}
-- No surface named goes through LuaForce::get_entity_count, the engine's own
-- O(1) per-force counter, which asks no surface anything. Reporting a surface
-- count there would be inventing one, so its absence is part of the contract
-- rather than an oversight.
check("entity_count all=true with no surface reports no surface count",
      count_all.ok and count_all.r.surface == "all"
      and count_all.r.surfaces_counted == nil, F.encode(count_all))

local count_one = call("entity_count", { all = true, name = "lab", surface = "nauvis" })
check("entity_count all=true on one named surface reports that surface",
      count_one.ok and count_one.r.found == true and count_one.r.surface == "nauvis"
      and count_one.r.surfaces_counted == 1, F.encode(count_one))

local count_bad = call("entity_count", { all = true, name = "not-a-real-prototype" })
check("entity_count all=true on an unknown prototype says found=false, not zero",
      count_bad.ok and count_bad.r.found == false and count_bad.r.forces == nil,
      F.encode(count_bad))

-- The single-force shape is unchanged, byte for byte, and still carries the
-- force field the sweep must not.
local count_single = call("entity_count", { force = "player", surface = "nauvis", name = "lab" })
check("entity_count without all keeps its single-force shape",
      count_single.ok and count_single.r.force == "player"
      and count_single.r.surface == "nauvis" and count_single.r.count == 12,
      F.encode(count_single))

-- ── game_time ─────────────────────────────────────────────────────────
S.ticks_played = 216000 * 3 + 108000 -- three and a half hours
local clock = call("game_time", { force = "player" })
check("game_time ok", clock.ok, F.encode(clock))
check("game_time turns ticks into hours", clock.ok and clock.r.hours == "3.5", clock.ok and clock.r.hours)
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

-- ── sweep: the tier-3 metric registry, its walk and its envelope ──────
-- Phase 5 Unit B (docs/design/phase5-sweep.md, the phase5 implementation
-- contract). Kept in its own section, not folded into the NEW-tools loops
-- above: sweep answers across every force by design, so "refuses an unknown
-- force" and the other loops built for a single-force tool do not apply to
-- it, and forcing it into them would test the wrong contract.

local sweep_registry = require("scripts.sweep.registry")
local sweep_platform = require("scripts.sweep.platform")

local function row_eq(row, name, value)
  return type(row) == "table" and row[1] == name and row[2] == value
end

-- The manifest entry: present, generated, and clean of the reserved `force`
-- param the way every other sweep-shaped tool already is.
local sweep_entry = manifest and manifest.sweep
check("sweep is in the manifest with a real description",
      sweep_entry ~= nil and type(sweep_entry.desc) == "string" and #sweep_entry.desc > 60,
      sweep_entry)
-- Proves the description is actually BUILT from the registry rather than
-- typed out by hand and merely kept in sync by discipline: tool.lua calls
-- registry.metric_line() to build `desc`, so that exact string has to appear
-- inside it verbatim. Mutation-tested: hardcoding the four metric names as a
-- literal string in tool.lua instead of calling registry.metric_line()
-- turns this red (the generated line's own "(axes)" formatting no longer
-- appears anywhere in desc).
check("the manifest description is generated from the registry, not hand-copied",
      sweep_entry and sweep_entry.desc:find(sweep_registry.metric_line(), 1, true) ~= nil,
      sweep_entry and sweep_entry.desc)
check("sweep.metric is required", sweep_entry and sweep_entry.params.metric:match("^string!") ~= nil,
      sweep_entry and sweep_entry.params.metric)
for param, spec in pairs((sweep_entry or {}).params or {}) do
  check("sweep." .. param .. " parses", spec:match("^%a+!? .") ~= nil, spec)
  check("sweep does not declare force", param ~= "force", param)
end

-- The registry itself: the four metrics the phase 5 contract named are
-- always there, and every card names a metric the registry can serve. The
-- count is not pinned: Stage 5 grew it from four to fourteen, and a literal
-- here would have to move every time a metric lands.
local cards = sweep_registry.cards()
check("the registry carries at least the four contracted metrics", #cards >= 4, #cards)
for _, card in ipairs(cards) do
  check("card " .. card.metric .. " names a metric the registry serves",
        sweep_registry.get(card.metric) ~= nil, card.metric)
end
local by_name = {}
for _, card in ipairs(cards) do by_name[card.metric] = card end
for _, name in ipairs({ "entities", "rockets", "research", "players" }) do
  check("the registry carries " .. name, by_name[name] ~= nil)
end
-- Which metrics take a subject is part of what the generated description
-- tells the model, so it is pinned by name on both sides of the line.
for _, name in ipairs({ "entities", "item_made", "item_rate", "fluid_rate" }) do
  check(name .. " declares a subject", by_name[name] ~= nil and by_name[name].subject ~= nil, name)
end
for _, name in ipairs({ "rockets", "research", "players", "kills", "built", "trains" }) do
  check(name .. " declares no subject", by_name[name] ~= nil and by_name[name].subject == nil, name)
end

-- ── unrecognised metric or axis: found=false with every card, before any
-- pass runs, so a wrong guess recovers in one round (the phase5 contract's
-- envelope section) ────────────────────────────────────────────────────
local no_metric = call("sweep", {})
check("sweep with no metric is a refusal, not a Lua error", no_metric.ok, F.encode(no_metric))
check("sweep with no metric answers found=false and carries sweep_v",
      no_metric.r.sweep_v == 1 and no_metric.r.found == false, F.encode(no_metric))
check("the found=false reply carries every metric's own card",
      no_metric.r.metrics ~= nil and #no_metric.r.metrics == #cards, F.encode(no_metric))

local bad_metric = call("sweep", { metric = "standings" })
check("an unknown metric name answers found=false the same way",
      bad_metric.ok and bad_metric.r.found == false and bad_metric.r.metrics ~= nil
      and bad_metric.r.reason:find("standings") ~= nil, F.encode(bad_metric))

local bad_axis = call("sweep", { metric = "rockets", axis = "surface" })
check("an axis a metric does not sweep by is refused, not silently coerced to the default",
      bad_axis.ok and bad_axis.r.found == false and bad_axis.r.reason:find("force") ~= nil,
      F.encode(bad_axis))
local nonsense_axis = call("sweep", { metric = "entities", subject = "lab", axis = "teleport" })
check("a nonsense axis is refused the same way a real-but-unsupported one is",
      nonsense_axis.ok and nonsense_axis.r.found == false, F.encode(nonsense_axis))

-- ── entities: delegates every count to entity_count, never counts itself ──
local no_subject = call("sweep", { metric = "entities" })
check("entities without a subject is refused before any pass runs",
      no_subject.ok and no_subject.r.found == false and no_subject.r.reason:find("subject") ~= nil,
      F.encode(no_subject))
local bad_subject = call("sweep", { metric = "entities", subject = "not-a-real-prototype" })
check("entities with an unknown prototype is found=false, the same reason entity_count gives",
      bad_subject.ok and bad_subject.r.found == false
      and bad_subject.r.reason:find("no entity prototype") ~= nil, F.encode(bad_subject))
local unsupported_axis = call("sweep", { metric = "entities", subject = "lab", axis = "player" })
check("entities does not sweep by player, and says which axes it does",
      unsupported_axis.ok and unsupported_axis.r.found == false
      and unsupported_axis.r.reason:find("force, surface, platform, force%+surface") ~= nil,
      F.encode(unsupported_axis))

local entities_force = call("sweep", { metric = "entities", subject = "lab" })
check("entities axis=force ok", entities_force.ok, F.encode(entities_force))
check("entities defaults to the force axis, one row, via entity_count's own O(1) counter",
      entities_force.r.sweep_v == 1 and entities_force.r.axis == "force"
      and entities_force.r.metric == "entities" and entities_force.r.subject == "lab"
      and entities_force.r.unit == "count" and entities_force.r.found == nil,
      F.encode(entities_force))
check("entities force axis names cols name/value and one row for player, 12 labs",
      entities_force.r.cols[1] == "name" and entities_force.r.cols[2] == "value"
      and row_eq(entities_force.r.rows[1], "player", 12)
      and entities_force.r.total == 1 and entities_force.r.shown == 1
      and entities_force.r.skipped == 0 and entities_force.r.why == nil,
      F.encode(entities_force))

local entities_compound = call("sweep", { metric = "entities", subject = "lab", axis = "force+surface" })
check("the force+surface axis names each row \"<force> on <surface>\"",
      entities_compound.ok and row_eq(entities_compound.r.rows[1], "player on nauvis", 12),
      F.encode(entities_compound))

-- The platform axis against today's fake, which has no surface carrying a
-- real LuaSpacePlatform yet: a valid, empty answer, not an error. This stays
-- true whether or not a future fixture adds a live platform, so it is
-- intentionally a shape check, not a row count.
local entities_platform = call("sweep", { metric = "entities", subject = "lab", axis = "platform" })
check("the platform axis answers cleanly even with zero live platforms",
      entities_platform.ok and entities_platform.r.found == nil
      and entities_platform.r.axis == "platform" and type(entities_platform.r.rows) == "table"
      and entities_platform.r.total == #entities_platform.r.rows, F.encode(entities_platform))

-- A platform row says who owns the ship and where it is, so "which ship has
-- the most, where is it" is one row rather than a name and a guess. The
-- fixture's platform-1 belongs to player and is stopped at fulgora.
S.entity_counts_by_surface = { ["platform-1"] = { lab = 4 } }
local ship_rows = call("sweep", { metric = "entities", subject = "lab", axis = "platform" })
check("a platform row carries its owner and where it is stopped",
      ship_rows.ok and ship_rows.r.rows and ship_rows.r.rows[1]
      and ship_rows.r.rows[1][1] == "platform-1 (player, at fulgora)" and ship_rows.r.rows[1][2] == 4,
      F.encode(ship_rows))
S.entity_counts_by_surface = {}

-- The surface axis has to re-sort by value: ctx.surfaces is walked in NAME
-- order (nauvis, then platform-1), so if envelope.lua's own sort were
-- dropped or applied to the wrong field, this would come back nauvis-first
-- despite platform-1 holding the larger count. Mutation-tested: removing
-- envelope.lua's table.sort call turns this row order red while every count
-- stays right.
S.entity_counts_by_surface = { ["platform-1"] = { lab = 50 } }
local entities_surface = call("sweep", { metric = "entities", subject = "lab", axis = "surface" })
check("the surface axis sorts by value, not by the order surfaces were walked",
      entities_surface.ok and row_eq(entities_surface.r.rows[1], "platform-1", 50)
      and row_eq(entities_surface.r.rows[2], "nauvis", 12) and entities_surface.r.total == 2,
      F.encode(entities_surface))
S.entity_counts_by_surface = {}

-- ── rockets and research: force axis only, straight delegation ────────
local rockets_sweep = call("sweep", { metric = "rockets" })
check("rockets sweep delegates to rockets{all=true}",
      rockets_sweep.ok and rockets_sweep.r.unit == "count" and rockets_sweep.r.subject == nil
      and row_eq(rockets_sweep.r.rows[1], "player", 7) and rockets_sweep.r.total == 1,
      F.encode(rockets_sweep))

local research_sweep = call("sweep", { metric = "research" })
check("research sweep delegates to current_research{all=true}",
      research_sweep.ok and research_sweep.r.unit == "percent"
      and row_eq(research_sweep.r.rows[1], "player", 25), F.encode(research_sweep))

-- The regression this metric's own file documents: current_research's row
-- already rounds progress for display, a bare number 0 for an idle force
-- beside a rounded STRING for a running one. Comparing those two in Lua
-- throws outright, so this is not just a wrong-order risk, it is a crash
-- risk. Mutation-tested against scripts/sweep/metrics/research.lua: reading
-- the delegate's own already-rounded `progress` field instead of
-- LuaForce::research_progress directly turns this into a provider_error,
-- "attempt to compare string with number".
S.team3.players = { { name = "Zed", valid = true, connected = true } }
local research_mixed = call("sweep", { metric = "research" })
check("research sweeps an idle force beside a running one without erroring",
      research_mixed.ok and research_mixed.r.total == 2
      and row_eq(research_mixed.r.rows[1], "player", 25)
      and row_eq(research_mixed.r.rows[2], "team-3", 0), F.encode(research_mixed))
S.team3.players = {}

-- ── players: the one metric with a real player axis ───────────────────
local players_force = call("sweep", { metric = "players" })
check("players axis=force counts heads per force",
      players_force.ok and row_eq(players_force.r.rows[1], "player", 1), F.encode(players_force))
local players_player = call("sweep", { metric = "players", axis = "player" })
check("players axis=player lists one row per connected player",
      players_player.ok and row_eq(players_player.r.rows[1], "Bob", 1)
      and players_player.r.total == 1, F.encode(players_player))

-- ── limit, skipped and why: bounded the same way every other sweep is ──
-- Three forces with players, none from the baseline fixture, so the row
-- count and the leader are unambiguous. Cleaned up immediately after.
game.forces["sweep-synth-1"] = { name = "sweep-synth-1", players = { {} }, rockets_launched = 100, items_launched = {} }
game.forces["sweep-synth-2"] = { name = "sweep-synth-2", players = { {} }, rockets_launched = 50, items_launched = {} }
local limited = call("sweep", { metric = "rockets", limit = 2 })
check("a limit cuts the tail, keeps the leaders, and says why",
      limited.ok and limited.r.total == 3 and limited.r.shown == 2 and limited.r.skipped == 1
      and limited.r.why == "limit" and row_eq(limited.r.rows[1], "sweep-synth-1", 100)
      and row_eq(limited.r.rows[2], "sweep-synth-2", 50), F.encode(limited))
game.forces["sweep-synth-1"] = nil
game.forces["sweep-synth-2"] = nil
local cleaned_up = call("sweep", { metric = "rockets" })
check("removing the synthetic forces leaves the sweep back at baseline",
      cleaned_up.ok and cleaned_up.r.total == 1, F.encode(cleaned_up))

-- ── no sweep call writes storage, and every reply stays well under the
-- rpc byte cap (CAPS.call = 8000, scripts/rpc.lua) ─────────────────────
local sweep_before = F.encode(storage)
call("sweep", { metric = "entities", subject = "lab", axis = "force+surface" })
call("sweep", { metric = "players", axis = "player" })
check("sweep writes no storage", F.encode(storage) == sweep_before)

S.rcon_replies = {}
rpc({ op = "call", i = "ai-agent-bridge-tools", f = "sweep", a = { metric = "entities", subject = "lab" } })
local sweep_bytes = #S.rcon_replies[#S.rcon_replies]
check("a sweep reply is small (" .. sweep_bytes .. " bytes)", sweep_bytes < 4000, sweep_bytes)

-- ── the platform landmine, isolated from any fake game at all ─────────
-- LuaSpacePlatform::scheduled_for_deletion is a tick count, not a boolean
-- (verified against ~/factorio/doc-html/runtime-api.json: "Returns how many
-- ticks are left before the platform will be deleted. 0 if not scheduled for
-- deletion."). scripts/sweep/platform.lua is the one place this is read, and
-- it takes a plain table rather than a real LuaSurface, so the landmine is
-- covered here directly rather than waiting on a fixture elsewhere to carry
-- one. Mutation-tested: swapping the `(scheduled_for_deletion or 0) ~= 0`
-- check for the naive `if platform.scheduled_for_deletion then` turns the
-- first of these four red, since 0 is truthy in Lua.
check("a live platform (scheduled_for_deletion = 0) contributes its name",
      sweep_platform.name_of({ platform = { name = "orbit-1", scheduled_for_deletion = 0 } }) == "orbit-1")
check("a platform mid-countdown to deletion (non-zero ticks left) is excluded",
      sweep_platform.name_of({ platform = { name = "orbit-1", scheduled_for_deletion = 1800 } }) == nil)
check("a platform whose field is simply absent still contributes",
      sweep_platform.name_of({ platform = { name = "orbit-1" } }) == "orbit-1")
check("a plain surface with no platform at all contributes nothing",
      sweep_platform.name_of({}) == nil)

-- ── bounded.fit: rows bounded by bytes, not by count ──────────────────
-- entity_count per_surface and list_surfaces both grew rows that carry a
-- platform, its owner, its location and its state. A row cap was the right
-- bound while a row was {force, count} and the wrong one the moment rows got
-- wide: 100 of them encode past rpc.lua's CAPS.call = 8000, and an over-cap
-- reply is refused whole, so the tool spends every pass and then answers
-- nothing the model can use. Measured before this bound: entity_count
-- per_surface died at 32 surfaces, list_surfaces reached 7794 bytes at its own
-- default row count.
-- ── sweep{metric="players"} over a roster the delegate truncated ──────
-- list_players{all=true} sorts by PLAYER NAME and cuts at its own row cap
-- before returning, so past that cap its rows are an alphabetical slice. A
-- sweep that counts the slice and lets the ranker publish a leader names the
-- wrong force whenever the cut bites, because the forces whose players sort
-- late are simply not in the sample. Refusing whole is the rule the briefing's
-- pl key already follows.
local saved_players = game.forces.player.players
local many = {}
for i = 1, 51 do
  many[i] = { name = string.format("p%03d", i), connected = true, admin = false, valid = true }
end
game.forces.player.players = many
game.forces.player.connected_players = many

local truncated = call("sweep", { metric = "players" })
check("sweep players refuses rather than ranking a truncated roster",
      truncated.ok and truncated.r.found == false, F.encode(truncated))
check("the refusal says how much of the roster it saw",
      truncated.ok and truncated.r.shown == 50 and truncated.r.total == 51, F.encode(truncated))
check("a refused sweep carries no rows at all",
      truncated.ok and truncated.r.rows == nil, F.encode(truncated))

game.forces.player.players = saved_players
game.forces.player.connected_players = { saved_players[1] }
local whole = call("sweep", { metric = "players" })
check("sweep players answers normally once the roster fits",
      whole.ok and whole.r.found ~= false and whole.r.rows ~= nil, F.encode(whole))

-- ── sweep entities delegates, and refuses a cut it cannot see past ────
-- entities.lua looks its delegate up at call time, so a stub can stand in
-- for entity_count and hand back the one shape the fixture can never produce:
-- a per_surface sweep the tool had to cut. Two forces on two surfaces is four
-- rows at most; the tool cuts at a hundred and at a byte budget. A ranking
-- over the survivors of a cut names whoever survived, so it is refused whole.
local entity_count_tool = require("scripts.tools.entity_count")
local real_entity_count = entity_count_tool.functions.entity_count
entity_count_tool.functions.entity_count = function(a)
  if a.all == true and a.per_surface == true then
    return { found = true, name = a.name, surface = "all", total = 80, shown = 50,
             forces = { { force = "team-1", surface = "platform-1", count = 9, platform = "platform-1" } } }
  end
  return real_entity_count(a)
end
local cut_sweep = call("sweep", { metric = "entities", subject = "lab", axis = "platform" })
check("sweep entities refuses when its delegate cut the per-surface rows",
      cut_sweep.ok and cut_sweep.r.found == false and cut_sweep.r.rows == nil, F.encode(cut_sweep))
check("the refusal carries the delegate's own shown and total",
      cut_sweep.ok and cut_sweep.r.shown == 50 and cut_sweep.r.total == 80, F.encode(cut_sweep))
entity_count_tool.functions.entity_count = real_entity_count
local whole_sweep = call("sweep", { metric = "entities", subject = "lab", axis = "platform" })
check("sweep entities answers again once the delegate shows everything",
      whole_sweep.ok and whole_sweep.r.found ~= false and whole_sweep.r.rows ~= nil, F.encode(whole_sweep))

-- ── a platform counting down to deletion carries no platform columns ──
-- scheduled_for_deletion is how many ticks are left, not a boolean, and 0 is
-- truthy in Lua. platform_lookup's exclusion branch is the one that fires on
-- a non-zero count; the fixture ships 0, so this is the only coverage the
-- branch has on the list_surfaces path.
local orbit = game.surfaces["platform-1"]
orbit.platform.scheduled_for_deletion = 600
local doomed = call("list_surfaces", { force = "player", limit = 50 })
local doomed_row
for _, row in ipairs(doomed.ok and doomed.r.surfaces or {}) do
  if row.name == "platform-1" then doomed_row = row end
end
check("list_surfaces still lists a platform counting down to deletion as a surface",
      doomed_row ~= nil, F.encode(doomed))
check("but carries none of the platform columns for it",
      doomed_row ~= nil and doomed_row.platform == nil and doomed_row.owner == nil
      and doomed_row.location == nil and doomed_row.state == nil, F.encode(doomed_row))
orbit.platform.scheduled_for_deletion = 0
local live_again = call("list_surfaces", { force = "player", limit = 50 })
local live_row
for _, row in ipairs(live_again.ok and live_again.r.surfaces or {}) do
  if row.name == "platform-1" then live_row = row end
end
check("and carries them again once the countdown is cleared",
      live_row ~= nil and live_row.platform == "platform-1" and live_row.owner ~= nil, F.encode(live_row))

-- ── a metric another mod declares, discovered without naming the mod ──
-- The companion knows nothing about "aab-fake-teams". It exposes a tool with a
-- sweep block in its own agent_tools_v1 manifest, and the sweep finds it
-- through the same probe every provider is already read with. This is what
-- makes a multi-team mod of any kind sweepable, MTS or an OARC-like one,
-- without a companion release.
remote.add_interface("aab-fake-teams", {
  agent_tools_v1 = function()
    return { v = 1, tools = {
      standings = {
        desc = "Each team's score.",
        params = {},
        sweep = { axes = { "force" }, rows = "forces", name = "force", value = "score", unit = "points" },
      },
      plain = { desc = "A tool with no sweep block, so not a metric.", params = {} },
    } }
  end,
  standings = function(args)
    if args.cut then
      return { total = 3, shown = 2, forces = { { force = "team-a", score = 5 }, { force = "team-b", score = "12.5" } } }
    end
    return { total = 2, shown = 2, forces = { { force = "team-a", score = 5 }, { force = "team-b", score = "12.5" } } }
  end,
  plain = function() return {} end,
})

local foreign = call("sweep", { metric = "standings" })
check("sweep finds a metric another provider declared",
      foreign.ok and foreign.r.found ~= false and foreign.r.metric == "standings", F.encode(foreign))
-- 12.5 leaves the wire as the short decimal string bounded.round makes of
-- every fraction; the service's ranker reads it back as a number. It must not
-- have been rounded to 13 on the way.
check("its rows arrive largest first, a string value read as a number and kept whole",
      foreign.ok and foreign.r.rows and foreign.r.rows[1][1] == "team-b" and tonumber(foreign.r.rows[1][2]) == 12.5
      and foreign.r.rows[2][1] == "team-a", F.encode(foreign))
check("a provider tool with no sweep block is not a metric",
      call("sweep", { metric = "plain" }).r.found == false, "plain was swept")
local cards = call("sweep", { metric = "no-such-metric" })
local card_by = {}
for _, card in ipairs(cards.ok and cards.r.metrics or {}) do card_by[card.metric] = card end
check("the cards a wrong guess gets back include the provider's metric and name its provider",
      card_by.standings ~= nil and card_by.standings.provider == "aab-fake-teams", F.encode(cards.r.metrics))
check("and still include the companion's own",
      card_by.entities ~= nil and card_by.entities.provider == nil, F.encode(cards.r.metrics))

-- The delegate cut its rows: refused whole, never ranked over the survivors.
S.interfaces["aab-fake-teams"].standings = function() 
  return { total = 3, shown = 2, forces = { { force = "team-a", score = 5 }, { force = "team-b", score = 12 } } }
end
local cut_foreign = call("sweep", { metric = "standings" })
check("a provider metric whose delegate cut its rows is refused, not ranked",
      cut_foreign.ok and cut_foreign.r.found == false and cut_foreign.r.rows == nil
      and cut_foreign.r.shown == 2 and cut_foreign.r.total == 3, F.encode(cut_foreign))
S.interfaces["aab-fake-teams"] = nil

local bounded = require("scripts.tools.bounded")
local function wide_rows(n)
  local rows = {}
  for i = 1, n do
    rows[i] = { name = "Cargo Hauler Mark " .. i .. " Heavy", index = i, force_players = 0,
      platform = "Cargo Hauler Mark " .. i .. " Heavy", owner = "team-123",
      location = "solar-system-edge", state = "waiting_for_starter_pack" }
  end
  return rows
end

local narrow = { { force = "team-1", count = 5 }, { force = "team-2", count = 3 } }
check("bounded.fit leaves rows alone when they already fit",
      #bounded.fit(narrow) == 2, F.encode(narrow))

for _, n in ipairs({ 32, 50, 100 }) do
  local rows = wide_rows(n)
  local raw = #F.encode(rows)
  local fitted = bounded.fit(rows)
  local after = #F.encode(fitted)
  check("bounded.fit keeps " .. n .. " wide rows under the byte budget",
        after <= bounded.ROW_BUDGET, n .. " rows: raw " .. raw .. " -> kept " .. #fitted .. ", " .. after .. " bytes")
  check("bounded.fit keeps rows rather than emptying the reply at " .. n,
        #fitted > 0, "kept " .. #fitted)
  -- The rows that survive are the FIRST ones, so a caller that sorted by value
  -- keeps the leaders rather than whatever the cut happened to reach.
  check("bounded.fit keeps the first rows at " .. n,
        fitted[1].name == rows[1].name, F.encode(fitted[1]))
end

-- The case the byte bound exists for: rows wide enough that the count cap
-- alone would have shipped an over-cap reply.
local hundred = wide_rows(100)
check("100 wide rows would have blown the call cap without the byte bound",
      #F.encode(hundred) > 8000, #F.encode(hundred))


print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
