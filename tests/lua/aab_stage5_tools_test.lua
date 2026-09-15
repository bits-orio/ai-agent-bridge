-- AI Agent Bridge - tests/lua/aab_stage5_tools_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Stage 5 Unit A: the four new tools (kills, built, fluid_rate, trains) and
-- their sweep metrics, driven through the aab-rpc call op against the fake
-- game exactly the way aab_breadth_test.lua drives the phase5 registry's
-- first four and aab_stage5_metrics_test.lua drives Stage 5 Unit B's six.
-- Kept in its own file per the stage5 contract rather than folded into
-- either of those, which this file does not edit.

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

local NEW4 = { "kills", "built", "fluid_rate", "trains" }
-- Minimal args each tool answers successfully with, force added by each check.
local HAPPY_ARGS = {
  kills      = { surface = "nauvis" },
  built      = { surface = "nauvis" },
  fluid_rate = { surface = "nauvis", fluid = "crude-oil", window = "one_minute" },
  trains     = { surface = "nauvis" },
}
local function with_force(name, force)
  local a = { force = force }
  for k, v in pairs(HAPPY_ARGS[name]) do a[k] = v end
  return a
end

-- ── the manifest: four new tools, tier 1, no reserved `force` param ───────
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
local manifest = mine.ok and mine.r.tools or nil
check("the companion still has a manifest", manifest ~= nil, F.encode(mine))
local manifest_bytes = #F.encode(mine.r)
print("STAGE5 UNIT A manifest op reply bytes: " .. manifest_bytes)
check("the manifest still fits one reply with room to spare (CAPS.manifest = 32768)",
      manifest_bytes < 16384, manifest_bytes)

for _, name in ipairs(NEW4) do
  local entry = manifest and manifest[name]
  check("manifest lists " .. name, entry ~= nil and type(entry.desc) == "string" and #entry.desc > 40,
        entry and entry.desc)
  check(name .. " declares tier 1 honestly", entry and entry.tier == "1", entry and entry.tier)
  for param, spec in pairs((entry or {}).params or {}) do
    -- Lua patterns have no alternation, so the type words are tried one at
    -- a time; a single pattern with | in it matched only the literal text
    -- "string|integer|number|boolean" and never anything real.
    local word = spec:match("^(%a+)!? ")
    local known = { string = true, integer = true, number = true, boolean = true }
    check(name .. "." .. param .. " parses", word ~= nil and known[word] == true, spec)
    check(name .. " does not declare force", param ~= "force", param)
  end
end

-- ── every one of the four refuses an unknown force ─────────────────────
for _, name in ipairs(NEW4) do
  local bad = call(name, { force = "no-such-force" })
  check(name .. " refuses an unknown force", (not bad.ok) and bad.e == "provider_error", F.encode(bad))
end

-- ── a bare call with no argument table at all says force is required ──
-- probe.call substitutes {} for a missing `a` (scripts/probe.lua's own
-- comment), so every one of these reaches force_lookup.require_force first.
for _, name in ipairs(NEW4) do
  local bare = rpc({ op = "call", i = "ai-agent-bridge-tools", f = name })
  check(name .. " with no argument table says force is required",
        (not bare.ok) and bare.e == "provider_error" and bare.m == "force is required", F.encode(bare))
end

-- ── every reply fits the rpc byte cap with room to spare ──────────────
for _, name in ipairs(NEW4) do
  S.rcon_replies = {}
  rpc({ op = "call", i = "ai-agent-bridge-tools", f = name, a = with_force(name, "player") })
  local bytes = #S.rcon_replies[#S.rcon_replies]
  check(name .. " reply is small (" .. bytes .. " bytes)", bytes < 4000, bytes)
end

-- ── unknown surface: found=false, not an error, for every one of the four ──
for _, name in ipairs(NEW4) do
  local a = with_force(name, "player")
  a.surface = "atlantis"
  local bad = call(name, a)
  check(name .. " answers found=false for an unknown surface",
        bad.ok and bad.r.found == false and bad.r.reason:find("list_surfaces"), F.encode(bad))
end

