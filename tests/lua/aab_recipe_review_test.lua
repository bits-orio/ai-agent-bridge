-- AI Agent Bridge - tests/lua/aab_recipe_review_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The review fixes to recipe_needs: T1 (surface conditions on machines and
-- recipes, and the named recipe yielding when its chain cannot be finished
-- on the surface), T9 (approx says what is left out), F7 (a missing rate
-- defaults to one machine; one close name is used), T2 (one reply budget).
-- Driven through the aab-rpc call op against the fake game's recipe block.
--
-- The Space Age data is transcribed from data/space-age 2.1.23: the crusher
-- needs gravity 0 (entities.lua), the stone and steel furnaces pressure 10
-- (base-data-updates.lua), molten-iron-from-lava and casting-iron are
-- metallurgy recipes for the foundry (recipe.lua), lava is the fluid a
-- Vulcanus lava tile gives a pump (tiles-vulcanus.lua). Vulcanus' own
-- properties are pressure 4000 and gravity 40, Nauvis' 1000 and 10, a
-- platform's 0 and 0.

local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local S = F.install()
F.install_recipes(S)

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
local function needs(args) return rpc({ op = "call", i = "ai-agent-bridge-tools", f = "recipe_needs", a = args }) end
local function find(list, item)
  for _, row in ipairs(list or {}) do
    if row.item == item then return row end
  end
  return nil
end
local function has(list, word)
  for _, one in ipairs(list or {}) do
    if one == word then return true end
  end
  return false
end
local function item(name, amount) return { type = "item", name = name, amount = amount, probability = 1 } end
local function fluid(name, amount) return { type = "fluid", name = name, amount = amount, probability = 1 } end
local function input(p) p.probability = nil return p end

-- ── LuaSurface::get_property on the surfaces asked about ──────────────
local function with_properties(surface, pressure, gravity)
  local props = { pressure = pressure, gravity = gravity }
  surface.get_property = function(name)
    assert(type(name) == "string", "get_property wants a SurfacePropertyID")
    return props[name] or 0
  end
  return surface
end
with_properties(S.surfaces.nauvis, 1000, 10)
with_properties(S.surfaces["platform-1"], 0, 0)

-- ── Space Age, the parts this needs ────────────────────────────────────
-- Space Age gives the stone and steel furnaces a pressure of 10 or more
-- (base-data-updates.lua ten_pressure_condition); re-added here with it.
local TEN_PRESSURE = { { property = "pressure", min = 10, max = 1e300 } }
S.add_crafter({ "stone-furnace", "furnace", 1, { "smelting" }, { burner = true, surface_conditions = TEN_PRESSURE } })
S.add_crafter({ "steel-furnace", "furnace", 2, { "smelting" }, { burner = true, surface_conditions = TEN_PRESSURE } })
S.add_crafter({ "crusher", "assembling-machine", 1, { "crushing" },
                { surface_conditions = { { property = "gravity", min = 0, max = 0 } } } })
S.add_crafter({ "foundry", "assembling-machine", 4, { "metallurgy" }, { productivity = 0.5 } })
S.add_recipe({ name = "crusher", ingredients = { input(item("iron-plate", 20)) }, products = { item("crusher", 1) } })
S.add_recipe({ name = "foundry", ingredients = { input(item("steel-plate", 50)) }, products = { item("foundry", 1) } })
S.add_recipe({ name = "metallic-asteroid-crushing", category = "crushing", energy = 2, main_product = "",
               ingredients = { input(item("metallic-asteroid-chunk", 1)) }, products = { item("iron-ore", 20) } })
S.add_recipe({ name = "molten-iron-from-lava", category = "metallurgy", energy = 16, main_product = "",
               ingredients = { input(fluid("lava", 500)), input(item("calcite", 1)) },
               products = { fluid("molten-iron", 250), item("stone", 10) } })
S.add_recipe({ name = "molten-iron", category = "metallurgy", energy = 32, main_product = "",
               ingredients = { input(item("iron-ore", 50)), input(item("calcite", 1)) },
               products = { fluid("molten-iron", 500) } })
S.add_recipe({ name = "casting-iron", category = "metallurgy", energy = 3.2,
               ingredients = { input(fluid("molten-iron", 20)) }, products = { item("iron-plate", 2) } })
-- Not Space Age: a recipe whose own surface conditions hold on Vulcanus alone.
S.add_recipe({ name = "test-lava-glass", category = "metallurgy", energy = 1,
               surface_conditions = { { property = "pressure", min = 4000, max = 4000 } },
               ingredients = { input(item("stone", 1)) }, products = { item("test-lava-glass", 1) } })
S.add_resource("calcite", { item("calcite", 1) })
S.add_tile("lava", "lava")
-- Vulcanus' rocks, as a fresh 2.1.23 map has them: simple-entities its map
-- generator places, which give iron ore when mined by hand. Without them
-- the fixture hid the live failure: the rocks made iron ore "obtainable"
-- there, so iron plate smelted from ore looked like a chain that completes.
S.add_harvestable("huge-volcanic-rock", "simple-entity",
  { item("stone", 12), item("iron-ore", 18), item("copper-ore", 12), item("tungsten-ore", 9) })
