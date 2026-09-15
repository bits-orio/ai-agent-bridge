-- AI Agent Bridge - tests/lua/aab_stage5_metrics_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Stage 5 Unit B: the six sweep metrics that delegate to tools that already
-- exist (item_made, item_rate, pollution, evolution, robots, networks),
-- driven through the aab-rpc call op against the fake game exactly the way
-- aab_breadth_test.lua drives the phase5 registry's first four. Kept in its
-- own file per the stage5 contract rather than folded into
-- aab_breadth_test.lua, which this file does not edit.

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

--- Exact match: both fields of an envelope row equal exactly what was given.
local function row_eq(row, name, value)
  return type(row) == "table" and row[1] == name and row[2] == value
end
--- Loose match on the value: a metric's own places setting may leave a
--- fraction on the wire as a short decimal STRING (bounded.round), same as
--- every other sweep metric in this catalog, so this reads it back as a
--- number the way registry.lua's own foreign_metric and every metric file
--- in this stage does before comparing.
local function row_num_eq(row, name, value)
  return type(row) == "table" and row[1] == name and tonumber(row[2]) == value
end

-- ── the manifest: grows with every metric, still fits one reply ───────
-- docs/design/phase5-sweep.md: "The tool's manifest description must
-- GENERATE its metric vocabulary from the registry", so item_made,
-- item_rate, pollution, evolution, robots and networks show up here by
-- registration in registry.lua alone. Byte count printed for the stage5
-- contract's own "Report byte counts" instruction.
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
check("the companion still has a manifest", mine.ok and mine.r.tools ~= nil, F.encode(mine))
local manifest_bytes = #F.encode(mine.r)
print("STAGE5 sweep manifest op reply bytes: " .. manifest_bytes)
check("the manifest still fits one reply with room to spare (CAPS.manifest = 32768)",
      manifest_bytes < 16384, manifest_bytes)

local sweep_registry = require("scripts.sweep.registry")
local sweep_desc = mine.r.tools.sweep.desc
for _, name in ipairs({ "item_made", "item_rate", "pollution", "evolution", "robots", "networks" }) do
  check("sweep's generated desc names " .. name, sweep_desc:find(name, 1, true) ~= nil, sweep_desc)
end

local cards = sweep_registry.cards()
local by_name = {}
for _, card in ipairs(cards) do by_name[card.metric] = card end
for _, name in ipairs({ "item_made", "item_rate", "pollution", "evolution", "robots", "networks" }) do
  check("the registry carries " .. name, by_name[name] ~= nil)
end
check("item_made and item_rate declare a subject, the rest of this stage do not",
      by_name.item_made.subject ~= nil and by_name.item_rate.subject ~= nil
      and by_name.pollution.subject == nil and by_name.evolution.subject == nil
      and by_name.robots.subject == nil and by_name.networks.subject == nil)
check("item_made and item_rate say what kind of subject", by_name.item_made.subject_kind == "item"
      and by_name.item_rate.subject_kind == "item")
check("pollution's card says it walks the map", by_name.pollution.costly == true)

-- ── item_made: delegates to production_since with since_tick = 0 ──────
-- Nauvis-only production in the fixture (fakegame.lua's lifetime_count only
-- answers off S.last_stats_surface == "nauvis"), 1000 iron-plate at normal
-- quality and 0.5x that again at uncommon (S.quality_share): 1500 exact,
-- summed once per quality, never sampled since since_tick = 0 is the exact
-- lifetime path.
local no_subject_made = call("sweep", { metric = "item_made" })
check("item_made without a subject is refused before any pass runs",
      no_subject_made.ok and no_subject_made.r.found == false
      and no_subject_made.r.reason:find("subject") ~= nil, F.encode(no_subject_made))

local made = call("sweep", { metric = "item_made", subject = "iron-plate" })
check("item_made ok, one row, the exact lifetime sum over every quality",
      made.ok and made.r.axis == "force" and made.r.unit == "count"
      and row_eq(made.r.rows[1], "player", 1500) and made.r.total == 1, F.encode(made))