-- ── no tool in this file writes storage ────────────────────────────────
local before_any = F.encode(storage)
for _, name in ipairs(NEW4) do call(name, with_force(name, "player")) end
check("no stage5 unit A tool writes storage", F.encode(storage) == before_any)

-- ── kills ────────────────────────────────────────────────────────────
-- Fixture (fakegame.lua): player killed 40 small-biter + 10 medium-biter and
-- lost 3 characters on nauvis, and 5 more small-biter on platform-1; team-3
-- killed 99 small-biter on nauvis alone, more than player's 55-kill total,
-- so a sort has a real leader to find.
local kills_nauvis = call("kills", { force = "player", surface = "nauvis" })
check("kills on one surface ok", kills_nauvis.ok and kills_nauvis.r.found == true, F.encode(kills_nauvis))
check("kills on nauvis alone", kills_nauvis.ok and kills_nauvis.r.kills == 50 and kills_nauvis.r.losses == 3
      and kills_nauvis.r.surface == "nauvis", F.encode(kills_nauvis.r))
check("kills names the kinds killed most, largest first",
      kills_nauvis.ok and kills_nauvis.r.top[1].name == "small-biter" and kills_nauvis.r.top[1].count == 40
      and kills_nauvis.r.top[2].name == "medium-biter" and kills_nauvis.r.top[2].count == 10,
      kills_nauvis.ok and F.encode(kills_nauvis.r.top))
check("kills counts distinct kinds", kills_nauvis.ok and kills_nauvis.r.distinct_kinds == 2)

local kills_all_surfaces = call("kills", { force = "player" })
check("kills with no surface sums every surface",
      kills_all_surfaces.ok and kills_all_surfaces.r.kills == 55 and kills_all_surfaces.r.losses == 3
      and kills_all_surfaces.r.surface == "all", F.encode(kills_all_surfaces.r))
check("kills' per-surface breakdown is largest first and reports how many were counted",
      kills_all_surfaces.ok and kills_all_surfaces.r.surfaces_counted == 2
      and kills_all_surfaces.r.surfaces[1].surface == "nauvis" and kills_all_surfaces.r.surfaces[1].kills == 50
      and kills_all_surfaces.r.surfaces[2].surface == "platform-1" and kills_all_surfaces.r.surfaces[2].kills == 5,
      F.encode(kills_all_surfaces.r.surfaces))