local vulcanus = with_properties(S.add_surface({ name = "vulcanus",
  places = { entity = { "calcite", "coal", "huge-volcanic-rock" }, tile = { "lava" } } }), 4000, 40)
-- A Gleba-like surface: an iron stromatolite (simple-entity) gives iron
-- ore, no resource does, no lava, and the trees give wood.
S.add_harvestable("iron-stromatolite", "simple-entity", { item("stone", 4), item("iron-ore", 4) })
S.add_harvestable("test-tree", "tree", { item("wood", 4) })
S.add_recipe({ name = "test-wooden-box", ingredients = { input(item("wood", 2)) }, products = { item("test-wooden-box", 1) } })
with_properties(S.add_surface({ name = "test-marsh",
  places = { entity = { "iron-stromatolite", "test-tree", "stone" }, tile = { "water" } } }), 2000, 20)
local ship = with_properties(S.add_surface({ name = "ship-1", platform = true }), 0, 0)

-- ══ T1: surface conditions ══════════════════════════════════════════════
local vul = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "vulcanus" })
local plate = vul.ok and vul.r.targets[1] or {}
local plate_row = find(vul.ok and vul.r.rows, "iron-plate")
check("T1 on Vulcanus iron plate is cast, not smelted from ore nothing there makes",
      plate_row and plate_row.recipe == "casting-iron" and plate.machine == "foundry", F.encode(vul.r))
local molten = find(vul.ok and vul.r.rows, "molten-iron")
check("T1 its molten iron comes from lava, not from iron ore",
      molten and molten.recipe == "molten-iron-from-lava", F.encode(molten))
check("T1 no iron ore and no asteroid chunk anywhere in the Vulcanus answer",
      vul.ok and find(vul.r.rows, "iron-ore") == nil and find(vul.r.raw, "iron-ore") == nil
      and find(vul.r.raw, "metallic-asteroid-chunk") == nil, F.encode(vul.r))
local lava = find(vul.ok and vul.r.raw, "lava")
local calcite = find(vul.ok and vul.r.raw, "calcite")
check("T1 lava is pumped and calcite mined there", lava and lava.raw == "mined" and calcite and calcite.raw == "mined",
      F.encode(vul.r.raw))
check("T1 a chain that completes adds no not_makeable_on_surface",
      vul.ok and not has(vul.r.approx, "not_makeable_on_surface"), F.encode(vul.r.approx))

-- On Nauvis nothing changes: iron plate is smelted from mined ore.
local nauvis = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "nauvis" })
local nauvis_row = find(nauvis.ok and nauvis.r.rows, "iron-plate")
check("T1 on Nauvis the named recipe still wins: iron-plate from mined iron ore",
      nauvis_row and nauvis_row.recipe == "iron-plate" and find(nauvis.r.raw, "iron-ore").raw == "mined", F.encode(nauvis.r))

-- A rock or stromatolite is mined once and gone: ore it gives is no source.
local marsh = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "test-marsh" })
check("T1 ore only a stromatolite gives is not makeable there: approx and not_makeable name iron ore",
      marsh.ok and has(marsh.r.approx, "not_makeable_on_surface") and has(marsh.r.not_makeable, "iron-ore"), F.encode(marsh.r))
local box = needs({ force = "player", items = { "test-wooden-box" }, per_min = { 60 }, surface = "test-marsh" })
check("T1 wood its trees give still counts: no not_makeable_on_surface",
      box.ok and find(box.r.raw, "wood") ~= nil and not has(box.r.approx, "not_makeable_on_surface"), F.encode(box.r))

-- (a) a machine whose conditions fail is never picked: the stone furnace
-- needs pressure 10, a platform has 0. team-3 can build the stone furnace
-- alone, so off a platform it gets it and on one it is offered the electric
-- furnace, not yet buildable.
local fresh_ground = needs({ force = "team-3", items = { "iron-plate" }, per_min = { 60 }, surface = "nauvis" })
check("T1 a fresh force smelts in its stone furnace on Nauvis",
      fresh_ground.ok and fresh_ground.r.targets[1].machine == "stone-furnace", F.encode(fresh_ground.r.targets))
local fresh_ship = needs({ force = "team-3", items = { "iron-plate" }, per_min = { 60 }, surface = "ship-1" })
check("T1 on a platform the stone furnace is not eligible: the electric furnace, marked not unlocked",
      fresh_ship.ok and fresh_ship.r.targets[1].machine == "electric-furnace" and fresh_ship.r.targets[1].unlocked == false,
      F.encode(fresh_ship.r.targets))