local made_bad_axis = call("sweep", { metric = "item_made", subject = "iron-plate", axis = "surface" })
check("item_made only sweeps by force, and says so rather than silently defaulting",
      made_bad_axis.ok and made_bad_axis.r.found == false
      and made_bad_axis.r.reason:find("force") ~= nil, F.encode(made_bad_axis))

-- Refusal: production_since_tool.functions.production_since is looked up at
-- call time (item_made.lua's own comment explains why, the same reason
-- entities.lua gives), so a stub standing in for it is the seam a test
-- reaches through without editing item_made.lua. production_since never
-- actually sends total/shown on a real save (its own reply carries
-- surfaces_counted instead), so this proves the forward-defensive guard
-- itself works rather than something the live tool exercises today.
local production_since_tool = require("scripts.tools.production_since")
local real_production_since = production_since_tool.functions.production_since
production_since_tool.functions.production_since = function(_a)
  return { found = true, produced = "999", total = 5, shown = 3 }
end
local made_cut = call("sweep", { metric = "item_made", subject = "iron-plate" })
check("item_made refuses whole when its delegate reports a cut",
      made_cut.ok and made_cut.r.found == false and made_cut.r.rows == nil
      and made_cut.r.shown == 3 and made_cut.r.total == 5, F.encode(made_cut))
production_since_tool.functions.production_since = real_production_since
local made_again = call("sweep", { metric = "item_made", subject = "iron-plate" })
check("item_made answers again once the delegate stops cutting",
      made_again.ok and made_again.r.found ~= false, F.encode(made_again))

-- ── item_rate: delegates to item_rate per (force, surface) ────────────
local no_subject_rate = call("sweep", { metric = "item_rate" })
check("item_rate without a subject is refused before any pass runs",
      no_subject_rate.ok and no_subject_rate.r.found == false
      and no_subject_rate.r.reason:find("subject") ~= nil, F.encode(no_subject_rate))

-- fakegame's get_flow_count does not vary by surface (only the lifetime
-- get_input_count/get_output_count pair check S.last_stats_surface), so the
-- real fixture answers the same rate on nauvis and platform-1: 60 + 60*0.5
-- input, 20 + 20*0.5 output, over the normal/uncommon qualities
-- (S.quality_share). This is still a real two-row axis answer, just a tied
-- one; the differently-valued stub below is what proves the sort.
local rate = call("sweep", { metric = "item_rate", subject = "iron-plate" })
check("item_rate defaults to force+surface, two rows, tied at 90/min each",
      rate.ok and rate.r.axis == "force+surface"
      and row_eq(rate.r.rows[1], "player on nauvis", 90)
      and row_eq(rate.r.rows[2], "player on platform-1", 90)
      and rate.r.total == 2, F.encode(rate))
local rate_by_force = call("sweep", { metric = "item_rate", subject = "iron-plate", axis = "force" })
check("item_rate's force axis sums both surfaces' rates",
      rate_by_force.ok and row_eq(rate_by_force.r.rows[1], "player", 180), F.encode(rate_by_force))

-- Axis check with real values apart, and tonumber on a string value: the
-- delegate is stubbed to answer differently per surface, which the real
-- fixture cannot do, and to send produced_per_min as a STRING the way this
-- tool's own bounded.round already does for a fraction.
local production_tool = require("scripts.tools.production")
local real_item_rate = production_tool.functions.item_rate
production_tool.functions.item_rate = function(a)
  local value = (a.surface == "nauvis") and "100" or "40"
  return { found = true, force = a.force, surface = a.surface, item = a.item, window = a.window,
           produced_per_min = value, consumed_per_min = "0", net_per_min = value }
end
local rate_apart = call("sweep", { metric = "item_rate", subject = "iron-plate" })
check("item_rate's force+surface axis keeps nauvis and platform-1 apart, largest first",
      rate_apart.ok and row_eq(rate_apart.r.rows[1], "player on nauvis", 100)
      and row_eq(rate_apart.r.rows[2], "player on platform-1", 40), F.encode(rate_apart))