local kills_limited = call("kills", { force = "player", limit = 1 })
check("kills' limit bounds the top list", kills_limited.ok and #kills_limited.r.top == 1, F.encode(kills_limited))

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local kills_all = call("kills", { all = true })
check("kills all=true carries no top-level force field", kills_all.ok and kills_all.r.force == nil, F.encode(kills_all))
check("kills all=true carries no items or top list, kills/losses only",
      kills_all.ok and kills_all.r.forces[1].top == nil and kills_all.r.forces[1].distinct_kinds == nil,
      F.encode(kills_all))
check("kills all=true ranks by kills, not by force name: team-3 (99) sorts before player (55)",
      kills_all.ok and kills_all.r.total == 2 and kills_all.r.forces[1].force == "team-3"
      and kills_all.r.forces[1].kills == 99 and kills_all.r.forces[1].losses == 0
      and kills_all.r.forces[2].force == "player" and kills_all.r.forces[2].kills == 55, F.encode(kills_all))
S.team3.players = {}

-- ── built ────────────────────────────────────────────────────────────
-- Fixture: player built 6 assembling-machine-2 + 2 lab and mined 1
-- stone-furnace on nauvis, plus 3 solar-panel on platform-1; team-3 built
-- 200 lab on nauvis, more than player's 11-built total.
local built_nauvis = call("built", { force = "player", surface = "nauvis" })
check("built on one surface ok", built_nauvis.ok and built_nauvis.r.found == true, F.encode(built_nauvis))
check("built on nauvis alone", built_nauvis.ok and built_nauvis.r.built == 8 and built_nauvis.r.mined == 1
      and built_nauvis.r.surface == "nauvis", F.encode(built_nauvis.r))
check("built names the kinds built most, largest first",
      built_nauvis.ok and built_nauvis.r.top[1].name == "assembling-machine-2" and built_nauvis.r.top[1].count == 6
      and built_nauvis.r.top[2].name == "lab" and built_nauvis.r.top[2].count == 2,
      built_nauvis.ok and F.encode(built_nauvis.r.top))
check("built counts distinct kinds", built_nauvis.ok and built_nauvis.r.distinct_kinds == 2)

local built_all_surfaces = call("built", { force = "player" })
check("built with no surface sums every surface",
      built_all_surfaces.ok and built_all_surfaces.r.built == 11 and built_all_surfaces.r.mined == 1
      and built_all_surfaces.r.surface == "all", F.encode(built_all_surfaces.r))
check("built's per-surface breakdown is largest first",
      built_all_surfaces.ok and built_all_surfaces.r.surfaces_counted == 2
      and built_all_surfaces.r.surfaces[1].surface == "nauvis" and built_all_surfaces.r.surfaces[1].built == 8
      and built_all_surfaces.r.surfaces[2].surface == "platform-1" and built_all_surfaces.r.surfaces[2].built == 3,
      F.encode(built_all_surfaces.r.surfaces))
check("built's distinct_kinds grows once a second surface's kinds are counted in",
      built_all_surfaces.ok and built_all_surfaces.r.distinct_kinds == 3, built_all_surfaces.ok and built_all_surfaces.r.distinct_kinds)

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local built_all = call("built", { all = true })
check("built all=true carries no top-level force field", built_all.ok and built_all.r.force == nil, F.encode(built_all))
check("built all=true ranks by built, not by force name: team-3 (200) sorts before player (11)",
      built_all.ok and built_all.r.total == 2 and built_all.r.forces[1].force == "team-3"
      and built_all.r.forces[1].built == 200 and built_all.r.forces[1].mined == 0
      and built_all.r.forces[2].force == "player" and built_all.r.forces[2].built == 11, F.encode(built_all))
S.team3.players = {}

-- ── fluid_rate ───────────────────────────────────────────────────────
-- Fixture: player makes 90/min, uses 30/min on nauvis, and 10/min, 5/min on
-- platform-1; team-3 makes 500/min on nauvis alone, more than player's
-- 100/min two-surface total.
local rate_nauvis = call("fluid_rate", { force = "player", surface = "nauvis", fluid = "crude-oil", window = "one_minute" })
check("fluid_rate on one surface ok", rate_nauvis.ok and rate_nauvis.r.found == true, F.encode(rate_nauvis))
check("fluid_rate reports produced, consumed and net",
      rate_nauvis.ok and rate_nauvis.r.produced_per_min == 90 and rate_nauvis.r.consumed_per_min == 30
      and rate_nauvis.r.net_per_min == 60, F.encode(rate_nauvis.r))

local rate_no_surface = call("fluid_rate", { force = "player", fluid = "crude-oil", window = "one_minute" })
check("fluid_rate's surface is required, the same as item_rate's",
      rate_no_surface.ok and rate_no_surface.r.found == false
      and rate_no_surface.r.reason:find("surface is required") ~= nil, F.encode(rate_no_surface))

local no_fluid = call("fluid_rate", { force = "player", surface = "nauvis", window = "one_minute" })
check("fluid_rate without a fluid is a provider_error",
      (not no_fluid.ok) and no_fluid.e == "provider_error", F.encode(no_fluid))

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local rate_all = call("fluid_rate", { all = true, fluid = "crude-oil", window = "one_minute" })
check("fluid_rate all=true carries no top-level force field", rate_all.ok and rate_all.r.force == nil, F.encode(rate_all))
check("fluid_rate all=true sums every surface per force, largest producer first: team-3 (500) before player (100)",
      rate_all.ok and rate_all.r.total == 2 and rate_all.r.forces[1].force == "team-3"
      and rate_all.r.forces[1].produced_per_min == 500 and rate_all.r.forces[1].consumed_per_min == 100
      and rate_all.r.forces[2].force == "player" and rate_all.r.forces[2].produced_per_min == 100
      and rate_all.r.forces[2].consumed_per_min == 35, F.encode(rate_all))
S.team3.players = {}

-- fluid_rate's all=true has to sort on the RAW number, never the rounded
-- wire value: bounded.round leaves a whole number as a plain number but a
-- fraction as a short decimal STRING (scripts/tools/bounded.lua), and Lua's
-- `>` on two strings compares byte by byte, so "100.7" < "12.3" (the second
-- character, '0' < '2') even though 100.7 is the larger rate. Every other
-- fixture value in this file is a whole number, which cannot exercise this,
-- so this is isolated on a direct stub of both forces' own engine method
-- rather than S.fluid_flow, which every check above this one depends on.
local real_player_fluid = game.forces.player.get_fluid_production_statistics
local real_team3_fluid = game.forces["team-3"].get_fluid_production_statistics
local zero_fluid_stats = { get_flow_count = function() return 0 end }
local function stub_fluid_stats(produced, consumed)
  return { get_flow_count = function(a) return a.category == "input" and produced or consumed end }
end
game.forces.player.get_fluid_production_statistics = function(surface)
  if surface.name == "nauvis" then return stub_fluid_stats(12.3, 1) end
  return zero_fluid_stats
end
game.forces["team-3"].get_fluid_production_statistics = function(surface)
  if surface.name == "nauvis" then return stub_fluid_stats(100.7, 1) end
  return zero_fluid_stats
end
S.team3.players = { { name = "Zed", valid = true, connected = true } }
local rate_fraction = call("fluid_rate", { all = true, fluid = "crude-oil", window = "one_minute" })
check("fluid_rate all=true sorts on the raw number, not the rounded wire string: 100.7 outranks 12.3",
      rate_fraction.ok and rate_fraction.r.forces[1].force == "team-3"
      and rate_fraction.r.forces[1].produced_per_min == "100.7"
      and rate_fraction.r.forces[2].force == "player"
      and rate_fraction.r.forces[2].produced_per_min == "12.3", F.encode(rate_fraction))
S.team3.players = {}
game.forces.player.get_fluid_production_statistics = real_player_fluid
game.forces["team-3"].get_fluid_production_statistics = real_team3_fluid

-- ── trains ───────────────────────────────────────────────────────────
-- Fixture: player has 3 trains on nauvis (1 moving, 1 manual) and 1 idle
-- automatic train on platform-1; team-3 has 5 trains across both surfaces
-- (3 moving, 1 manual), more than player's 4-train total.
local trains_nauvis = call("trains", { force = "player", surface = "nauvis" })
check("trains on one surface ok", trains_nauvis.ok and trains_nauvis.r.found == true, F.encode(trains_nauvis))
check("trains on nauvis alone",
      trains_nauvis.ok and trains_nauvis.r.trains == 3 and trains_nauvis.r.moving == 1
      and trains_nauvis.r.manual == 1 and trains_nauvis.r.surface == "nauvis", F.encode(trains_nauvis.r))

local trains_all_surfaces = call("trains", { force = "player" })
check("trains with no surface sums every surface",
      trains_all_surfaces.ok and trains_all_surfaces.r.trains == 4 and trains_all_surfaces.r.moving == 1
      and trains_all_surfaces.r.manual == 1 and trains_all_surfaces.r.surface == "all", F.encode(trains_all_surfaces.r))
check("trains' per-surface breakdown is largest first",
      trains_all_surfaces.ok and trains_all_surfaces.r.surfaces_counted == 2
      and trains_all_surfaces.r.surfaces[1].surface == "nauvis" and trains_all_surfaces.r.surfaces[1].trains == 3
      and trains_all_surfaces.r.surfaces[2].surface == "platform-1" and trains_all_surfaces.r.surfaces[2].trains == 1,
      F.encode(trains_all_surfaces.r.surfaces))

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local trains_all = call("trains", { all = true })
check("trains all=true carries no top-level force field", trains_all.ok and trains_all.r.force == nil, F.encode(trains_all))
check("trains all=true ranks by train count, not by force name: team-3 (5) sorts before player (4)",
      trains_all.ok and trains_all.r.total == 2 and trains_all.r.forces[1].force == "team-3"
      and trains_all.r.forces[1].trains == 5 and trains_all.r.forces[1].moving == 3 and trains_all.r.forces[1].manual == 1
      and trains_all.r.forces[2].force == "player" and trains_all.r.forces[2].trains == 4, F.encode(trains_all))
S.team3.players = {}

-- ── the registry carries all four, and the sweep manifest names them ──
local sweep_registry = require("scripts.sweep.registry")
local sweep_desc = mine.r.tools.sweep.desc
for _, name in ipairs(NEW4) do
  check("sweep's generated desc names " .. name, sweep_desc:find(name, 1, true) ~= nil, sweep_desc)
end
local cards = sweep_registry.cards()
local by_name = {}
for _, card in ipairs(cards) do by_name[card.metric] = card end
for _, name in ipairs(NEW4) do
  check("the registry carries " .. name, by_name[name] ~= nil)
end
check("only fluid_rate declares a subject among this stage's own four",
      by_name.kills.subject == nil and by_name.built.subject == nil and by_name.trains.subject == nil
      and by_name.fluid_rate.subject ~= nil)
check("fluid_rate says what kind of subject", by_name.fluid_rate.subject_kind == "fluid")
check("fluid_rate's second axis is force+surface, the other three are force only",
      #by_name.kills.axes == 1 and #by_name.built.axes == 1 and #by_name.trains.axes == 1
      and #by_name.fluid_rate.axes == 2, F.encode(cards))

-- ── metric kills: delegates to kills{all=true} ─────────────────────────
local kills_metric_solo = call("sweep", { metric = "kills" })
check("sweep kills, one force, matches the tool's own all=true total",
      kills_metric_solo.ok and kills_metric_solo.r.axis == "force" and kills_metric_solo.r.unit == "count"
      and row_eq(kills_metric_solo.r.rows[1], "player", 55) and kills_metric_solo.r.total == 1,
      F.encode(kills_metric_solo))

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local kills_metric_two = call("sweep", { metric = "kills" })
check("sweep kills ranks team-3 (99) ahead of player (55)",
      kills_metric_two.ok and row_eq(kills_metric_two.r.rows[1], "team-3", 99)
      and row_eq(kills_metric_two.r.rows[2], "player", 55), F.encode(kills_metric_two))
S.team3.players = {}

local kills_tool = require("scripts.tools.kills")
local real_kills = kills_tool.functions.kills
kills_tool.functions.kills = function(_a)
  return { total = 5, shown = 3, forces = { { force = "team-3", kills = 10, losses = 0 } } }
end
local kills_cut = call("sweep", { metric = "kills" })
check("sweep kills refuses whole when its delegate reports a cut",
      kills_cut.ok and kills_cut.r.found == false and kills_cut.r.rows == nil
      and kills_cut.r.shown == 3 and kills_cut.r.total == 5, F.encode(kills_cut))
kills_tool.functions.kills = real_kills
local kills_restored = call("sweep", { metric = "kills" })
check("sweep kills answers again once the delegate stops cutting",
      kills_restored.ok and kills_restored.r.found ~= false, F.encode(kills_restored))

-- ── metric built: delegates to built{all=true} ─────────────────────────
local built_metric_solo = call("sweep", { metric = "built" })
check("sweep built, one force, matches the tool's own all=true total",
      built_metric_solo.ok and row_eq(built_metric_solo.r.rows[1], "player", 11)
      and built_metric_solo.r.total == 1, F.encode(built_metric_solo))

local built_tool = require("scripts.tools.built")
local real_built = built_tool.functions.built
built_tool.functions.built = function(_a)
  return { total = 5, shown = 3, forces = { { force = "team-3", built = 10, mined = 0 } } }
end
local built_cut = call("sweep", { metric = "built" })
check("sweep built refuses whole when its delegate reports a cut",
      built_cut.ok and built_cut.r.found == false and built_cut.r.rows == nil
      and built_cut.r.shown == 3 and built_cut.r.total == 5, F.encode(built_cut))
built_tool.functions.built = real_built
local built_restored = call("sweep", { metric = "built" })
check("sweep built answers again once the delegate stops cutting",
      built_restored.ok and built_restored.r.found ~= false, F.encode(built_restored))

-- ── metric trains: delegates to trains{all=true} ────────────────────────
local trains_metric_solo = call("sweep", { metric = "trains" })
check("sweep trains, one force, matches the tool's own all=true total",
      trains_metric_solo.ok and row_eq(trains_metric_solo.r.rows[1], "player", 4)
      and trains_metric_solo.r.total == 1, F.encode(trains_metric_solo))

local trains_tool = require("scripts.tools.trains")
local real_trains = trains_tool.functions.trains
trains_tool.functions.trains = function(_a)
  return { total = 5, shown = 3, forces = { { force = "team-3", trains = 10, moving = 0, manual = 0 } } }
end
local trains_cut = call("sweep", { metric = "trains" })
check("sweep trains refuses whole when its delegate reports a cut",
      trains_cut.ok and trains_cut.r.found == false and trains_cut.r.rows == nil
      and trains_cut.r.shown == 3 and trains_cut.r.total == 5, F.encode(trains_cut))
trains_tool.functions.trains = real_trains
local trains_restored = call("sweep", { metric = "trains" })
check("sweep trains answers again once the delegate stops cutting",
      trains_restored.ok and trains_restored.r.found ~= false, F.encode(trains_restored))

-- ── metric fluid_rate: per-cell reads, not all=true (force+surface needs
-- the breakdown all=true throws away) ──────────────────────────────────
local no_subject_rate = call("sweep", { metric = "fluid_rate" })
check("fluid_rate without a subject is refused before any pass runs",
      no_subject_rate.ok and no_subject_rate.r.found == false
      and no_subject_rate.r.reason:find("subject") ~= nil, F.encode(no_subject_rate))

local rate_force = call("sweep", { metric = "fluid_rate", subject = "crude-oil" })
check("sweep fluid_rate defaults to force, summed over both surfaces: 90 + 10 = 100",
      rate_force.ok and rate_force.r.axis == "force" and row_eq(rate_force.r.rows[1], "player", 100),
      F.encode(rate_force))

local rate_cells = call("sweep", { metric = "fluid_rate", subject = "crude-oil", axis = "force+surface" })
check("sweep fluid_rate's force+surface axis keeps nauvis (90) and platform-1 (10) apart",
      rate_cells.ok and row_eq(rate_cells.r.rows[1], "player on nauvis", 90)
      and row_eq(rate_cells.r.rows[2], "player on platform-1", 10), F.encode(rate_cells))

S.team3.players = { { name = "Zed", valid = true, connected = true } }
local rate_two_forces = call("sweep", { metric = "fluid_rate", subject = "crude-oil" })
check("sweep fluid_rate ranks team-3 (500) ahead of player (100)",
      rate_two_forces.ok and row_eq(rate_two_forces.r.rows[1], "team-3", 500)
      and row_eq(rate_two_forces.r.rows[2], "player", 100), F.encode(rate_two_forces))
S.team3.players = {}

local fluid_rate_tool = require("scripts.tools.fluid_rate")
local real_fluid_rate = fluid_rate_tool.functions.fluid_rate
fluid_rate_tool.functions.fluid_rate = function(_a)
  return { found = true, produced_per_min = "10", total = 5, shown = 3 }
end
local rate_cut = call("sweep", { metric = "fluid_rate", subject = "crude-oil" })
check("sweep fluid_rate refuses whole when its delegate reports a cut",
      rate_cut.ok and rate_cut.r.found == false and rate_cut.r.shown == 3 and rate_cut.r.total == 5,
      F.encode(rate_cut))
fluid_rate_tool.functions.fluid_rate = real_fluid_rate
local rate_restored = call("sweep", { metric = "fluid_rate", subject = "crude-oil" })
check("sweep fluid_rate answers again once the delegate stops cutting",
      rate_restored.ok and rate_restored.r.found ~= false, F.encode(rate_restored))

-- ── the window is read, not assumed ───────────────────────────────────
-- The fixture holds a distinct one_hour row for player on nauvis (60 in, 20
-- out) beside the one_minute row (90 in, 30 out). Every earlier check asked
-- for one_minute, so a precision hardcoded to one_minute passed all of them:
-- the fake was keyed on precision and nothing ever asked for a second one.
local hour = call("fluid_rate", { force = "player", surface = "nauvis", fluid = "crude-oil", window = "one_hour" })
check("fluid_rate reads the window it was asked for, not one_minute",
      hour.ok and hour.r.found == true and tonumber(hour.r.produced_per_min) == 60
      and tonumber(hour.r.consumed_per_min) == 20, F.encode(hour))
local hour_all = call("fluid_rate", { all = true, fluid = "crude-oil", window = "one_hour" })
check("fluid_rate all=true reads the window too: player's one_hour total is nauvis alone",
      hour_all.ok and hour_all.r.forces and hour_all.r.forces[1].force == "player"
      and tonumber(hour_all.r.forces[1].produced_per_min) == 60, F.encode(hour_all))

-- ── per-surface breakdowns are sorted, not walked ────────────────────
-- In the fixture nauvis holds the larger count AND sorts first by name, so
-- the "largest first" checks above pass with the breakdown's sort deleted:
-- the walk order already matched. Swap the surfaces' counts so the larger
-- one sorts LAST by name, and only a real sort puts it first.
local kc = S.kill_counts.player
kc.nauvis, kc["platform-1"] = kc["platform-1"], kc.nauvis
local kills_swapped = call("kills", { force = "player" })
check("kills' per-surface breakdown puts the larger surface first whatever its name",
      kills_swapped.ok and kills_swapped.r.surfaces[1].surface == "platform-1"
      and kills_swapped.r.surfaces[1].kills == 50 and kills_swapped.r.surfaces[2].surface == "nauvis",
      F.encode(kills_swapped.r.surfaces))
kc.nauvis, kc["platform-1"] = kc["platform-1"], kc.nauvis

local bc = S.build_counts.player
bc.nauvis, bc["platform-1"] = bc["platform-1"], bc.nauvis
local built_swapped = call("built", { force = "player" })
check("built's per-surface breakdown puts the larger surface first whatever its name",
      built_swapped.ok and built_swapped.r.surfaces[1].surface == "platform-1"
      and built_swapped.r.surfaces[1].built == 8 and built_swapped.r.surfaces[2].surface == "nauvis",
      F.encode(built_swapped.r.surfaces))
bc.nauvis, bc["platform-1"] = bc["platform-1"], bc.nauvis

for _, train in ipairs(S.trains) do
  if train.force_name == "player" then
    train.surface_name = (train.surface_name == "nauvis") and "platform-1" or "nauvis"
  end
end
local trains_swapped = call("trains", { force = "player" })
check("trains' per-surface breakdown puts the larger surface first whatever its name",
      trains_swapped.ok and trains_swapped.r.surfaces[1].surface == "platform-1"
      and trains_swapped.r.surfaces[1].trains == 3 and trains_swapped.r.surfaces[2].surface == "nauvis",
      F.encode(trains_swapped.r.surfaces))
for _, train in ipairs(S.trains) do
  if train.force_name == "player" then
    train.surface_name = (train.surface_name == "nauvis") and "platform-1" or "nauvis"
  end
end

-- ── the kinds are sorted, with enough of them that walk order cannot pass ─
-- With two kinds, pairs() walks them in the right order about half the time,
-- so deleting top_rows' sort left the two-kind checks above green two runs in
-- three. Five kinds in a deliberately scrambled order: a walk that happens to
-- come out descending is one chance in a hundred and twenty.
local saved_kills_nauvis = S.kill_counts.player.nauvis
S.kill_counts.player.nauvis = { input_counts = { ["a-biter"] = 1, ["b-biter"] = 7, ["c-biter"] = 3, ["d-biter"] = 9, ["e-biter"] = 5 }, output_counts = {} }
local kinds = call("kills", { force = "player", surface = "nauvis", limit = 5 })
local order = {}
for _, row in ipairs(kinds.ok and kinds.r.top or {}) do order[#order + 1] = row.name .. "=" .. row.count end
check("kills sorts five kinds largest first",
      table.concat(order, ",") == "d-biter=9,b-biter=7,e-biter=5,c-biter=3,a-biter=1", table.concat(order, ","))
S.kill_counts.player.nauvis = saved_kills_nauvis

local saved_build_nauvis = S.build_counts.player.nauvis
S.build_counts.player.nauvis = { input_counts = { a = 1, b = 7, c = 3, d = 9, e = 5 }, output_counts = {} }
local built_kinds = call("built", { force = "player", surface = "nauvis", limit = 5 })
local built_order = {}
for _, row in ipairs(built_kinds.ok and built_kinds.r.top or {}) do built_order[#built_order + 1] = row.name .. "=" .. row.count end
check("built sorts five kinds largest first",
      table.concat(built_order, ",") == "d=9,b=7,e=5,c=3,a=1", table.concat(built_order, ","))
S.build_counts.player.nauvis = saved_build_nauvis

-- ── bounding: total/shown when a tool's own all=true would cut ─────────
-- 105 forces beyond the two the fixture ships, each with one player, so
-- bounded.cut(rows, bounded.MAX_FORCES=100) has something real to cut
-- against: 1 (player) + 105 synthetic = 106 total, cut to 100 shown. The
-- kills statistics closures were only ever attached to `force` and S.team3
-- (attach_count_stats in fakegame.lua), so each synthetic force gets its own
-- trivial one answering zero on every surface, the same shape a real force
-- with no kill history gets from the engine's own empty input_counts.
-- Each synthetic force also answers fluid statistics, with a fraction so its
-- rows carry the three short decimal strings a real reply carries: the bound
-- on fluid_rate's reply was measured against half-size rows once, and past
-- roughly ninety forces the reply was refused whole as too_large.
local synth_fluid = { get_flow_count = function(a) return a.category == "input" and 12.5 or 4.25 end }
for i = 1, 105 do
  local name = string.format("sweep-synth-%03d", i)
  game.forces[name] = {
    name = name, players = { {} },
    get_kill_count_statistics = function() return { input_counts = {}, output_counts = {} } end,
    get_fluid_production_statistics = function() return synth_fluid end,
  }
end
local fluid_bounded = call("fluid_rate", { all = true, fluid = "crude-oil", window = "one_minute" })
check("fluid_rate all=true at 106 forces answers rather than tripping the call cap",
      fluid_bounded.ok and fluid_bounded.r.found ~= false and fluid_bounded.r.forces ~= nil,
      F.encode(fluid_bounded))
check("fluid_rate all=true at 106 forces reports total beside a shown that fits",
      fluid_bounded.ok and fluid_bounded.r.total == 106 and fluid_bounded.r.shown >= 1
      and fluid_bounded.r.shown <= 100 and #F.encode(fluid_bounded.r) <= 8000,
      fluid_bounded.ok and (fluid_bounded.r.shown .. " shown, " .. #F.encode(fluid_bounded.r) .. " bytes") or F.encode(fluid_bounded))
-- player makes 90 + 10 a minute across its two surfaces against 12.5 for
-- every synthetic force (team-3 has no player in this block), so player must
-- lead: the cut keeps the FIRST rows and the sort runs before it.
check("fluid_rate all=true keeps the leader through the cut",
      fluid_bounded.ok and fluid_bounded.r.forces[1] and fluid_bounded.r.forces[1].force == "player"
      and tonumber(fluid_bounded.r.forces[1].produced_per_min) == 100,
      F.encode(fluid_bounded.r.forces and fluid_bounded.r.forces[1]))
local kills_bounded = call("kills", { all = true })
check("kills all=true bounds its rows at bounded.MAX_FORCES and reports total beside shown",
      kills_bounded.ok and kills_bounded.r.total == 106 and kills_bounded.r.shown == 100
      and #kills_bounded.r.forces == 100, F.encode(kills_bounded))
check("player, the only real kill count among the synthetic zeroes, still leads",
      kills_bounded.ok and kills_bounded.r.forces[1].force == "player" and kills_bounded.r.forces[1].kills == 55,
      F.encode(kills_bounded.r.forces[1]))
for i = 1, 105 do
  game.forces[string.format("sweep-synth-%03d", i)] = nil
end
local kills_cleaned = call("kills", { all = true })
check("removing the synthetic forces leaves kills all=true back at baseline",
      kills_cleaned.ok and kills_cleaned.r.total == 1, F.encode(kills_cleaned))

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