-- (c) with no route that completes on the surface the pick stays, and
-- approx says which item cannot be had there.
check("T1 with nothing that completes on a platform, the pick stays iron-plate",
      fresh_ship.ok and find(fresh_ship.r.rows, "iron-plate").recipe == "iron-plate", F.encode(fresh_ship.r.rows))
check("T1 and approx says not_makeable_on_surface, naming iron ore",
      fresh_ship.ok and has(fresh_ship.r.approx, "not_makeable_on_surface")
      and has(fresh_ship.r.not_makeable, "iron-ore"), F.encode(fresh_ship.r))
local player_ship = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "ship-1" })
check("T1 the crusher can run in space: player's ship crushes iron ore, and the chunk is what cannot be had",
      player_ship.ok and find(player_ship.r.rows, "iron-ore") and find(player_ship.r.rows, "iron-ore").machine == "crusher"
      and has(player_ship.r.not_makeable, "metallic-asteroid-chunk"), F.encode(player_ship.r))
-- (b) a recipe whose own conditions fail is never picked.
local glass_nauvis = needs({ force = "player", items = { "test-lava-glass" }, per_min = { 60 }, surface = "nauvis" })
check("T1 a recipe that needs Vulcanus' pressure is not offered on Nauvis",
      glass_nauvis.ok and glass_nauvis.r.targets[1].raw == true and has(glass_nauvis.r.approx, "not_makeable_on_surface"),
      F.encode(glass_nauvis.r))
local glass_vulcanus = needs({ force = "player", items = { "test-lava-glass" }, per_min = { 60 }, surface = "vulcanus" })
check("T1 and is on Vulcanus", glass_vulcanus.ok and glass_vulcanus.r.targets[1].machine == "foundry",
      F.encode(glass_vulcanus.r))
-- A machine the caller names that cannot run there is refused in words.
local named = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "ship-1", machine = "stone-furnace" })
check("T1 a named machine the surface rules out is refused in the tool's own words",
      named.ok == false and tostring(named.m):find("ship-1", 1, true) ~= nil, F.encode(named))
-- No surface: nothing changes.
local anywhere = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 } })
check("T1 with no surface the conditions are not read and iron plate is smelted",
      anywhere.ok and find(anywhere.r.rows, "iron-plate").recipe == "iron-plate", F.encode(anywhere.r.rows))

-- ══ T9: approx says what the figures leave out ══════════════════════════
check("T9 approx always says no_modules_or_beacons",
      has(nauvis.r.approx, "no_modules_or_beacons") and has(vul.r.approx, "no_modules_or_beacons"), F.encode(nauvis.r.approx))
check("T9 a burner machine adds no_fuel: the stone furnace",
      fresh_ground.ok and has(fresh_ground.r.approx, "no_fuel"), F.encode(fresh_ground.r.approx))
check("T9 electric machines add no no_fuel",
      nauvis.ok and not has(nauvis.r.approx, "no_fuel"), F.encode(nauvis.r.approx))

-- ══ F7: a missing rate is one machine; one close name is used ═══════════
local bare = needs({ force = "player", items = { "iron-gear-wheel", "pipe" }, surface = "nauvis" })
check("F7 with neither per_min nor machines, one machine each is assumed and said",
      bare.ok and bare.r.assumed == "machines=1" and bare.r.mode == "machines"
      and tonumber(bare.r.targets[1].machines) == 1 and tonumber(bare.r.targets[2].machines) == 1, F.encode(bare))
S.add_resource("pm-gold-ore", { item("pm-gold-ore", 1) })
local gold = needs({ force = "player", items = { "gold-ore" }, per_min = { 60 } })
check("F7 gold-ore resolves to its one close match, pm-gold-ore, and says so",
      gold.ok and gold.r.found == true and gold.r.targets[1].item == "pm-gold-ore"
      and gold.r.resolved_from and gold.r.resolved_from[1].asked == "gold-ore"
      and gold.r.resolved_from[1].used == "pm-gold-ore", F.encode(gold))
S.add_resource("pm-gold-ore-dust", { item("pm-gold-ore-dust", 1) })
local two = needs({ force = "player", items = { "gold-ore" }, per_min = { 60 } })
local guesses = two.ok and two.r.did_you_mean and table.concat(two.r.did_you_mean["gold-ore"] or {}, ",") or ""
check("F7 with two close matches nothing is guessed: found=false, both suggested as before",
      two.ok and two.r.found == false and guesses:find("pm-gold-ore", 1, true) and guesses:find("pm-gold-ore-dust", 1, true),
      F.encode(two))

-- ══ T2: one reply budget ═══════════════════════════════════════════════
local recipe_reply = require("scripts.tools.recipe_reply")
local bounded = require("scripts.tools.bounded")
check("T2 recipe_needs fits the same reply budget every other tool does",
      bounded.REPLY_BUDGET ~= nil and recipe_reply.REPLY_BUDGET == bounded.REPLY_BUDGET,
      tostring(recipe_reply.REPLY_BUDGET) .. " vs " .. tostring(bounded.REPLY_BUDGET))

local _ = ship
print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