local rate_apart_summed = call("sweep", { metric = "item_rate", subject = "iron-plate", axis = "force" })
check("item_rate's force axis sums the stubbed 100 and 40 to 140",
      rate_apart_summed.ok and row_eq(rate_apart_summed.r.rows[1], "player", 140), F.encode(rate_apart_summed))

-- Refusal: the same forward-defensive guard as item_made, stubbed the same
-- way, through the same call-time seam.
production_tool.functions.item_rate = function(_a)
  return { found = true, produced_per_min = "10", total = 5, shown = 3 }
end
local rate_cut = call("sweep", { metric = "item_rate", subject = "iron-plate" })
check("item_rate refuses whole when its delegate reports a cut",
      rate_cut.ok and rate_cut.r.found == false and rate_cut.r.shown == 3 and rate_cut.r.total == 5,
      F.encode(rate_cut))
production_tool.functions.item_rate = real_item_rate
local rate_restored = call("sweep", { metric = "item_rate", subject = "iron-plate" })
check("item_rate answers again once the delegate stops cutting",
      rate_restored.ok and rate_restored.r.found ~= false, F.encode(rate_restored))

-- ── pollution: delegates to pollution once per surface, force is echoed ──
-- Real fixture values differ by surface on their own (S.pollution: 1234.5
-- on nauvis, 0 on platform-1), so this is both the happy path and the sort
-- check: envelope.lua's own table.sort has to put nauvis first.
local no_force_pollution = call("sweep", { metric = "pollution" })
check("pollution needs a force to ask as, and none was on this call",
      no_force_pollution.ok and no_force_pollution.r.found == false
      and no_force_pollution.r.reason:find("force") ~= nil, F.encode(no_force_pollution))

local pollution = call("sweep", { metric = "pollution", force = "player" })
check("pollution defaults to the surface axis, largest first",
      pollution.ok and pollution.r.axis == "surface"
      and row_num_eq(pollution.r.rows[1], "nauvis", 1234.5)
      and row_num_eq(pollution.r.rows[2], "platform-1", 0)
      and pollution.r.total == 2, F.encode(pollution))

local pollution_bad_axis = call("sweep", { metric = "pollution", force = "player", axis = "force" })
check("pollution only sweeps by surface, and says so",
      pollution_bad_axis.ok and pollution_bad_axis.r.found == false
      and pollution_bad_axis.r.reason:find("surface") ~= nil, F.encode(pollution_bad_axis))

local environment_tool = require("scripts.tools.environment")
local real_pollution = environment_tool.functions.pollution
environment_tool.functions.pollution = function(_a)
  return { found = true, total_pollution = "5", total = 5, shown = 3 }
end
local pollution_cut = call("sweep", { metric = "pollution", force = "player" })
check("pollution refuses whole when its delegate reports a cut",
      pollution_cut.ok and pollution_cut.r.found == false
      and pollution_cut.r.shown == 3 and pollution_cut.r.total == 5, F.encode(pollution_cut))
environment_tool.functions.pollution = real_pollution
local pollution_restored = call("sweep", { metric = "pollution", force = "player" })
check("pollution answers again once the delegate stops cutting",
      pollution_restored.ok and pollution_restored.r.found ~= false, F.encode(pollution_restored))

-- ── evolution: delegates to evolution per (force, surface) ────────────
-- Real fixture values differ by surface too (0.42 on nauvis, 0 on
-- platform-1, environment's own force[method] closures), so again both the
-- happy path and the sort check in one.
local evo = call("sweep", { metric = "evolution" })
check("evolution defaults to force+surface, largest first",
      evo.ok and evo.r.axis == "force+surface"
      and row_num_eq(evo.r.rows[1], "player on nauvis", 0.42)
      and row_num_eq(evo.r.rows[2], "player on platform-1", 0)
      and evo.r.unit == "factor", F.encode(evo))

local evo_no_force = call("sweep", { metric = "evolution", axis = "surface" })
check("evolution's surface axis needs a force to read across surfaces, and none was on this call",
      evo_no_force.ok and evo_no_force.r.found == false
      and evo_no_force.r.reason:find("force") ~= nil, F.encode(evo_no_force))

local evo_surface = call("sweep", { metric = "evolution", axis = "surface", force = "player" })
check("evolution's surface axis reads the one asking force across every surface",
      evo_surface.ok and evo_surface.r.axis == "surface"
      and row_num_eq(evo_surface.r.rows[1], "nauvis", 0.42)
      and row_num_eq(evo_surface.r.rows[2], "platform-1", 0), F.encode(evo_surface))

local evo_bad_axis = call("sweep", { metric = "evolution", axis = "player" })
check("evolution does not sweep by player, and says which axes it does",
      evo_bad_axis.ok and evo_bad_axis.r.found == false, F.encode(evo_bad_axis))

local real_evolution = environment_tool.functions.evolution
environment_tool.functions.evolution = function(_a)
  return { found = true, evolution_factor = "0.5", total = 5, shown = 3 }
end
local evo_cut = call("sweep", { metric = "evolution" })
check("evolution refuses whole when its delegate reports a cut",
      evo_cut.ok and evo_cut.r.found == false and evo_cut.r.shown == 3 and evo_cut.r.total == 5,
      F.encode(evo_cut))
environment_tool.functions.evolution = real_evolution
local evo_restored = call("sweep", { metric = "evolution" })
check("evolution answers again once the delegate stops cutting",
      evo_restored.ok and evo_restored.r.found ~= false, F.encode(evo_restored))

-- ── robots and networks: delegate to logistics_summary{contents=false} ──
-- Real fixture: nauvis carries two networks, 120 + 5 logistic_robots and 2
-- networks total; platform-1 carries none (force.logistic_networks has no
-- "platform-1" key at all, logistics.lua's own `or {}`), so both metrics
-- answer with a real, sortable difference on the unstubbed fixture too.
S.contents_reads = 0
local robots = call("sweep", { metric = "robots" })
check("robots sums logistic_robots, summed over both surfaces by default",
      robots.ok and robots.r.axis == "force" and row_eq(robots.r.rows[1], "player", 125),
      F.encode(robots))
local robots_cells = call("sweep", { metric = "robots", axis = "force+surface" })
check("robots' force+surface axis keeps nauvis (125) and platform-1 (0) apart",
      robots_cells.ok and row_eq(robots_cells.r.rows[1], "player on nauvis", 125)
      and row_eq(robots_cells.r.rows[2], "player on platform-1", 0), F.encode(robots_cells))

local networks = call("sweep", { metric = "networks" })
check("networks counts logistics_summary's own whole total, summed over surfaces",
      networks.ok and row_eq(networks.r.rows[1], "player", 2), F.encode(networks))
local networks_cells = call("sweep", { metric = "networks", axis = "force+surface" })
check("networks' force+surface axis keeps nauvis (2) and platform-1 (0) apart",
      networks_cells.ok and row_eq(networks_cells.r.rows[1], "player on nauvis", 2)
      and row_eq(networks_cells.r.rows[2], "player on platform-1", 0), F.encode(networks_cells))

check("neither sweep read a network's contents: contents=false actually skipped get_contents",
      S.contents_reads == 0, S.contents_reads)

local robots_bad_axis = call("sweep", { metric = "robots", axis = "surface" })
check("robots does not sweep by surface, and says which axes it does",
      robots_bad_axis.ok and robots_bad_axis.r.found == false, F.encode(robots_bad_axis))

-- The contract's real distinction between the two metrics: robots sums a
-- list logistics_summary can cut, networks reads a total that is whole
-- however much of the list got left out. One stub, both answers checked
-- against it, so the difference in behaviour is what this test actually
-- pins down rather than each metric's refusal in isolation.
local logistics_tool = require("scripts.tools.logistics")
local real_logistics_summary = logistics_tool.functions.logistics_summary
logistics_tool.functions.logistics_summary = function(a)
  return { found = true, force = a.force, surface = a.surface, total = 5, shown = 3,
           networks = { { logistic_robots = 10 } } }
end
-- robots reads the whole-surface total the tool sums BEFORE it cuts its
-- rows, so a cut list is no obstacle: the first version summed the rows and
-- refused on the cut, which made the metric unable to answer on any base
-- with more than a handful of networks and told the model to name a surface
-- the sweep tool has no parameter for. The stub above carries no total, the
-- shape an older companion sends, and that is the one case robots refuses.
local robots_old = call("sweep", { metric = "robots" })
check("robots refuses when the delegate sends no whole total (an older companion)",
      robots_old.ok and robots_old.r.found == false
      and tostring(robots_old.r.reason):find("older") ~= nil, F.encode(robots_old))
logistics_tool.functions.logistics_summary = function(a)
  return { found = true, force = a.force, surface = a.surface, total = 5, shown = 3,
           logistic_robots_total = 25, construction_robots_total = 4,
           networks = { { logistic_robots = 10 } } }
end
local robots_cut = call("sweep", { metric = "robots" })
check("robots answers from the pre-cut total however many rows were shown: 25 + 25 over both surfaces",
      robots_cut.ok and robots_cut.r.found ~= false
      and row_eq(robots_cut.r.rows[1], "player", 50), F.encode(robots_cut))
local networks_not_cut = call("sweep", { metric = "networks" })
check("networks does not refuse on the same cut, reply.total is whole: 5 + 5 summed over both surfaces",
      networks_not_cut.ok and networks_not_cut.r.found ~= false
      and row_eq(networks_not_cut.r.rows[1], "player", 10), F.encode(networks_not_cut))
logistics_tool.functions.logistics_summary = real_logistics_summary

local robots_restored = call("sweep", { metric = "robots" })
check("robots answers again once logistics_summary stops cutting",
      robots_restored.ok and robots_restored.r.found ~= false, F.encode(robots_restored))

-- ── no sweep call in this file wrote storage ───────────────────────────
local before = F.encode(storage)
call("sweep", { metric = "item_made", subject = "iron-plate" })
call("sweep", { metric = "robots" })
check("stage5's own sweep calls write no storage", F.encode(storage) == before)


-- ── the guards that run before any pass ──────────────────────────────
-- A subject that is not a prototype, or a window the game does not offer,
-- is refused with a card the model can act on, before a single force is
-- asked. Without these, a misspelt item swept every force and answered a
-- confident zero for each, which reads as "nobody has made any" rather than
-- "no such item", and a wrong window escaped as a bare provider_error.
local no_item = call("sweep", { force = "player", metric = "item_made", subject = "iron-plat" })
check("item_made refuses a subject that is not an item prototype, before any pass",
      no_item.ok and no_item.r.found == false and tostring(no_item.r.reason):find("no item prototype") ~= nil,
      F.encode(no_item))
local no_item_rate = call("sweep", { force = "player", metric = "item_rate", subject = "iron-plat" })
check("item_rate refuses a subject that is not an item prototype",
      no_item_rate.ok and no_item_rate.r.found == false
      and tostring(no_item_rate.r.reason):find("no item prototype") ~= nil, F.encode(no_item_rate))
local no_fluid = call("sweep", { force = "player", metric = "fluid_rate", subject = "nonexistent-fluid-xyz" })
check("fluid_rate refuses a subject that is not a fluid prototype rather than answering a number",
      no_fluid.ok and no_fluid.r.found == false and tostring(no_fluid.r.reason):find("no fluid prototype") ~= nil,
      F.encode(no_fluid))
local bad_window = call("sweep", { force = "player", metric = "item_rate", subject = "iron-plate", window = "10m" })
check("item_rate refuses a window the game does not offer, as a card, not a provider_error",
      bad_window.ok and bad_window.r.found == false and tostring(bad_window.r.reason):find("unknown window") ~= nil
      and bad_window.r.sweep_v == 1, F.encode(bad_window))
local bad_fluid_window = call("sweep", { force = "player", metric = "fluid_rate", subject = "crude-oil", window = "10m" })
check("fluid_rate refuses a window the game does not offer",
      bad_fluid_window.ok and bad_fluid_window.r.found == false
      and tostring(bad_fluid_window.r.reason):find("unknown window") ~= nil, F.encode(bad_fluid_window))
local good_window = call("sweep", { force = "player", metric = "fluid_rate", subject = "crude-oil", window = "one_hour" })
check("fluid_rate accepts a real window and reads it: player's one_hour rate is 60",
      good_window.ok and good_window.r.found ~= false and good_window.r.rows
      and good_window.r.rows[1][1] == "player" and tonumber(good_window.r.rows[1][2]) == 60, F.encode(good_window))

-- ── two forces, so "largest first" can actually fail ──────────────────
-- Every check above walks a single force, and one row satisfies any order.
-- team-3 gets a player, and larger numbers than player on every metric, so
-- it must sort FIRST on each; deleting envelope.lua's sort turns every one
-- of these red, which nothing in this file did before.
local team3 = game.forces["team-3"]
local saved_players = team3.players
team3.players = { game.get_player(1) }
S.team3_scale = 3
S.team3_evolution = 0.9
team3.logistic_networks = { nauvis = {
  { network_id = 9, cells = { {} }, all_logistic_robots = 999, available_logistic_robots = 0,
    all_construction_robots = 0, available_construction_robots = 0 },
  { network_id = 10, cells = { {} }, all_logistic_robots = 1, available_logistic_robots = 0,
    all_construction_robots = 0, available_construction_robots = 0 },
  { network_id = 11, cells = { {} }, all_logistic_robots = 1, available_logistic_robots = 0,
    all_construction_robots = 0, available_construction_robots = 0 },
} }
local saved_platform_pollution = S.pollution["platform-1"]
S.pollution["platform-1"] = 5000 -- larger than nauvis, and sorting after it by name

local function first_name(reply) return reply.ok and reply.r.rows and reply.r.rows[1] and reply.r.rows[1][1] end
check("item_made ranks team-3 above player when it has made more",
      first_name(call("sweep", { force = "player", metric = "item_made", subject = "iron-plate" })) == "team-3",
      F.encode(call("sweep", { force = "player", metric = "item_made", subject = "iron-plate" })))
check("item_rate ranks team-3 above player on the force axis",
      first_name(call("sweep", { force = "player", metric = "item_rate", subject = "iron-plate", axis = "force" })) == "team-3",
      F.encode(call("sweep", { force = "player", metric = "item_rate", subject = "iron-plate", axis = "force" })))
check("item_rate ranks team-3 on nauvis first on the compound axis",
      first_name(call("sweep", { force = "player", metric = "item_rate", subject = "iron-plate" })) == "team-3 on nauvis",
      F.encode(call("sweep", { force = "player", metric = "item_rate", subject = "iron-plate" })))
check("pollution ranks the dirtier platform above nauvis whatever the name order",
      first_name(call("sweep", { force = "player", metric = "pollution" })) == "platform-1",
      F.encode(call("sweep", { force = "player", metric = "pollution" })))
check("evolution ranks team-3 first on the compound axis",
      first_name(call("sweep", { force = "player", metric = "evolution" })) == "team-3 on nauvis",
      F.encode(call("sweep", { force = "player", metric = "evolution" })))
check("robots ranks team-3 above player",
      first_name(call("sweep", { force = "player", metric = "robots" })) == "team-3",
      F.encode(call("sweep", { force = "player", metric = "robots" })))
check("networks ranks team-3 (3) above player (2)",
      first_name(call("sweep", { force = "player", metric = "networks" })) == "team-3",
      F.encode(call("sweep", { force = "player", metric = "networks" })))

team3.players = saved_players
team3.logistic_networks = {}
S.team3_scale = 0
S.team3_evolution = 0
S.pollution["platform-1"] = saved_platform_pollution

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
