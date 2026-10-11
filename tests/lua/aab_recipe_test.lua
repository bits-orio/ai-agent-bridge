-- AI Agent Bridge - tests/lua/aab_recipe_test.lua
-- Author: bits-orio
-- License: MIT
--
-- recipe_needs (docs/design/recipe-needs-spec.md, part 1), driven through
-- the aab-rpc call op against the fake game's recipe block
-- (tests/lua/fakegame.lua, F.install_recipes): vanilla 2.0.77 recipes for the
-- red, green and blue science trees, the oil and uranium recipes, crafting
-- machines and belts. The "player" force has every recipe; "team-3" has only
-- what a fresh freeplay force starts with.
--
-- Every figure below is worked out by hand from the game's own data, never
-- read back from the tool. The oracle per pack, no productivity research:
--   red   2 iron plate + 1 copper plate
--   green 5.5 iron plate + 1.5 copper plate
--   blue  12 iron plate + 7.5 copper plate + 3 plastic bar + 0.5 sulfur

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
  return F.decode(S.rcon_replies[#S.rcon_replies]), S.rcon_replies[#S.rcon_replies]
end
local function needs(args)
  return rpc({ op = "call", i = "ai-agent-bridge-tools", f = "recipe_needs", a = args })
end

--- The row (or raw row, or target) for one item, or nil.
local function find(list, item)
  for _, row in ipairs(list or {}) do
    if row.item == item then return row end
  end
  return nil
end
--- A wire figure equals `want`: whole numbers leave as numbers, fractions as
--- short decimal strings, so both forms are read through tonumber.
local function is(value, want)
  local n = tonumber(value)
  return n ~= nil and math.abs(n - want) < 1e-9
end
local function per_min(reply, item, list)
  local row = find(reply.r and reply.r[list or "rows"], item)
  return row and tonumber(row.per_min)
end
local function has(list, word)
  for _, one in ipairs(list or {}) do
    if one == word then return true end
  end
  return false
end
--- The alternatives a reply names for one item, nil when it names none. A
--- reply with no alternatives at all leaves the map out.
local function alternatives_for(reply, item)
  return reply.ok and (reply.r.alternatives or {})[item] or nil
end
--- No list or map in the raw reply came out empty: the engine's writer would
--- print one as {}, the fake's as [].
local function nothing_empty(raw)
  return raw:find("{}", 1, true) == nil and raw:find("[]", 1, true) == nil
end

local RED, GREEN, BLUE = "automation-science-pack", "logistic-science-pack", "chemical-science-pack"

-- ── the manifest ──────────────────────────────────────────────────────
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
local entry = mine.ok and mine.r.tools.recipe_needs or nil
check("recipe_needs is in the companion's manifest", entry ~= nil and type(entry.desc) == "string", F.encode(mine.r and mine.r.tools and mine.r.tools.recipe_needs))
check("the desc carries the routing rule", entry and entry.desc:find("Never work a ratio out yourself", 1, true) ~= nil
      and entry.desc:find("per_min", 1, true) ~= nil and entry.desc:find("machines", 1, true) ~= nil, entry and entry.desc)
check("the desc has no em-dash", entry and entry.desc:find("\226\128\148", 1, true) == nil, entry and entry.desc)
-- Measured with DeepSeek V4 Pro on a live 2.0.77 rig: with the rule alone,
-- "I have 48 blue science" went out as per_min in 4 of 4 tries; with this
-- example beside it, as machines in 3 of 3.
check("the desc shows a bare count going in as machines", entry
      and entry.desc:find("\"I have 48 blue science\" is machines=[48], not per_min=[48]", 1, true) ~= nil, entry and entry.desc)
check("recipe_needs declares tier 1: it reads prototypes, never the map", entry and entry.tier == "1", entry and entry.tier)
-- The probe grammar (service/internal/catalog/params.go): "<type>[!] desc",
-- list<...> as one type word, a trailing ! for required.
local WANT_PARAMS = {
  items = "list<string>!", per_min = "list<number>", machines = "list<number>",
  machine = "string", belt = "string", stop_at = "list<string>", surface = "string",
}
for name, want in pairs(WANT_PARAMS) do
  local spec = entry and entry.params and entry.params[name]
  local word = spec and spec:match("^(%S+) ")
  check("recipe_needs." .. name .. " is declared " .. want, word == want, spec)
end
local declared = 0
for name in pairs((entry or {}).params or {}) do
  declared = declared + 1
  check("recipe_needs does not declare force", name ~= "force", name)
end
check("recipe_needs declares exactly its seven params", declared == 7, declared)
check("the surface param asks for the asker's surface and says what it decides",
      entry and entry.params and tostring(entry.params.surface):find("asker's surface", 1, true) ~= nil
      and tostring(entry.params.surface):find("mines or pumps", 1, true) ~= nil, entry and entry.params and entry.params.surface)
check("the desc says machine productivity is counted", entry
      and entry.desc:find("research and machine productivity", 1, true) ~= nil, entry and entry.desc)
check("the manifest still fits one reply with room to spare", #F.encode(mine.r) < 16384, #F.encode(mine.r))

-- ── the fake holds the tool to the 2.1 API ────────────────────────────
-- 2.1.17 removes LuaRecipePrototype::category, ::additional_categories and
-- ::is_parameter, and LuaRecipe::category. The fake raises on all of them, so
-- every passing check below also proves none of them was read.
for _, key in ipairs({ "category", "additional_categories", "is_parameter" }) do
  local read = pcall(function() return prototypes.recipe["iron-plate"][key] end)
  check("the fake recipe prototype raises on " .. key, read == false)
end
check("the fake LuaRecipe raises on category",
      pcall(function() return S.force.recipes["iron-plate"].category end) == false)
check("the fake filter result is a LuaCustomTable: next() sees nothing, pairs sees the recipe",
      (function()
        local t = prototypes.get_recipe_filtered({ { filter = "has-product-item", elem_filters = { { filter = "name", name = "iron-plate" } } } })
        local walked = 0
        for _ in pairs(t) do walked = walked + 1 end
        return next(t) == nil and walked == 1
      end)())

-- ── the oracle: one pack at a time, 60 a minute ───────────────────────
local red, red_raw = needs({ force = "player", items = { RED }, per_min = { 60 } })
check("red science answers", red.ok and red.r.found == true and red.r.mode == "per_min", F.encode(red))
check("oracle red: 2 iron plate a pack", per_min(red, "iron-plate") == 120, red.ok and F.encode(red.r.rows))
check("oracle red: 1 copper plate a pack", per_min(red, "copper-plate") == 60, red.ok and F.encode(red.r.rows))
check("red walks down to ore", per_min(red, "iron-ore", "raw") == 120 and per_min(red, "copper-ore", "raw") == 60,
      red.ok and F.encode(red.r.raw))
check("red science is exact: approx is left out, never sent empty", red.ok and red.r.approx == nil
      and red_raw:find('"approx"', 1, true) == nil, red_raw)
check("nor are its empty alternatives: no list or map in the reply is empty",
      red.ok and red.r.alternatives == nil and nothing_empty(red_raw), red_raw)
check("red reports research productivity only", red.ok and red.r.productivity == "research")
check("the target row is in rows too, with its recipe",
      red.ok and find(red.r.rows, RED) ~= nil and find(red.r.rows, RED).recipe == RED, red.ok and F.encode(red.r.rows))

local green = needs({ force = "player", items = { GREEN }, per_min = { 60 } })
check("oracle green: 5.5 iron plate a pack", per_min(green, "iron-plate") == 330, green.ok and F.encode(green.r.rows))
check("oracle green: 1.5 copper plate a pack", per_min(green, "copper-plate") == 90, green.ok and F.encode(green.r.rows))
check("green: two belts a craft means 30 belt crafts for 60 packs, 30 more iron plate",
      per_min(green, "transport-belt") == 60, green.ok and F.encode(find(green.r.rows, "transport-belt")))

local blue = needs({ force = "player", items = { BLUE }, per_min = { 60 } })
check("oracle blue: 12 iron plate a pack", per_min(blue, "iron-plate") == 720, blue.ok and F.encode(blue.r.rows))
check("oracle blue: 7.5 copper plate a pack", per_min(blue, "copper-plate") == 450, blue.ok and F.encode(blue.r.rows))
check("oracle blue: 3 plastic bar a pack", per_min(blue, "plastic-bar") == 180, blue.ok and F.encode(blue.r.rows))
check("oracle blue: 0.5 sulfur a pack", per_min(blue, "sulfur") == 30, blue.ok and F.encode(blue.r.rows))
check("blue: 2 packs a craft, so 30 crafts make 60",
      blue.ok and is(find(blue.r.rows, BLUE).machines, 30 * 24 / (1.25 * 60)), blue.ok and F.encode(find(blue.r.rows, BLUE)))

-- ── three targets in one call, summed into shared rows ────────────────
local three = needs({ force = "player", items = { RED, GREEN, BLUE }, per_min = { 60, 60, 60 } })
check("three targets sum their iron plate", per_min(three, "iron-plate") == 120 + 330 + 720, three.ok and F.encode(three.r.rows))
check("three targets sum their copper plate", per_min(three, "copper-plate") == 60 + 90 + 450, three.ok and F.encode(three.r.rows))
check("gears are one shared row: 60 red, 90 green, 60 blue", per_min(three, "iron-gear-wheel") == 210,
      three.ok and F.encode(find(three.r.rows, "iron-gear-wheel")))
check("each target is listed once, in the order asked",
      three.ok and #three.r.targets == 3 and three.r.targets[1].item == RED and three.r.targets[2].item == GREEN
      and three.r.targets[3].item == BLUE, three.ok and F.encode(three.r.targets))
check("each item is one row however many paths reach it", (function()
  local seen = {}
  for _, row in ipairs(three.r.rows) do
    if seen[row.item] then return false end
    seen[row.item] = true
  end
  return true
end)(), three.ok and F.encode(three.r.rows))
check("total and shown sit beside the rows", three.ok and three.r.total == #three.r.rows and three.r.shown == three.r.total,
      three.ok and (three.r.total .. " / " .. three.r.shown))
local _, three_raw = needs({ force = "player", items = { RED, GREEN, BLUE }, per_min = { 60, 60, 60 } })
check("Pirion's three packs fit one reply under the tool's own budget (" .. #three_raw .. " bytes)",
      #three_raw <= 4000 + 32, #three_raw)

-- ── the oil tree: fluids, byproducts and alternatives ─────────────────
local gas = find(blue.r.rows, "petroleum-gas")
check("petroleum gas is picked by largest net output: advanced oil processing's 55",
      gas ~= nil and gas.recipe == "advanced-oil-processing", F.encode(gas))
check("a multi-product pick puts byproducts in approx", blue.ok and has(blue.r.approx, "byproducts"), blue.ok and F.encode(blue.r.approx))
check("a fluid row says fluid and counts no belts", gas ~= nil and gas.fluid == true and gas.belts == nil, F.encode(gas))
check("blue's gas: 1800 for plastic, 450 for sulfur", gas ~= nil and is(gas.per_min, 2250), F.encode(gas))
check("crude oil and water are raw, as fluids",
      blue.ok and find(blue.r.raw, "crude-oil") and find(blue.r.raw, "crude-oil").fluid == true
      and is(find(blue.r.raw, "crude-oil").per_min, 4090.91) and is(find(blue.r.raw, "water").per_min, 2495.45),
      blue.ok and F.encode(blue.r.raw))
check("the recipes not picked are named as alternatives, best first",
      blue.ok and alternatives_for(blue, "petroleum-gas") ~= nil and alternatives_for(blue, "petroleum-gas")[1] == "basic-oil-processing"
      and alternatives_for(blue, "petroleum-gas")[2] == "light-oil-cracking", blue.ok and F.encode(blue.r.alternatives))
local gas_target = needs({ force = "player", items = { "petroleum-gas" }, per_min = { 110 } })
check("a fluid can be a target: 110 gas is 2 crafts of advanced oil processing",
      gas_target.ok and gas_target.r.targets[1].fluid == true and is(gas_target.r.targets[1].per_min, 110)
      and per_min(gas_target, "crude-oil", "raw") == 200 and per_min(gas_target, "water", "raw") == 100,
      gas_target.ok and F.encode(gas_target.r))

-- ── barrels: a round trip is never the way to make a fluid ────────────
-- The player force has fluid handling's barrel recipes, as every force past
-- early oil does. Emptying a barrel nets 50 water; walked, it would fill that
-- barrel from water and charge a new steel barrel each time. On the live
-- 2.0.77 engine that took blue science from 720 iron plate a minute to
-- 1378.64. The oracle above is the first guard; these say why.
check("water stays raw though a barrel could be emptied for it", blue.ok and find(blue.r.rows, "water") == nil
      and per_min(blue, "water", "raw") ~= nil and find(blue.r.raw, "water").cycle == nil, blue.ok and F.encode(blue.r.raw))
check("no barrel is charged to blue science", blue.ok and find(blue.r.rows, "barrel") == nil
      and find(blue.r.rows, "water-barrel") == nil and find(blue.r.rows, "crude-oil-barrel") == nil,
      blue.ok and F.encode(blue.r.rows))
check("an emptying recipe is not even an alternative for petroleum gas",
      blue.ok and not has(alternatives_for(blue, "petroleum-gas"), "empty-petroleum-gas-barrel"), blue.ok and F.encode(blue.r.alternatives))
local filled = needs({ force = "player", items = { "water-barrel" }, per_min = { 60 } })
check("asking for water barrels still fills them: the item's own recipe is never a round trip",
      filled.ok and find(filled.r.rows, "water-barrel") ~= nil and find(filled.r.rows, "water-barrel").recipe == "water-barrel"
      and per_min(filled, "water", "raw") == 3000 and per_min(filled, "barrel") == 60 and per_min(filled, "iron-plate") == 300,
      filled.ok and F.encode(filled.r))
local barrels = needs({ force = "player", items = { "barrel" }, per_min = { 60 } })
check("emptying is not an alternative way to make an empty barrel either",
      barrels.ok and find(barrels.r.rows, "barrel").recipe == "barrel" and alternatives_for(barrels, "barrel") == nil,
      barrels.ok and F.encode(barrels.r.alternatives))
local barrel_recipes = {}
for _, fluid_name in ipairs({ "water", "crude-oil", "petroleum-gas" }) do
  barrel_recipes[#barrel_recipes + 1] = fluid_name .. "-barrel"
  barrel_recipes[#barrel_recipes + 1] = "empty-" .. fluid_name .. "-barrel"
end
for _, name in ipairs(barrel_recipes) do S.set_recipe("player", name, { enabled = false }) end
local unbarrelled = needs({ force = "player", items = { BLUE }, per_min = { 60 } })
check("without fluid handling the oracle is the same", per_min(unbarrelled, "iron-plate") == 720
      and per_min(unbarrelled, "water", "raw") == per_min(blue, "water", "raw"), unbarrelled.ok and F.encode(unbarrelled.r.raw))
check("and water is not called locked because a barrel recipe could make it",
      unbarrelled.ok and find(unbarrelled.r.raw, "water").recipe_locked == nil, unbarrelled.ok and F.encode(unbarrelled.r.raw))
for _, name in ipairs(barrel_recipes) do S.set_recipe("player", name, { enabled = true }) end

-- ── a disabled recipe is never picked ─────────────────────────────────
S.set_recipe("player", "advanced-oil-processing", { enabled = false })
local basic = needs({ force = "player", items = { BLUE }, per_min = { 60 } })
check("with advanced oil processing disabled, basic oil processing is picked",
      basic.ok and find(basic.r.rows, "petroleum-gas").recipe == "basic-oil-processing", basic.ok and F.encode(find(basic.r.rows, "petroleum-gas")))
check("a disabled recipe is not even an alternative",
      basic.ok and not has(alternatives_for(basic, "petroleum-gas"), "advanced-oil-processing"), basic.ok and F.encode(basic.r.alternatives))
check("a single-product oil tree is exact: no byproducts", basic.ok and not has(basic.r.approx, "byproducts"), basic.ok and F.encode(basic.r.approx))
check("crude oil follows the 45 gas a craft: 2250 gas is 50 crafts of 100", basic.ok and per_min(basic, "crude-oil", "raw") == 5000,
      basic.ok and F.encode(basic.r.raw))
S.set_recipe("player", "advanced-oil-processing", { enabled = true })

-- ── a fresh freeplay force: what is locked and what can be built ──────
local fresh_locked = needs({ force = "team-3", items = { RED }, per_min = { 60 } })
check("an item whose only recipe is disabled is raw", fresh_locked.ok and fresh_locked.r.total == 0
      and per_min(fresh_locked, RED, "raw") == 60, fresh_locked.ok and F.encode(fresh_locked.r))
check("and says it is locked, not missing", fresh_locked.ok and find(fresh_locked.r.raw, RED).recipe_locked == true,
      fresh_locked.ok and F.encode(fresh_locked.r.raw))
check("the target says raw", fresh_locked.ok and fresh_locked.r.targets[1].raw == true, fresh_locked.ok and F.encode(fresh_locked.r.targets))
check("and the target says locked too, as its raw row does", fresh_locked.ok and fresh_locked.r.targets[1].recipe_locked == true,
      fresh_locked.ok and F.encode(fresh_locked.r.targets))
check("ore with no recipe at all is raw and not locked", per_min(red, "iron-ore", "raw") == 120
      and find(red.r.raw, "iron-ore").recipe_locked == nil, red.ok and F.encode(red.r.raw))

S.set_recipe("team-3", RED, { enabled = true })
local fresh = needs({ force = "team-3", items = { RED }, per_min = { 60 } })
check("fresh force: same oracle once the pack recipe is enabled", per_min(fresh, "iron-plate") == 120
      and per_min(fresh, "copper-plate") == 60, fresh.ok and F.encode(fresh.r.rows))
check("fresh force: no assembler can be built, so the fastest is named and unlocked=false",
      fresh.ok and find(fresh.r.rows, RED).machine == "assembling-machine-3" and find(fresh.r.rows, RED).unlocked == false
      and is(find(fresh.r.rows, RED).machines, 4), fresh.ok and F.encode(find(fresh.r.rows, RED)))
check("fresh force: the stone furnace is buildable and beats faster furnaces it cannot build",
      fresh.ok and find(fresh.r.rows, "iron-plate").machine == "stone-furnace" and find(fresh.r.rows, "iron-plate").unlocked == nil
      and is(find(fresh.r.rows, "iron-plate").machines, 6.4), fresh.ok and F.encode(find(fresh.r.rows, "iron-plate")))
check("fresh force: the yellow belt is the fastest it can build", fresh.ok and fresh.r.belt.name == "transport-belt"
      and fresh.r.belt.per_sec == 15 and fresh.r.belt.unlocked == nil, fresh.ok and F.encode(fresh.r.belt))
check("fresh force: the target names its machine and says it is locked",
      fresh.ok and fresh.r.targets[1].machine == "assembling-machine-3" and fresh.r.targets[1].unlocked == false,
      fresh.ok and F.encode(fresh.r.targets))
S.set_recipe("team-3", "transport-belt", { enabled = false })
local no_belt = needs({ force = "team-3", items = { RED }, per_min = { 60 } })
check("a force that can build no belt still counts in the fastest, unlocked=false",
      no_belt.ok and no_belt.r.belt.name == "express-transport-belt" and no_belt.r.belt.unlocked == false,
      no_belt.ok and F.encode(no_belt.r.belt))
S.set_recipe("team-3", "transport-belt", { enabled = true })
S.set_recipe("team-3", RED, { enabled = false })

-- ── machines: has_category, the fastest buildable, the machine param ──
check("smelting goes to a furnace: the fastest, electric before steel by name",
      blue.ok and find(blue.r.rows, "iron-plate").machine == "electric-furnace" and find(blue.r.rows, "steel-plate").machine == "electric-furnace",
      blue.ok and F.encode(find(blue.r.rows, "iron-plate")))
check("chemistry goes to the chemical plant", blue.ok and find(blue.r.rows, "plastic-bar").machine == "chemical-plant"
      and find(blue.r.rows, "sulfur").machine == "chemical-plant", blue.ok and F.encode(find(blue.r.rows, "plastic-bar")))
check("advanced-crafting goes to an assembler", blue.ok and find(blue.r.rows, "engine-unit").machine == "assembling-machine-3",
      blue.ok and F.encode(find(blue.r.rows, "engine-unit")))
-- 2250 gas at 55 a craft is 40.9 crafts a minute of a 5 s recipe at speed 1:
-- 3.409 refineries, two decimals on the wire.
check("oil processing goes to the refinery", gas ~= nil and gas.machine == "oil-refinery" and is(gas.machines, 3.41),
      F.encode(gas))
check("machines = crafts x energy / (speed x 60): 3.2 s plates, 120 a minute, speed 2",
      is(find(red.r.rows, "iron-plate").machines, 120 * 3.2 / (2 * 60)), F.encode(find(red.r.rows, "iron-plate")))
local u235 = needs({ force = "player", items = { "uranium-235" }, per_min = { 1 } })
check("a recipe no crafting machine can make has no machine column", u235.ok and find(u235.r.rows, "uranium-235").machine == nil
      and find(u235.r.rows, "uranium-235").machines == nil, u235.ok and F.encode(u235.r.rows))

local am1 = needs({ force = "player", items = { RED }, per_min = { 60 }, machine = "assembling-machine-1" })
check("machine= wins for every recipe it can make", am1.ok and find(am1.r.rows, RED).machine == "assembling-machine-1"
      and is(find(am1.r.rows, RED).machines, 60 * 5 / (0.5 * 60)) and find(am1.r.rows, "iron-gear-wheel").machine == "assembling-machine-1",
      am1.ok and F.encode(am1.r.rows))
check("and leaves the recipes it cannot make alone", am1.ok and find(am1.r.rows, "iron-plate").machine == "electric-furnace",
      am1.ok and F.encode(find(am1.r.rows, "iron-plate")))
local bad_machine = needs({ force = "player", items = { RED }, per_min = { 60 }, machine = "lab" })
check("a machine that is not a crafting machine is an argument error", (not bad_machine.ok) and bad_machine.e == "provider_error"
      and tostring(bad_machine.m):find("crafting machine", 1, true) ~= nil, F.encode(bad_machine))

-- ── belts ─────────────────────────────────────────────────────────────
check("the default belt is the fastest the force can build", red.ok and red.r.belt.name == "express-transport-belt"
      and red.r.belt.per_sec == 45, red.ok and F.encode(red.r.belt))
check("belts = per minute / 60 / (belt_speed x 480)", is(find(red.r.rows, "iron-plate").belts, 0.044),
      F.encode(find(red.r.rows, "iron-plate")))
local yellow = needs({ force = "player", items = { RED }, per_min = { 60 }, belt = "transport-belt" })
check("belt= counts in that belt: 120 plates a minute is 0.133 of a 15 a second belt",
      yellow.ok and yellow.r.belt.name == "transport-belt" and yellow.r.belt.per_sec == 15
      and is(find(yellow.r.rows, "iron-plate").belts, 0.133) and is(find(yellow.r.raw, "iron-ore").belts, 0.133),
      yellow.ok and F.encode(yellow.r))
local bad_belt = needs({ force = "player", items = { RED }, per_min = { 60 }, belt = "assembling-machine-2" })
check("a belt that is not a transport belt is an argument error", (not bad_belt.ok)
      and tostring(bad_belt.m):find("transport belt", 1, true) ~= nil, F.encode(bad_belt))

-- ── machines mode ─────────────────────────────────────────────────────
-- 4 assembling-machine-3 at speed 1.25 on a 5 s recipe make 60 a minute.
local by_count = needs({ force = "player", items = { RED }, machines = { 4 } })
check("machines mode answers", by_count.ok and by_count.r.mode == "machines", F.encode(by_count))
check("machines mode gives the same tree as the equivalent per_min", by_count.ok and red.ok
      and F.encode(by_count.r.rows) == F.encode(red.r.rows) and F.encode(by_count.r.raw) == F.encode(red.r.raw),
      by_count.ok and F.encode(by_count.r.rows))
check("machines mode reports the rate it worked out and the count it was given",
      by_count.ok and is(by_count.r.targets[1].per_min, 60) and is(by_count.r.targets[1].machines, 4)
      and by_count.r.targets[1].machine == "assembling-machine-3", by_count.ok and F.encode(by_count.r.targets))
-- Pirion's question, read as machines: 48 blue, 24 red, 24 green.
local pirion = needs({ force = "player", items = { BLUE, RED, GREEN }, machines = { 48, 24, 24 } })
local blue_rate = 48 * 1.25 * 60 / 24 * 2
local red_rate = 24 * 1.25 * 60 / 5
local green_rate = 24 * 1.25 * 60 / 6
check("Pirion's machines become rates: 300 blue, 360 red, 300 green a minute",
      pirion.ok and is(pirion.r.targets[1].per_min, blue_rate) and is(pirion.r.targets[2].per_min, red_rate)
      and is(pirion.r.targets[3].per_min, green_rate), pirion.ok and F.encode(pirion.r.targets))
check("and the plates follow the oracle at those rates",
      per_min(pirion, "iron-plate") == blue_rate * 12 + red_rate * 2 + green_rate * 5.5
      and per_min(pirion, "copper-plate") == blue_rate * 7.5 + red_rate * 1 + green_rate * 1.5,
      pirion.ok and F.encode(find(pirion.r.rows, "iron-plate")))
local fresh_count, fresh_count_raw = needs({ force = "team-3", items = { RED }, machines = { 4 } })
check("a machine count for an item with no usable recipe cannot become a rate, and says why",
      fresh_count.ok and is(fresh_count.r.targets[1].per_min, 0) and is(fresh_count.r.targets[1].machines, 4)
      and type(fresh_count.r.targets[1].reason) == "string" and fresh_count.r.total == 0 and fresh_count.r.raw == nil,
      fresh_count.ok and F.encode(fresh_count.r))
check("with no row at all, rows is left out too, total and shown still 0",
      fresh_count.ok and fresh_count.r.rows == nil and fresh_count.r.shown == 0 and nothing_empty(fresh_count_raw),
      fresh_count_raw)
local u_count = needs({ force = "player", items = { "uranium-235" }, machines = { 2 } })
check("a machine count for a recipe no machine makes says why",
      u_count.ok and is(u_count.r.targets[1].per_min, 0) and tostring(u_count.r.targets[1].reason):find("machine", 1, true) ~= nil,
      u_count.ok and F.encode(u_count.r.targets))

-- ── stop_at ───────────────────────────────────────────────────────────
local stopped = needs({ force = "player", items = { RED }, per_min = { 60 }, stop_at = { "iron-plate" } })
check("stop_at keeps the row with its rate and belts", stopped.ok and find(stopped.r.rows, "iron-plate") ~= nil
      and per_min(stopped, "iron-plate") == 120 and find(stopped.r.rows, "iron-plate").stop == true
      and is(find(stopped.r.rows, "iron-plate").belts, 0.044), stopped.ok and F.encode(stopped.r.rows))
check("stop_at stops expansion: no iron ore, still copper ore", stopped.ok and find(stopped.r.raw, "iron-ore") == nil
      and per_min(stopped, "copper-ore", "raw") == 60, stopped.ok and F.encode(stopped.r.raw))
check("a stopped row names no recipe or machine", stopped.ok and find(stopped.r.rows, "iron-plate").recipe == nil
      and find(stopped.r.rows, "iron-plate").machine == nil, stopped.ok and F.encode(find(stopped.r.rows, "iron-plate")))
local stop_target, stop_target_raw = needs({ force = "player", items = { "iron-plate" }, per_min = { 30 }, stop_at = { "iron-plate" } })
check("a target in stop_at is a stopped row and a stopped target",
      stop_target.ok and stop_target.r.targets[1].stop == true and find(stop_target.r.rows, "iron-plate").stop == true
      and stop_target.r.raw == nil, stop_target.ok and F.encode(stop_target.r))
check("with nothing raw, rough or alternative, raw, approx and alternatives are all left out",
      stop_target.ok and stop_target.r.raw == nil and stop_target.r.approx == nil and stop_target.r.alternatives == nil
      and nothing_empty(stop_target_raw), stop_target_raw)

-- ── the catalyst: kovarex nets one ────────────────────────────────────
check("uranium-235 is made by kovarex, ranked above processing by net output",
      u235.ok and find(u235.r.rows, "uranium-235").recipe == "kovarex-enrichment-process", u235.ok and F.encode(u235.r.rows))
check("kovarex nets 1 a craft: 1 a minute takes 3 uranium-238 a minute (5 in, 2 back)",
      per_min(u235, "uranium-238") == 3, u235.ok and F.encode(u235.r.rows))
check("the 40 it hands back are not a demand: uranium-235 is no input of itself",
      u235.ok and find(u235.r.raw, "uranium-235") == nil and not has(u235.r.approx, "cycle"), u235.ok and F.encode(u235.r))
check("uranium-238 comes from processing at 0.993 a craft: 30.21 ore a minute",
      u235.ok and find(u235.r.rows, "uranium-238").recipe == "uranium-processing"
      and is(per_min(u235, "uranium-ore", "raw"), 30.21), u235.ok and F.encode(u235.r.raw))

-- ── productivity: research bonus, capped ──────────────────────────────
S.set_recipe("player", "iron-gear-wheel", { productivity_bonus = 0.25 })
local prod = needs({ force = "player", items = { RED }, per_min = { 60 } })
check("a research productivity bonus is applied: 1.25 gears a craft, 96 plates",
      per_min(prod, "iron-plate") == 96 and per_min(prod, "iron-gear-wheel") == 60, prod.ok and F.encode(prod.r.rows))
S.set_recipe("player", "iron-gear-wheel", { productivity_bonus = 5 })
local capped_prod = needs({ force = "player", items = { RED }, per_min = { 60 } })
check("the bonus is capped at maximum_productivity: +300%, 4 gears a craft, 30 plates",
      per_min(capped_prod, "iron-plate") == 30, capped_prod.ok and F.encode(capped_prod.r.rows))
-- A script may write any EffectValue; the engine's productivity sum never
-- goes below 0% (prototype docs, Effect::productivity).
S.set_recipe("player", "iron-gear-wheel", { productivity_bonus = -0.5 })
local negative = needs({ force = "player", items = { RED }, per_min = { 60 } })
check("a negative bonus counts as none: 1 gear a craft, 120 plates",
      per_min(negative, "iron-plate") == 120, negative.ok and F.encode(negative.r.rows))
S.set_recipe("player", "iron-gear-wheel", { productivity_bonus = 0 })

-- ── synthetic recipes for the rules vanilla does not exercise ─────────
local function product(name, amount, extra)
  local p = { type = "item", name = name, amount = amount, probability = 1 }
  for key, value in pairs(extra or {}) do p[key] = value end
  return p
end
local function input(name, amount) return { type = "item", name = name, amount = amount } end

-- ignored_by_productivity above the expected amount is clamped: one craft
-- gives 0.5 on average, all of it skipped by productivity, never "1 skipped".
S.add_recipe({ name = "test-clamp-item", ingredients = { input("iron-plate", 1) },
               products = { product("test-clamp-item", 1, { probability = 0.5, ignored_by_productivity = 1 }) } })
S.set_recipe("player", "test-clamp-item", { productivity_bonus = 0.5 })
local clamp = needs({ force = "player", items = { "test-clamp-item" }, per_min = { 60 } })
check("ignored_by_productivity above the expected amount is clamped: 0.5 a craft, 120 plates",
      per_min(clamp, "iron-plate") == 120, clamp.ok and F.encode(clamp.r.rows))

-- 2.1's product shape: no probability, independent_probability 0.5 and a
-- shared roll window of 0.5, so 4 a roll is 1 a craft.
S.add_recipe({ name = "test-shared-chance", ingredients = { input("iron-plate", 1) },
               products = { { type = "item", name = "test-chance-item", amount = 4,
                              independent_probability = 0.5, shared_probability = { min = 0.25, max = 0.75 } } } })
local shared = needs({ force = "player", items = { "test-chance-item" }, per_min = { 60 } })
check("a 2.1 product's chance is independent x (max - min): 1 a craft, 60 plates",
      per_min(shared, "iron-plate") == 60, shared.ok and F.encode(shared.r.rows))

-- amount_min and amount_max, a chance and extra_count_fraction together. The
-- chance gates the extra count too, as the engine counts it (2.1.17
-- get_product_amount, and real crafting on 2.0.77 and 2.1.17): the mean of 1
-- to 3 is 2, plus 0.25, times 0.5, is 1.125 a craft, not 2 x 0.5 + 0.25 =
-- 1.25. 60 a minute is 53.33 crafts of one plate each.
S.add_recipe({ name = "test-range", ingredients = { input("iron-plate", 1) },
               products = { { type = "item", name = "test-range-item", amount_min = 1, amount_max = 3,
                              probability = 0.5, extra_count_fraction = 0.25 } } })
local range = needs({ force = "player", items = { "test-range-item" }, per_min = { 60 } })
check("a range under a chance with an extra count: (2 + 0.25) x 0.5 = 1.125 a craft, 53.33 plates",
      is(per_min(range, "iron-plate"), 53.33), range.ok and F.encode(range.r.rows))
-- A max below the min is read as the min (prototype docs, amount_max): 3 a craft.
S.add_recipe({ name = "test-inverted", ingredients = { input("iron-plate", 1) },
               products = { { type = "item", name = "test-inverted-item", amount_min = 3, amount_max = 1, probability = 1 } } })
local inverted = needs({ force = "player", items = { "test-inverted-item" }, per_min = { 60 } })
check("an amount_max below amount_min reads as amount_min: 3 a craft, 20 plates",
      per_min(inverted, "iron-plate") == 20, inverted.ok and F.encode(inverted.r.rows))

-- ── the product formula, one shape at a time ──────────────────────────
-- One craft gives c x mean over the roll of ((r + e) + p x max(r + e - g, 0)),
-- c the chance, r the roll, e extra_count_fraction, g ignored_by_productivity,
-- p the bonus: what 2.1.17's get_product_amount and real crafting on 2.0.77
-- and 2.1.17 give. Each shape below is one where reading the plain expected
-- amount gets a different figure; every value is worked by hand.
local yield = require("scripts.tools.recipe_yield")
local function one_craft(fields, bonus)
  local made = { type = "item", name = "test-formula-item" }
  for key, value in pairs(fields) do made[key] = value end
  return yield.output(made, bonus)
end
local SHAPE_A = { amount = 10, probability = 0.5, ignored_by_productivity = 4 }
local SHAPE_B = { amount = 3, probability = 0.5, extra_count_fraction = 0.5 }
local SHAPE_C = { amount_min = 0, amount_max = 3, ignored_by_productivity = 2 }
-- A: 10 at a half chance, 4 skipped. The skip comes off the roll before the
-- chance: 0.5 x (10 + 1 x (10 - 4)) = 8 at +100%, 0.5 x (10 + 0.5 x 6) = 6.5
-- at +50%. Off the expected 5 it would be 4 + 1 x 2 = 6 and 4 + 1 x 1.5 = 5.5.
check("shape A, a chance with a part skipped: 8 a craft at +100%", one_craft(SHAPE_A, 1) == 8, one_craft(SHAPE_A, 1))
check("shape A at +50%: 6.5 a craft", one_craft(SHAPE_A, 0.5) == 6.5, one_craft(SHAPE_A, 0.5))
check("shape A with no bonus is the plain 5", one_craft(SHAPE_A, 0) == 5, one_craft(SHAPE_A, 0))
-- B: 3 at a half chance with half an extra. The chance gates the extra too:
-- 0.5 x (3 + 0.5) = 1.75, not 3 x 0.5 + 0.5 = 2; at +50% the extra is
-- boosted with the roll, 0.5 x (3.5 + 0.5 x 3.5) = 2.625.
check("shape B, a chance with an extra count: 1.75 a craft", one_craft(SHAPE_B, 0) == 1.75, one_craft(SHAPE_B, 0))
check("shape B at +50%: 2.625 a craft", one_craft(SHAPE_B, 0.5) == 2.625, one_craft(SHAPE_B, 0.5))
-- C: 0 to 3 with 2 skipped. An item rolls 0, 1, 2 or 3 alike: mean 1.5, and
-- the part above 2 is 0, 0, 0 or 1, mean 0.25. At +50%: 1.5 + 0.5 x 0.25 =
-- 1.625. Off the mean, 2 would swallow all 1.5 and leave 1.5.
check("shape C, a range with a skip: 1.625 a craft at +50%", one_craft(SHAPE_C, 0.5) == 1.625, one_craft(SHAPE_C, 0.5))
-- C with half an extra: the part above 2 of 0.5, 1.5, 2.5, 3.5 is 0, 0, 0.5,
-- 1.5, mean 0.5. At +100%: 1.5 + 0.5 + 0.5 = 2.5.
check("shape C with an extra count: 2.5 a craft at +100%",
      one_craft({ amount_min = 0, amount_max = 3, extra_count_fraction = 0.5, ignored_by_productivity = 2 }, 1) == 2.5)
-- A fluid rolls any amount over its span. 0 to 30 with 10 skipped: the part
-- above 10 averages 20 x 20 / 2 / 30 = 20/3. At +50%: 15 + 10/3 = 18.333.
local fluid_range = one_craft({ type = "fluid", amount_min = 0, amount_max = 30, ignored_by_productivity = 10 }, 0.5)
check("a fluid range rolls evenly over its span: 18.333 a craft at +50%", math.abs(fluid_range - (15 + 10 / 3)) < 1e-9, fluid_range)
-- Through the walk: shape A at +100% is 8 a craft, so 60 a minute is 7.5
-- crafts and 7.5 iron plate. Off the expected amount it would be 10.
S.add_recipe({ name = "test-chance-skip", ingredients = { input("iron-plate", 1) },
               products = { product("test-chance-skip", 10, { probability = 0.5, ignored_by_productivity = 4 }) } })
S.set_recipe("player", "test-chance-skip", { productivity_bonus = 1 })
local chance_skip = needs({ force = "player", items = { "test-chance-skip" }, per_min = { 60 } })
check("the walk counts shape A as the engine does: 7.5 iron plate for 60 a minute",
      is(per_min(chance_skip, "iron-plate"), 7.5), chance_skip.ok and F.encode(chance_skip.r.rows))

-- A research-progress product is skipped, not a crash.
local progress = needs({ force = "player", items = { "test-progress-item" }, per_min = { 60 } })
check("a research-progress product is skipped, not a crash", progress.ok and progress.r.found == true
      and per_min(progress, "iron-plate") == 60 and progress.r.approx == nil, F.encode(progress))

-- Ranking rule 1, the recipe named after the item, beats a larger net output.
S.add_recipe({ name = "test-gear-casting", ingredients = { input("iron-plate", 3) },
               products = { product("iron-gear-wheel", 2) } })
local named = needs({ force = "player", items = { "iron-gear-wheel" }, per_min = { 60 } })
check("the recipe named after the item wins over a larger net output",
      named.ok and find(named.r.rows, "iron-gear-wheel").recipe == "iron-gear-wheel"
      and per_min(named, "iron-plate") == 120, named.ok and F.encode(named.r.rows))
check("and the other is its alternative", named.ok and alternatives_for(named, "iron-gear-wheel") ~= nil
      and alternatives_for(named, "iron-gear-wheel")[1] == "test-gear-casting", named.ok and F.encode(named.r.alternatives))

-- Rule 2, main_product, beats a larger net output from a recipe that only
-- makes the item on the side.
S.add_recipe({ name = "test-main-side", ingredients = { input("copper-plate", 1) },
               products = { product("test-main-item", 10), product("test-side-item", 1) } })
S.add_recipe({ name = "test-main-own", ingredients = { input("iron-plate", 1) }, main_product = "test-main-item",
               products = { product("test-main-item", 1), product("test-side-item", 1) } })
local main = needs({ force = "player", items = { "test-main-item" }, per_min = { 60 } })
check("a recipe whose main_product is the item wins over a larger net output",
      main.ok and find(main.r.rows, "test-main-item").recipe == "test-main-own", main.ok and F.encode(main.r.rows))
-- An item and a fluid may share a name. A main product that is the FLUID
-- of that name is not the item, so the larger net output must not win on it.
S.add_recipe({ name = "test-twin-fluid-main", main_product = "test-twin", ingredients = { input("iron-plate", 1) },
               products = { { type = "fluid", name = "test-twin", amount = 10, probability = 1 }, product("test-twin", 5) } })
S.add_recipe({ name = "test-twin-item", ingredients = { input("iron-plate", 1) }, products = { product("test-twin", 2) } })
local twin = needs({ force = "player", items = { "test-twin" }, per_min = { 60 } })
check("a main product of the other kind with the same name does not count",
      twin.ok and find(twin.r.rows, "test-twin").recipe == "test-twin-item" and find(twin.r.rows, "test-twin").fluid == nil,
      twin.ok and F.encode(twin.r.rows))

-- Rule 3, net output, with hidden and parameter recipes out of the running
-- even though they would win it.
S.add_recipe({ name = "test-widget-small", ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 1) } })
S.add_recipe({ name = "test-widget-large", ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 2) } })
S.add_recipe({ name = "test-widget-hidden", hidden = true, ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 9) } })
S.add_recipe({ name = "test-widget-param", parameter = true, ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 8) } })
S.add_recipe({ name = "test-widget-a", ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 1) } })
S.add_recipe({ name = "test-widget-b", ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 1) } })
S.add_recipe({ name = "test-widget-c", ingredients = { input("iron-plate", 1) }, products = { product("test-widget", 1) } })
local widget = needs({ force = "player", items = { "test-widget" }, per_min = { 60 } })
local widget_row = widget.ok and find(widget.r.rows, "test-widget") or {}
check("the largest net output wins when no name or main product decides", widget_row.recipe == "test-widget-large",
      F.encode(widget_row))
check("a hidden recipe is never picked nor named", widget.ok
      and not has(alternatives_for(widget, "test-widget"), "test-widget-hidden"), widget.ok and F.encode(widget.r.alternatives))
check("a parameter recipe is never picked nor named", widget.ok
      and not has(alternatives_for(widget, "test-widget"), "test-widget-param"), widget.ok and F.encode(widget.r.alternatives))
-- Five usable recipes: the pick and four others, so the cap has one to drop.
check("alternatives are capped at three, ties broken by name",
      widget.ok and #alternatives_for(widget, "test-widget") == 3 and alternatives_for(widget, "test-widget")[1] == "test-widget-a"
      and alternatives_for(widget, "test-widget")[2] == "test-widget-b" and alternatives_for(widget, "test-widget")[3] == "test-widget-c"
      and not has(alternatives_for(widget, "test-widget"), "test-widget-small"),
      widget.ok and F.encode(widget.r.alternatives))

-- ── the fake reads a list of recipe filters as the engine does ────────
-- recipe_pick asks for many items in one filter list. Measured on a live
-- 2.0.77 game of 712 recipes: invert flips one filter; mode joins a filter to
-- the one before, "and" binding tighter than "or"; the first filter's mode
-- joins nothing; an empty list matches every recipe. These hold the fake to
-- each, so a list the tool sends is answered here as the engine answers it.
-- The only hidden recipe so far is test-widget-hidden.
local function filtered(filters)
  local names = {}
  for name in pairs(prototypes.get_recipe_filtered(filters)) do names[#names + 1] = name end
  table.sort(names)
  return table.concat(names, " ")
end
local function makes_filter(kind, names, extra)
  local f = { filter = "has-product-" .. kind, elem_filters = { { filter = "name", name = names } } }
  for key, value in pairs(extra or {}) do f[key] = value end
  return f
end
local function count_of(filters)
  local n = 0
  for _ in pairs(prototypes.get_recipe_filtered(filters)) do n = n + 1 end
  return n
end
local HIDDEN, NOT_HIDDEN = { filter = "hidden" }, { filter = "hidden", invert = true }
-- Every recipe is hidden or not, so the two counts together are all of them.
local recipe_count = count_of({ HIDDEN }) + count_of({ NOT_HIDDEN })
local WIDGETS = "test-widget-a test-widget-b test-widget-c test-widget-hidden test-widget-large test-widget-param test-widget-small"
local GAS = "advanced-oil-processing basic-oil-processing empty-petroleum-gas-barrel light-oil-cracking"
check("the fake: a name list matches a maker of any name in it",
      filtered({ makes_filter("item", { "test-widget", "iron-gear-wheel" }) })
      == "iron-gear-wheel test-gear-casting " .. WIDGETS,
      filtered({ makes_filter("item", { "test-widget", "iron-gear-wheel" }) }))
check("the fake: recipe_pick's shape, (not hidden and items) or (not hidden and fluids)",
      filtered({ { filter = "hidden", invert = true, mode = "or" }, makes_filter("item", { "test-widget" }, { mode = "and" }),
                 { filter = "hidden", invert = true, mode = "or" }, makes_filter("fluid", { "petroleum-gas" }, { mode = "and" }) })
      == GAS .. " test-widget-a test-widget-b test-widget-c test-widget-large test-widget-param test-widget-small")
check("the fake: and binds tighter than or, so items or fluids and not hidden keeps the hidden item maker",
      filtered({ makes_filter("item", { "test-widget" }), makes_filter("fluid", { "petroleum-gas" }, { mode = "or" }),
                 { filter = "hidden", invert = true, mode = "and" } })
      == GAS .. " " .. WIDGETS)
check("the fake: hidden alone is the hidden recipe, and inverted it is every other one",
      filtered({ HIDDEN }) == "test-widget-hidden" and count_of({ NOT_HIDDEN }) == recipe_count - 1
      and prototypes.get_recipe_filtered({ NOT_HIDDEN })["test-widget-hidden"] == nil
      and prototypes.get_recipe_filtered({ NOT_HIDDEN })["iron-plate"] ~= nil, recipe_count)
check("the fake: the first filter's mode joins nothing",
      filtered({ { filter = "hidden", invert = true, mode = "and" } }) == filtered({ { filter = "hidden", invert = true } }))
check("the fake: invert flips a product filter", count_of({ makes_filter("item", { "test-widget" }, { invert = true }) }) == recipe_count - 7)
check("the fake: an empty filter list matches every recipe, as the engine's does", count_of({}) == recipe_count)
check("the fake refuses a filter it does not implement",
      pcall(prototypes.get_recipe_filtered, { { filter = "category", category = "crafting" } }) == false)
check("the fake refuses a mode that is neither and nor or",
      pcall(prototypes.get_recipe_filtered, { { filter = "hidden", mode = "xor" } }) == false)
check("the fake refuses a name the game does not have, stricter than the engine",
      pcall(prototypes.get_recipe_filtered, { makes_filter("item", { "iron-plate", "no-such-item" }) }) == false)

-- A recipe that takes back more than it gives nets nothing and is no candidate.
S.add_recipe({ name = "test-loss", ingredients = { input("test-loss-item", 3) }, products = { product("test-loss-item", 2) } })
local loss = needs({ force = "player", items = { "test-loss-item" }, per_min = { 60 } })
check("a recipe that nets the item below zero is no candidate: the item is raw",
      loss.ok and loss.r.total == 0 and per_min(loss, "test-loss-item", "raw") == 60, loss.ok and F.encode(loss.r))

-- A two-item cycle: a is made from b, b from a and copper.
S.add_recipe({ name = "test-cycle-a", ingredients = { input("test-cycle-b", 1) }, products = { product("test-cycle-a", 1) } })
S.add_recipe({ name = "test-cycle-b", ingredients = { input("test-cycle-a", 1), input("copper-plate", 1) },
               products = { product("test-cycle-b", 2) } })
local cycle = needs({ force = "player", items = { "test-cycle-a" }, per_min = { 60 } })
check("a two-item cycle stops and adds cycle to approx", cycle.ok and has(cycle.r.approx, "cycle"), F.encode(cycle))
check("the item met again on its own path is charged as raw there: 30 a minute",
      cycle.ok and find(cycle.r.raw, "test-cycle-a") ~= nil and find(cycle.r.raw, "test-cycle-a").cycle == true
      and per_min(cycle, "test-cycle-a", "raw") == 30, cycle.ok and F.encode(cycle.r.raw))
check("and the rest of the tree is still walked", per_min(cycle, "test-cycle-a") == 60 and per_min(cycle, "test-cycle-b") == 60
      and per_min(cycle, "copper-plate") == 30, cycle.ok and F.encode(cycle.r.rows))

-- ── caps: depth and distinct items ────────────────────────────────────
for i = 1, 19 do
  S.add_recipe({ name = "test-chain-" .. i, ingredients = { input("test-chain-" .. (i + 1), 1) },
                 products = { product("test-chain-" .. i, 1) } })
end
S.add_recipe({ name = "test-chain-20", ingredients = { input("iron-plate", 1) }, products = { product("test-chain-20", 1) } })
local chain = needs({ force = "player", items = { "test-chain-1" }, per_min = { 60 } })
check("a chain past 15 deep stops and adds cap to approx", chain.ok and has(chain.r.approx, "cap"), F.encode(chain))
check("15 levels are expanded and the 16th is charged as raw, capped",
      chain.ok and chain.r.total == 15 and find(chain.r.rows, "test-chain-15") ~= nil and find(chain.r.rows, "test-chain-16") == nil
      and find(chain.r.raw, "test-chain-16") ~= nil and find(chain.r.raw, "test-chain-16").capped == true,
      chain.ok and F.encode(chain.r.raw))
check("nothing past the cap is walked", chain.ok and find(chain.r.raw, "iron-ore") == nil, chain.ok and F.encode(chain.r.raw))

local many = {}
for i = 1, 90 do
  local name = string.format("test-many-%02d", i)
  many[i] = input(name, 1)
  S.add_recipe({ name = name, ingredients = { input("copper-plate", 1) }, products = { product(name, 1) } })
end
S.add_recipe({ name = "test-many", ingredients = many, products = { product("test-many", 1) } })
local wide_cap = needs({ force = "player", items = { "test-many" }, per_min = { 1 } })
check("past 80 distinct items the walk stops expanding and adds cap", wide_cap.ok and has(wide_cap.r.approx, "cap"), F.encode(wide_cap.r and wide_cap.r.approx))
check("items met past the cap are raw and capped", wide_cap.ok and find(wide_cap.r.raw, "test-many-90") ~= nil
      and find(wide_cap.r.raw, "test-many-90").capped == true and find(wide_cap.r.raw, "test-many-01") == nil,
      wide_cap.ok and F.encode(find(wide_cap.r.raw, "test-many-90")))
-- A target the cap stopped has a recipe the walk never reached: it says
-- capped, never raw, which would tell the player it is mined. The first
-- target fills the 80 items before red science is met.
local capped_target = needs({ force = "player", items = { "test-many", RED }, per_min = { 1, 60 } })
local red_target = capped_target.ok and capped_target.r.targets[2] or {}
check("a target met past the 80-item cap says capped, not raw", red_target.item == RED and red_target.capped == true
      and red_target.raw == nil and red_target.recipe_locked == nil, F.encode(red_target))

-- ── a wide tree is cut by bytes, total above shown ────────────────────
local wide = {}
for i = 1, 60 do
  local name = string.format("test-wide-%02d", i)
  wide[i] = input(name, 1)
  S.add_recipe({ name = name, ingredients = { input("iron-plate", 1) }, products = { product(name, 1) } })
end
S.add_recipe({ name = "test-wide", ingredients = wide, products = { product("test-wide", 1) } })
-- The first wide item and the last each have a second recipe. Equal flows sort
-- by name, so the first is shown and the last is cut.
S.add_recipe({ name = "test-wide-01-alt", ingredients = { input("copper-plate", 1) }, products = { product("test-wide-01", 1) } })
S.add_recipe({ name = "test-wide-60-alt", ingredients = { input("copper-plate", 1) }, products = { product("test-wide-60", 1) } })
local cut, cut_raw = needs({ force = "player", items = { "test-wide" }, per_min = { 60 } })
check("a wide tree is cut by bounded.fit: total above shown", cut.ok and cut.r.total == 62 and cut.r.shown < cut.r.total
      and cut.r.shown == #cut.r.rows, cut.ok and (cut.r.total .. " total, " .. cut.r.shown .. " shown"))
check("the cut reply stays under the tool's budget (" .. #cut_raw .. " bytes)", #cut_raw <= 4000 + 32, #cut_raw)
check("the cut keeps the largest flow first: 3600 iron plate a minute", cut.ok and cut.r.rows[1].item == "iron-plate"
      and is(cut.r.rows[1].per_min, 3600), cut.ok and F.encode(cut.r.rows[1]))
check("raw rows and targets are never cut", cut.ok and per_min(cut, "iron-ore", "raw") == 3600 and #cut.r.targets == 1,
      cut.ok and F.encode(cut.r.raw))
check("rows are largest flow first", (function()
  for i = 2, #(cut.r.rows or {}) do
    if tonumber(cut.r.rows[i].per_min) > tonumber(cut.r.rows[i - 1].per_min) then return false end
  end
  return cut.ok
end)(), cut.ok and F.encode(cut.r.rows))
check("alternatives are kept for shown rows and dropped with cut ones",
      cut.ok and alternatives_for(cut, "test-wide-01") ~= nil and alternatives_for(cut, "test-wide-60") == nil
      and find(cut.r.rows, "test-wide-60") == nil, cut.ok and F.encode(cut.r.alternatives))

-- ── arguments ─────────────────────────────────────────────────────────
local function refused(args, words, what)
  local reply = needs(args)
  check(what, (not reply.ok) and reply.e == "provider_error" and tostring(reply.m):find(words, 1, true) ~= nil, F.encode(reply))
end
refused({ force = "player", items = { RED, GREEN }, per_min = { 60 } }, "one entry per item",
        "mismatched list lengths are an argument error")
refused({ force = "player", items = { RED }, machines = { 4, 5 } }, "one entry per item",
        "a machines list of the wrong length is an argument error")
refused({ force = "player", items = { RED }, per_min = { 60 }, machines = { 4 } }, "not both",
        "per_min and machines together are an argument error")
refused({ force = "player", items = { RED } }, "give per_min", "neither per_min nor machines is an argument error")
refused({ force = "player", items = { RED }, per_min = { 0 } }, "above 0", "a rate of 0 is an argument error")
refused({ force = "player", items = { RED }, per_min = { -5 } }, "above 0", "a negative rate is an argument error")
refused({ force = "player", items = { RED }, per_min = { "lots" } }, "above 0", "a rate that is not a number is an argument error")
refused({ force = "player", items = { RED }, per_min = { 1e12 } }, "at most", "a rate past the ceiling is an argument error")
refused({ force = "player", items = { RED }, machines = { 1e7 } }, "at most", "a machine count past the ceiling is an argument error")
refused({ force = "player", items = { RED }, per_min = { 60 }, machine = 7 }, "crafting machine prototype name",
        "a machine that is not a name is an argument error")
refused({ force = "player", items = { RED }, per_min = { 60 }, belt = "" }, "transport belt prototype name",
        "an empty belt name is an argument error")
local listed_machine = needs({ force = "player", items = { RED }, per_min = { 60 }, machine = { "assembling-machine-2" } })
check("a machine sent as a list is refused by its type, never a table address",
      (not listed_machine.ok) and tostring(listed_machine.m):find("not table", 1, true) ~= nil
      and tostring(listed_machine.m):find("0x", 1, true) == nil, F.encode(listed_machine))
refused({ force = "player", per_min = { 60 } }, "items is required", "no items is an argument error")
refused({ force = "player", items = { 7 }, per_min = { 60 } }, "names", "an item that is not a name is an argument error")
refused({ force = "player", items = { RED }, per_min = { 60 }, stop_at = { false } }, "names",
        "a stop_at that is not names is an argument error")
local thirteen, thirteen_rates = {}, {}
for i = 1, 13 do thirteen[i], thirteen_rates[i] = RED, 1 end
refused({ force = "player", items = thirteen, per_min = thirteen_rates }, "at most 12", "more than 12 targets is an argument error")
refused({ force = "nobody", items = { RED }, per_min = { 60 } }, "unknown force", "an unknown force is an error")
local bare = rpc({ op = "call", i = "ai-agent-bridge-tools", f = "recipe_needs" })
check("no argument table at all says force is required", (not bare.ok) and bare.m == "force is required", F.encode(bare))
local one = needs({ force = "player", items = RED, per_min = 60 })
check("one bare value is a one-entry list", one.ok and per_min(one, "iron-plate") == 120, F.encode(one))

local unknown = needs({ force = "player", items = { "red-science", RED }, per_min = { 60, 60 }, stop_at = { "iron plate" } })
check("unknown names give found=false", unknown.ok and unknown.r.found == false and unknown.r.force == "player", F.encode(unknown))
check("and list every unknown name, sorted, stop_at included", unknown.ok and #unknown.r.unknown == 2
      and unknown.r.unknown[1] == "iron plate" and unknown.r.unknown[2] == "red-science", unknown.ok and F.encode(unknown.r.unknown))
check("and say what names look like", unknown.ok and tostring(unknown.r.reason):find("internal names", 1, true) ~= nil,
      unknown.ok and unknown.r.reason)

-- ── did_you_mean: the real name beside every miss ─────────────────────
-- The live DeepSeek run sent red-science-pack and paid a round to learn it
-- was wrong. Each unknown name now comes back with up to five real item or
-- fluid names, likeliest first: the name respelled, then what players call
-- a pack, circuit or belt, then names sharing a word with it.
-- Mutation-tested a pass at a time: without the respelling, frobnicators and
-- iron plate lose their first guess; without the nicknames, the colour
-- names do; without the shared words, copper-wire has none; without the cap,
-- the test names come back more than five.
local function guesses(reply, name) return reply.ok and reply.r.did_you_mean and reply.r.did_you_mean[name] end
check("the stop_at name respelled is the first guess: iron plate is iron-plate",
      (guesses(unknown, "iron plate") or {})[1] == "iron-plate", F.encode(unknown.r and unknown.r.did_you_mean))
check("a colour name is the first guess: red science is automation-science-pack",
      (guesses(unknown, "red-science") or {})[1] == RED, F.encode(unknown.r and unknown.r.did_you_mean))
check("the reason points at did_you_mean", unknown.ok and tostring(unknown.r.reason):find("did_you_mean", 1, true) ~= nil,
      unknown.ok and unknown.r.reason)
local NICKNAMED = {
  ["red-science-pack"] = RED, ["green-science-pack"] = GREEN, ["Blue Science"] = BLUE,
  ["grey-science-packs"] = "military-science-pack", ["red-circuit"] = "advanced-circuit",
  ["green-circuits"] = "electronic-circuit", ["yellow-belt"] = "transport-belt",
  ["red_belt"] = "fast-transport-belt", ["blue transport belts"] = "express-transport-belt",
}
for said, meant in pairs(NICKNAMED) do
  local reply = needs({ force = "player", items = { said }, per_min = { 60 } })
  check("did_you_mean reads " .. said .. " as " .. meant, (guesses(reply, said) or {})[1] == meant,
        F.encode(reply.r and reply.r.did_you_mean))
end
S.add_recipe({ name = "frobnicator", ingredients = { { type = "item", name = "iron-plate", amount = 1 } },
               products = { { type = "item", name = "frobnicator", amount = 1, probability = 1 } } })
local frob = needs({ force = "player", items = { "Frobnicators" }, per_min = { 60 } })
check("a plural with capitals is respelled to the real name, though no shorter word matches",
      (guesses(frob, "Frobnicators") or {})[1] == "frobnicator", F.encode(frob.r and frob.r.did_you_mean))
local wire = needs({ force = "player", items = { "copper-wire" }, per_min = { 60 } })
check("a name sharing a word with real ones gets those: copper-wire has copper-cable",
      (function()
        for _, name in ipairs(guesses(wire, "copper-wire") or {}) do if name == "copper-cable" then return true end end
        return false
      end)(), F.encode(wire.r and wire.r.did_you_mean))
local fluid_miss = needs({ force = "player", items = { "Petroleum Gas" }, per_min = { 60 } })
check("a fluid is a real name too: Petroleum Gas is petroleum-gas",
      (guesses(fluid_miss, "Petroleum Gas") or {})[1] == "petroleum-gas", F.encode(fluid_miss.r and fluid_miss.r.did_you_mean))
local crowded = needs({ force = "player", items = { "test-twin-crowd" }, per_min = { 60 } })
check("never more than five guesses for one name", guesses(crowded, "test-twin-crowd") ~= nil
      and #guesses(crowded, "test-twin-crowd") == 5, F.encode(crowded.r and crowded.r.did_you_mean))
local nothing_close = needs({ force = "player", items = { "zq", RED }, per_min = { 60, 60 }, stop_at = { "iron plate" } })
check("a name with nothing close has no entry, and the others keep theirs",
      nothing_close.ok and nothing_close.r.found == false and guesses(nothing_close, "zq") == nil
      and (guesses(nothing_close, "iron plate") or {})[1] == "iron-plate", F.encode(nothing_close.r))
local none_close = needs({ force = "player", items = { "zq" }, per_min = { 60 } })
check("no guess at all leaves did_you_mean out", none_close.ok and none_close.r.found == false
      and none_close.r.did_you_mean == nil, F.encode(none_close.r))
-- Plurals: no prototype name holds gears, circuits, belts, plates or
-- engines, so the word passes look for the singular spellings too. On the
-- live 2.0.77 save every one of these came back with no guess at all.
local PLURALS = { gears = "iron-gear-wheel", circuits = "electronic-circuit", belts = "transport-belt",
                  plates = "iron-plate", engines = "engine-unit" }
for said, meant in pairs(PLURALS) do
  local reply = needs({ force = "player", items = { said }, per_min = { 60 } })
  check("did_you_mean finds " .. meant .. " for " .. said, has(guesses(reply, said), meant),
        F.encode(reply.r and reply.r.did_you_mean))
end
-- A name holding every word of what was asked for comes before names that
-- share one. Fewer than five share one here, so that pass alone would sort
-- them and put the name holding both words last.
local function made(name)
  S.add_recipe({ name = name, ingredients = { { type = "item", name = "iron-plate", amount = 1 } },
                 products = { { type = "item", name = name, amount = 1, probability = 1 } } })
end
for _, name in ipairs({ "test-zebu-1", "test-zebu-2", "zz-test-zebu-yak" }) do made(name) end
local both_words = needs({ force = "player", items = { "Zebu Yaks" }, per_min = { 60 } })
check("a name holding every word, the singular's included, is the first guess",
      (guesses(both_words, "Zebu Yaks") or {})[1] == "zz-test-zebu-yak", F.encode(both_words.r and both_words.r.did_you_mean))
-- A word only the singular spells still counts when no name holds them all.
local mixed = needs({ force = "player", items = { "copper gears" }, per_min = { 60 } })
check("copper gears shares gear with iron-gear-wheel, through the singular",
      has(guesses(mixed, "copper gears"), "iron-gear-wheel") and has(guesses(mixed, "copper gears"), "copper-plate"),
      F.encode(mixed.r and mixed.r.did_you_mean))
local close_names = require("scripts.close_names")
local every = close_names.sharing_every_fragment({ ["b-yak-zebu"] = true, ["a-zebu-yak"] = true, ["c-zebu"] = true, ["d-yak-zebu-2"] = true,
                                                   ["e-yak-zebu"] = true, ["f-zebu-yak"] = true, ["g-zebu-yak"] = true }, "zebu yak")
check("sharing_every_fragment: names holding every word, sorted, at most five",
      table.concat(every, ",") == "a-zebu-yak,b-yak-zebu,d-yak-zebu-2,e-yak-zebu,f-zebu-yak", table.concat(every, ","))
check("sharing_every_fragment: a query with no word of three letters finds nothing",
      #close_names.sharing_every_fragment({ ["ab-cd"] = true }, "ab cd") == 0)

-- ── a pure read ───────────────────────────────────────────────────────
local before = F.encode(storage)
needs({ force = "player", items = { RED, GREEN, BLUE }, per_min = { 60, 60, 60 } })
needs({ force = "team-3", items = { RED }, machines = { 4 } })
check("recipe_needs writes no storage", F.encode(storage) == before)
-- The engine is asked a layer of the tree at a time, never an item at a
-- time: one filter for every item that places a crafting machine or belt,
-- one per layer of the walk, and one more for a layer whose side recipes the
-- round-trip rule looks through. Three packs: the twelve placing items (1);
-- the three packs (2); copper plate, gears, inserters, engines, advanced
-- circuits and sulfur, the belt already asked (3); copper ore, iron plate,
-- circuits, steel, pipe, plastic, cable, water and petroleum gas, items and
-- fluids in one filter (4), then the water and petroleum gas barrels, crude
-- oil and light oil, inputs of the side recipes for water and gas (5); iron
-- ore and coal, crude oil already asked (6), then the crude oil barrel behind
-- emptying one (7). It was 31 asked an item at a time. Red by machine count:
-- the placing items, the pack once before it is sized, then three layers
-- below it: 5. Pirion's three packs by machine count: the placing items, the
-- three packs in one filter before any is sized, then the layers below them
-- as above: 7, where sizing them one at a time would be 9. Red stopped at
-- iron plate: the placing items and three layers, the last only copper ore,
-- since a stopped item is never asked about and neither is anything under
-- it: 4.
local function filters_spent(args)
  local before_calls = S.recipe_filter_calls
  needs(args)
  return S.recipe_filter_calls - before_calls
end
local spent = filters_spent({ force = "player", items = { RED, GREEN, BLUE }, per_min = { 60, 60, 60 } })
check("one recipe filter per layer of the tree, not per item (" .. spent .. " for three packs)", spent == 7, spent)
local spent_count = filters_spent({ force = "player", items = { RED }, machines = { 4 } })
check("a target sized by machines is asked of the engine once, not twice (" .. spent_count .. ")", spent_count == 5, spent_count)
local spent_sized = filters_spent({ force = "player", items = { BLUE, RED, GREEN }, machines = { 48, 24, 24 } })
check("targets sized by machines are asked of the engine together (" .. spent_sized .. ")", spent_sized == 7, spent_sized)
local spent_stop = filters_spent({ force = "player", items = { RED }, per_min = { 60 }, stop_at = { "iron-plate" } })
check("a stopped item is never asked about, nor anything under it (" .. spent_stop .. ")", spent_stop == 4, spent_stop)

-- ── a prefetch changes nothing a pick returns ─────────────────────────
-- Ranking a list of items in one filter must pick exactly what asking for
-- each alone picks. Every item and fluid the fake has, asked both ways.
local recipe_pick = require("scripts.tools.recipe_pick")
local every = {}
for _, kind in ipairs({ "item", "fluid" }) do
  for name in pairs(prototypes[kind]) do every[#every + 1] = { kind = kind, name = name } end
end
table.sort(every, function(a, b) return a.kind .. "/" .. a.name < b.kind .. "/" .. b.name end)
local function every_pick(prefetch_first)
  local picker, out = recipe_pick.memo(S.force), {}
  if prefetch_first then picker.prefetch(every) end
  for _, asked in ipairs(every) do
    local found, locked, mined = picker.pick(asked.kind, asked.name)
    out[#out + 1] = F.encode({ asked.kind, asked.name, found and found.name or false, found and found.net or false,
                               found and found.alternatives or false, found and found.byproducts or false,
                               locked or false, mined and mined.alternatives or false })
  end
  return table.concat(out, "\n")
end
local one_by_one = every_pick(false)
check("one prefetch of all " .. #every .. " items and fluids picks exactly what asking one at a time does",
      every_pick(true) == one_by_one and #every > 100, #every)

-- ── the walk asks for its tree a layer at a time, within its own caps ──
-- A made-up tree behind a stub picker: tree[name] lists what one craft of
-- name takes, one of each; anything else is raw. The stub records every
-- item the walk prefetches, so these read the walk's own bounds.
local recipe_walk = require("scripts.tools.recipe_walk")
local function stub_picker(tree)
  local stub = { asked = {}, calls = 0 }
  function stub.pick(kind, name)
    local inputs = tree[name]
    if not inputs then return nil, false end
    local io = { [kind .. "/" .. name] = 1 }
    for _, taken in ipairs(inputs) do io["item/" .. taken] = -1 end
    return { name = name, io = io, net = 1, alternatives = {}, byproducts = false }
  end
  function stub.prefetch(list)
    stub.calls = stub.calls + 1
    for _, listed in ipairs(list) do stub.asked[listed.name] = true end
  end
  return stub
end
local function walk_stub(tree, target, stop)
  local stub = stub_picker(tree)
  recipe_walk.run({ { kind = "item", name = target, per_min = 1 } }, stub, stop)
  return stub
end
local diamond = walk_stub({ a = { "b", "c" }, b = { "d" }, c = { "d" } }, "a")
check("the walk prefetches each layer once: a, then b and c, then d",
      diamond.calls == 3 and diamond.asked.a and diamond.asked.b and diamond.asked.c and diamond.asked.d, diamond.calls)
local stopped_walk = walk_stub({ a = { "b" }, b = { "c" } }, "a", { ["item/b"] = true })
check("the walk never prefetches a stopped item or what is under it",
      stopped_walk.asked.a and not stopped_walk.asked.b and not stopped_walk.asked.c, F.encode(stopped_walk.asked))
local deep = {}
for i = 1, 20 do deep["deep-" .. i] = { "deep-" .. (i + 1) } end
local deep_walk = walk_stub(deep, "deep-1")
check("the walk prefetches no deeper than it picks: the 16th level, never the 17th",
      deep_walk.asked["deep-16"] and not deep_walk.asked["deep-17"], deep_walk.calls)
local fan = { fan = {} }
for i = 1, 90 do
  local name = string.format("fan-%02d", i)
  fan.fan[i] = name
  fan[name] = { "under-" .. name }
end
local fan_walk = walk_stub(fan, "fan")
check("the walk prefetches no wider once it has met 80 items: all 90 below the target, nothing under them",
      fan_walk.asked["fan-90"] and not fan_walk.asked["under-fan-01"] and not fan_walk.asked["under-fan-90"], fan_walk.calls)
-- Three entity reads: the crafting machines, the belts, and the resources
-- the mined rule asks about, the last one however many raw items there are.
local entity_before = S.entity_filter_calls
needs({ force = "player", items = { RED }, per_min = { 60 } })
check("crafting machines, belts and resources are read once per call each", S.entity_filter_calls - entity_before == 3,
      S.entity_filter_calls - entity_before)

-- ── Space Age: what the game hands out stays raw ─────────────────────
-- Once a force researches asteroid crushing, ice melting, coal synthesis or
-- scrap recycling, those recipes make ore, water, coal and stone, and the
-- walk followed them: iron ore came from asteroid chunks on a force that
-- mines it. An item mined from a resource or pumped from a tile, with no
-- recipe named after it, is raw again: its raw row says raw = "mined" and
-- the recipes that could make it stay in alternatives. Transcribed from
-- headless 2.0.77 data/space-age/prototypes/recipe.lua and
-- entity/resources.lua, and data/base/prototypes/recipe.lua for sulfuric
-- acid. Last in the file, so these recipes change no count above.
-- Mutation-tested, each guard alone: no mined rule in recipe_pick; no
-- named-recipe exception; no minable check; no product type check; no tile
-- read; no raw = "mined" on the row; no alternatives for it; no mined reason
-- for a machine count. Each turns at least one check below red.
local function fluid_in(name, amount) return { type = "fluid", name = name, amount = amount } end
local function fluid_out(name, amount) return { type = "fluid", name = name, amount = amount, probability = 1 } end
S.add_recipe({ name = "metallic-asteroid-crushing", category = "crushing", energy = 2,
               ingredients = { input("metallic-asteroid-chunk", 1) },
               products = { product("iron-ore", 20), product("metallic-asteroid-chunk", 1, { probability = 0.2 }) } })
S.add_recipe({ name = "advanced-metallic-asteroid-crushing", category = "crushing", energy = 5,
               ingredients = { input("metallic-asteroid-chunk", 1) },
               products = { product("iron-ore", 10), product("copper-ore", 4),
                            product("metallic-asteroid-chunk", 1, { probability = 0.05 }) } })
S.add_recipe({ name = "ice-melting", category = "chemistry", energy = 1,
               ingredients = { input("ice", 1) }, products = { fluid_out("water", 20) } })
S.add_recipe({ name = "coal-synthesis", category = "chemistry", energy = 2,
               ingredients = { input("carbon", 5), input("sulfur", 1), fluid_in("water", 10) },
               products = { product("coal", 1) } })
S.add_recipe({ name = "scrap-recycling", category = "recycling-or-hand-crafting", energy = 0.2,
               ingredients = { input("scrap", 1) },
               products = { product("iron-gear-wheel", 1, { probability = 0.2 }), product("stone", 1, { probability = 0.04 }),
                            product("holmium-ore", 1, { probability = 0.01 }) } })
S.add_recipe({ name = "sulfuric-acid", category = "chemistry", energy = 1,
               ingredients = { input("sulfur", 5), input("iron-plate", 1), fluid_in("water", 100) },
               products = { fluid_out("sulfuric-acid", 50) } })
S.add_resource("sulfuric-acid-geyser", { { type = "fluid", name = "sulfuric-acid", amount_min = 10, amount_max = 10, probability = 1 } })
S.add_resource("scrap", { product("scrap", 1) })

local function mined(reply, item)
  local row = find(reply.ok and reply.r.raw, item)
  return row ~= nil and row.raw == "mined"
end
local function nowhere(reply, item)
  return reply.ok and find(reply.r.rows, item) == nil and find(reply.r.raw, item) == nil
end
local function alternatives(reply, item) return F.encode(alternatives_for(reply, item) or {}) end

local red_sa = needs({ force = "player", items = { RED }, per_min = { 60 } })
check("with asteroid crushing researched, red science still mines its iron ore",
      mined(red_sa, "iron-ore") and per_min(red_sa, "iron-ore", "raw") == 120 and per_min(red_sa, "iron-plate") == 120,
      red_sa.ok and F.encode(red_sa.r.raw))
check("and no asteroid chunk appears anywhere", nowhere(red_sa, "metallic-asteroid-chunk"), red_sa.ok and F.encode(red_sa.r))
check("crushing stays named, best first, so the answer can mention it",
      alternatives(red_sa, "iron-ore") == '["metallic-asteroid-crushing","advanced-metallic-asteroid-crushing"]'
      and alternatives(red_sa, "copper-ore") == '["advanced-metallic-asteroid-crushing"]', alternatives(red_sa, "iron-ore"))
check("an ore row says mined and nothing else new", red_sa.ok and find(red_sa.r.raw, "copper-ore").raw == "mined"
      and find(red_sa.r.raw, "copper-ore").recipe_locked == nil, red_sa.ok and F.encode(find(red_sa.r.raw, "copper-ore")))

local blue_sa = needs({ force = "player", items = { BLUE }, per_min = { 60 } })
check("with ice melting researched, water is still pumped: raw, mined, the oracle unchanged",
      mined(blue_sa, "water") and per_min(blue_sa, "water", "raw") == per_min(blue, "water", "raw")
      and per_min(blue_sa, "iron-plate") == 720, blue_sa.ok and F.encode(blue_sa.r.raw))
check("and no ice is melted for it", nowhere(blue_sa, "ice"), blue_sa.ok and F.encode(blue_sa.r))
check("ice melting is its alternative, and the barrel round trip still is not",
      alternatives(blue_sa, "water") == '["ice-melting"]', alternatives(blue_sa, "water"))
check("with coal synthesis researched, coal is still mined, synthesis its alternative",
      mined(blue_sa, "coal") and nowhere(blue_sa, "carbon") and alternatives(blue_sa, "coal") == '["coal-synthesis"]',
      blue_sa.ok and F.encode(blue_sa.r.raw))
check("crude oil is mined, with no alternative to name", mined(blue_sa, "crude-oil")
      and alternatives_for(blue_sa, "crude-oil") == nil, blue_sa.ok and F.encode(blue_sa.r.alternatives))

local acid = needs({ force = "player", items = { "sulfuric-acid" }, per_min = { 50 } })
check("sulfuric acid keeps the recipe named after it, though a geyser gives it too",
      acid.ok and find(acid.r.rows, "sulfuric-acid") ~= nil and find(acid.r.rows, "sulfuric-acid").recipe == "sulfuric-acid"
      and find(acid.r.raw, "sulfuric-acid") == nil and per_min(acid, "iron-plate") == 1, acid.ok and F.encode(acid.r))

local bricks = needs({ force = "player", items = { "stone-brick" }, per_min = { 60 } })
check("with scrap recycling researched, stone is still mined, recycling its alternative",
      mined(bricks, "stone") and per_min(bricks, "stone", "raw") == 120 and nowhere(bricks, "scrap")
      and alternatives(bricks, "stone") == '["scrap-recycling"]', bricks.ok and F.encode(bricks.r))
local holmium = needs({ force = "player", items = { "holmium-ore" }, per_min = { 1 } })
check("holmium ore, which nothing mines, is still made by recycling scrap, and the scrap is mined",
      holmium.ok and find(holmium.r.rows, "holmium-ore").recipe == "scrap-recycling"
      and mined(holmium, "scrap") and per_min(holmium, "scrap", "raw") == 100, holmium.ok and F.encode(holmium.r))

local ore_target = needs({ force = "player", items = { "iron-ore" }, per_min = { 60 } })
check("a mined target is raw, mined, with no crafted row", ore_target.ok and ore_target.r.targets[1].raw == true
      and mined(ore_target, "iron-ore") and ore_target.r.total == 0, ore_target.ok and F.encode(ore_target.r))
local ore_count = needs({ force = "player", items = { "iron-ore" }, machines = { 2 } })
check("a machine count for a mined target says it is mined, not crafted",
      ore_count.ok and is(ore_count.r.targets[1].per_min, 0)
      and tostring(ore_count.r.targets[1].reason):find("mined", 1, true) ~= nil, ore_count.ok and F.encode(ore_count.r.targets))
local fresh_ore = needs({ force = "team-3", items = { "iron-plate" }, per_min = { 60 } })
check("a force without crushing mines its ore too, with no alternative to name",
      mined(fresh_ore, "iron-ore") and alternatives_for(fresh_ore, "iron-ore") == nil, fresh_ore.ok and F.encode(fresh_ore.r))

-- What counts as handed out: a resource that is minable, item and fluid
-- products only, and a tile's pumped fluid.
S.add_resource("test-unminable-rock", { product("test-unminable-ore", 1) }, { minable = false })
S.add_recipe({ name = "test-unminable-maker", ingredients = { input("iron-plate", 1) }, products = { product("test-unminable-ore", 1) } })
local unminable = needs({ force = "player", items = { "test-unminable-ore" }, per_min = { 60 } })
check("a resource that cannot be mined hands nothing out: its ore is crafted",
      unminable.ok and find(unminable.r.rows, "test-unminable-ore") ~= nil
      and find(unminable.r.rows, "test-unminable-ore").recipe == "test-unminable-maker", unminable.ok and F.encode(unminable.r))
S.add_resource("test-odd-resource", { { type = "research-progress", research_item = RED, amount = 1 }, product("test-odd-ore", 1) })
S.add_recipe({ name = "test-odd-maker", ingredients = { input("iron-plate", 1) }, products = { product("test-odd-ore", 1) } })
local odd = needs({ force = "player", items = { "test-odd-ore" }, per_min = { 60 } })
check("a resource product that is neither item nor fluid is skipped, not a crash, and the rest still count",
      odd.ok and mined(odd, "test-odd-ore") and alternatives(odd, "test-odd-ore") == '["test-odd-maker"]', F.encode(odd))
S.add_tile("test-lake", "test-lake-fluid")
S.add_recipe({ name = "test-lake-fluid-from-plates", ingredients = { input("iron-plate", 1) },
               products = { fluid_out("test-lake-fluid", 10) } })
local lake = needs({ force = "player", items = { "test-lake-fluid" }, per_min = { 60 } })
check("a fluid a tile gives a pump is raw, mined, with its recipe as the alternative",
      mined(lake, "test-lake-fluid") and find(lake.r.raw, "test-lake-fluid").fluid == true
      and alternatives(lake, "test-lake-fluid") == '["test-lake-fluid-from-plates"]', lake.ok and F.encode(lake.r))

-- ── where it is made: the surface decides what is mined ──────────────
-- Fulgora's oil ocean gives heavy oil, so with every planet counted heavy
-- oil was mined for everyone and lubricant on Nauvis lost its crude oil. A
-- surface hands out what its own map generator lists, else its planet's;
-- a platform nothing; and with no surface, or one that lists nothing,
-- anything mined anywhere still counts, with any_planet in approx when a
-- recipe could make that raw row instead. Mutation-tested, each guard
-- alone: no platform rule; the surface's own lists ignored; no planet
-- fallback; no fallback to any planet; any planet never said; empty lists
-- taken as nothing mined; tiles not read; resources not read; the surface
-- not passed to the picker; no surface type check; an empty surface taken
-- as a name; no any_planet; any_planet on rows no recipe makes.
S.add_recipe({ name = "lubricant", category = "chemistry", energy = 1,
               ingredients = { fluid_in("heavy-oil", 10) }, products = { fluid_out("lubricant", 10) } })
S.add_tile("oil-ocean-shallow", "heavy-oil")
S.add_surface({ name = "fulgora", places = { entity = { "scrap" }, tile = { "oil-ocean-shallow" } },
                planet = { entity = { "scrap" }, tile = { "oil-ocean-shallow" } } })
S.add_surface({ name = "landing-pen" })
S.add_surface({ name = "lab-moon", planet = { entity = { "iron-ore" } } })
local function lube(surface) return needs({ force = "player", items = { "lubricant" }, per_min = { 60 }, surface = surface }) end

local lube_anywhere = lube(nil)
check("with no surface heavy oil is mined somewhere, so it counts as mined",
      mined(lube_anywhere, "heavy-oil") and per_min(lube_anywhere, "heavy-oil", "raw") == 60, lube_anywhere.ok and F.encode(lube_anywhere.r))
check("and approx says any_planet, the recipe that makes it in alternatives",
      lube_anywhere.ok and has(lube_anywhere.r.approx, "any_planet")
      and alternatives(lube_anywhere, "heavy-oil") == '["advanced-oil-processing"]', lube_anywhere.ok and F.encode(lube_anywhere.r))
local lube_nauvis = lube("nauvis")
check("on Nauvis heavy oil is made from crude oil: 60 a minute is 2.4 oil processing crafts",
      lube_nauvis.ok and find(lube_nauvis.r.rows, "heavy-oil") ~= nil and find(lube_nauvis.r.rows, "heavy-oil").recipe == "advanced-oil-processing"
      and per_min(lube_nauvis, "crude-oil", "raw") == 240 and per_min(lube_nauvis, "water", "raw") == 120
      and mined(lube_nauvis, "crude-oil"), lube_nauvis.ok and F.encode(lube_nauvis.r))
check("and Nauvis says what it mines, so approx has no any_planet",
      lube_nauvis.ok and not has(lube_nauvis.r.approx, "any_planet"), lube_nauvis.ok and F.encode(lube_nauvis.r.approx))
local lube_index = lube(1)
check("a surface by index is the same surface", lube_index.ok and F.encode(lube_index.r) == F.encode(lube_nauvis.r),
      lube_index.ok and F.encode(lube_index.r.raw))
local lube_fulgora = lube("fulgora")
check("on Fulgora the oil ocean gives heavy oil: mined, no crude oil, approx clean",
      mined(lube_fulgora, "heavy-oil") and nowhere(lube_fulgora, "crude-oil") and lube_fulgora.r.approx == nil,
      lube_fulgora.ok and F.encode(lube_fulgora.r))
local lube_pen = lube("landing-pen")
check("a surface whose map generator lists nothing counts every planet, and says so",
      mined(lube_pen, "heavy-oil") and has(lube_pen.r.approx, "any_planet"), lube_pen.ok and F.encode(lube_pen.r))
local lube_blank = lube("")
check("an empty surface is no surface", lube_blank.ok and F.encode(lube_blank.r) == F.encode(lube_anywhere.r), lube_blank.ok and F.encode(lube_blank.r))
local moon_plate = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "lab-moon" })
check("a surface that lists nothing reads its planet's lists: iron ore is mined there",
      mined(moon_plate, "iron-ore") and moon_plate.r.approx == nil, moon_plate.ok and F.encode(moon_plate.r))
local moon_lube = lube("lab-moon")
check("and what the planet does not list is not mined there: crude oil is raw, not mined",
      moon_lube.ok and find(moon_lube.r.raw, "crude-oil") ~= nil and find(moon_lube.r.raw, "crude-oil").raw == nil
      and find(moon_lube.r.rows, "heavy-oil") ~= nil, moon_lube.ok and F.encode(moon_lube.r))
-- A surface's own lists win over its planet's: a script may change them.
S.add_surface({ name = "terraformed", places = { tile = { "oil-ocean-shallow" } }, planet = { entity = { "iron-ore", "crude-oil" } } })
local terra_lube = lube("terraformed")
check("a surface's own lists win over its planet's: heavy oil pumped, crude oil not mined",
      mined(terra_lube, "heavy-oil") and nowhere(terra_lube, "crude-oil"), terra_lube.ok and F.encode(terra_lube.r))
-- Lists that hold only land tiles still say something: nothing is mined there.
S.add_surface({ name = "barren", places = { tile = { "grass-1" } } })
local barren_lube = lube("barren")
check("a generator that lists only land mines and pumps nothing, and says no any_planet",
      barren_lube.ok and find(barren_lube.r.raw, "crude-oil") ~= nil and find(barren_lube.r.raw, "crude-oil").raw == nil
      and not has(barren_lube.r.approx, "any_planet"), barren_lube.ok and F.encode(barren_lube.r))
local entity_reads = S.entity_filter_calls
local orbit_plate = needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "platform-1" })
check("a platform mines and pumps nothing, and reads no resource to know it",
      S.entity_filter_calls - entity_reads == 2, S.entity_filter_calls - entity_reads)
check("so on a platform iron ore comes from crushing, and the chunks it takes are raw, not mined",
      orbit_plate.ok and find(orbit_plate.r.rows, "iron-ore") ~= nil
      and find(orbit_plate.r.rows, "iron-ore").recipe == "metallic-asteroid-crushing" and per_min(orbit_plate, "iron-ore") == 60
      and is(per_min(orbit_plate, "metallic-asteroid-chunk", "raw"), 2.4) and find(orbit_plate.r.raw, "metallic-asteroid-chunk").raw == nil,
      orbit_plate.ok and F.encode(orbit_plate.r))
entity_reads = S.entity_filter_calls
needs({ force = "player", items = { "iron-plate" }, per_min = { 60 }, surface = "lab-moon" })
check("resources are read once, though the surface's lists and then its planet's are asked", S.entity_filter_calls - entity_reads == 3,
      S.entity_filter_calls - entity_reads)
local nowhere_surface = needs({ force = "player", items = { "lubricant" }, per_min = { 60 }, surface = "vulcanus" })
check("a surface the game does not have is found=false, pointing at list_surfaces",
      nowhere_surface.ok and nowhere_surface.r.found == false and nowhere_surface.r.surface == "vulcanus"
      and nowhere_surface.r.force == "player" and tostring(nowhere_surface.r.reason):find("list_surfaces", 1, true) ~= nil,
      F.encode(nowhere_surface))
local listed_surface = needs({ force = "player", items = { "lubricant" }, per_min = { 60 }, surface = { "nauvis" } })
check("a surface sent as a list is refused by its type, never a table address",
      (not listed_surface.ok) and tostring(listed_surface.m):find("not table", 1, true) ~= nil
      and tostring(listed_surface.m):find("0x", 1, true) == nil, F.encode(listed_surface))

-- ── dead ends: a side recipe that needs what nobody can get ───────────
-- Nuclear fuel reprocessing nets 3 uranium-238 a craft to uranium
-- processing's 0.993, so it won on output and charged depleted fuel cells,
-- a burnt result no recipe makes, as raw where uranium ore belonged. Now a
-- side recipe with an unobtainable input loses to one whose inputs all
-- resolve. data/base/prototypes/recipe.lua, 2.0.77. Mutation-tested: no
-- dead-end ordering; obtainable without the mined check; dead ends dropped
-- instead of ranked last; every candidate called alive.
S.add_recipe({ name = "nuclear-fuel-reprocessing", category = "centrifuging", energy = 60, main_product = "",
               ingredients = { input("depleted-uranium-fuel-cell", 5) }, products = { product("uranium-238", 3) } })
S.add_recipe({ name = "uranium-fuel-cell", energy = 10,
               ingredients = { input("iron-plate", 10), input("uranium-235", 1), input("uranium-238", 19) },
               products = { product("uranium-fuel-cell", 10) } })
local cells = needs({ force = "player", items = { "uranium-fuel-cell" }, per_min = { 10 }, surface = "nauvis" })
-- One craft a minute: 19 uranium-238, plus 3 more for the kovarex craft that
-- nets the one uranium-235, is 22 at 0.993 a processing craft.
check("kovarex and reprocessing researched: uranium-238 still comes from uranium processing",
      cells.ok and find(cells.r.rows, "uranium-238") ~= nil and find(cells.r.rows, "uranium-238").recipe == "uranium-processing"
      and per_min(cells, "uranium-238") == 22, cells.ok and F.encode(cells.r.rows))
check("and the uranium ore is charged: 22 / 0.993 crafts of 10 ore", cells.ok and is(per_min(cells, "uranium-ore", "raw"), 221.55),
      cells.ok and F.encode(cells.r.raw))
check("no depleted fuel cell is charged, and reprocessing is only an alternative",
      nowhere(cells, "depleted-uranium-fuel-cell") and alternatives(cells, "uranium-238") == '["nuclear-fuel-reprocessing"]',
      cells.ok and F.encode(cells.r))
S.set_recipe("player", "uranium-processing", { enabled = false })
local only_reprocessing = needs({ force = "player", items = { "uranium-238" }, per_min = { 30 }, surface = "nauvis" })
check("with nothing else left, the dead end is still the pick and its input is raw",
      only_reprocessing.ok and find(only_reprocessing.r.rows, "uranium-238").recipe == "nuclear-fuel-reprocessing"
      and per_min(only_reprocessing, "depleted-uranium-fuel-cell", "raw") == 50, only_reprocessing.ok and F.encode(only_reprocessing.r))
S.set_recipe("player", "uranium-processing", { enabled = true })
-- Coal liquefaction (data/base/prototypes/recipe.lua, 2.0.77) nets 65 heavy
-- oil a craft to oil processing's 25, but takes steam, which no recipe in
-- the base game makes: a boiler does. So it is a dead end for the tool, and
-- heavy oil stays on crude oil.
S.add_recipe({ name = "coal-liquefaction", category = "oil-processing", energy = 5,
               ingredients = { input("coal", 10), fluid_in("heavy-oil", 25), fluid_in("steam", 50) },
               products = { { type = "fluid", name = "heavy-oil", amount = 90, probability = 1, ignored_by_productivity = 25 },
                            fluid_out("light-oil", 20), fluid_out("petroleum-gas", 10) } })
local liquefied = needs({ force = "player", items = { "lubricant" }, per_min = { 60 }, surface = "nauvis" })
check("with coal liquefaction researched, heavy oil still comes from crude oil, not coal and steam",
      liquefied.ok and find(liquefied.r.rows, "heavy-oil").recipe == "advanced-oil-processing" and nowhere(liquefied, "steam")
      and alternatives(liquefied, "heavy-oil") == '["coal-liquefaction"]', liquefied.ok and F.encode(liquefied.r))

-- ── ties: equal recipes go cheapest first ─────────────────────────────
-- The three vanilla solid fuel recipes each make one, each with solid fuel
-- as main product (data/base/prototypes/recipe.lua, 2.0.77), so the name used
-- to decide and heavy oil won. Per fuel, through advanced oil processing:
-- light oil 10 / 45 of a craft, 22.2 crude and 11.1 water; petroleum gas
-- 20 / 55, 54.5 raw units; heavy oil 20 / 25, 120. Mutation-tested: no
-- tie-break; the unobtainable costing 1, not ruled out; mined inputs not
-- costing 1; a depth of 1; no depth bound; no tie in approx; tie in approx
-- with one possible route; the cost order ignored; net output ignored.
for _, source in ipairs({ { "light-oil", 10 }, { "petroleum-gas", 20 }, { "heavy-oil", 20 } }) do
  S.add_recipe({ name = "solid-fuel-from-" .. source[1], category = "chemistry", energy = 1,
                 ingredients = { fluid_in(source[1], source[2]) }, products = { product("solid-fuel", 1) } })
end
local fuel = needs({ force = "player", items = { "solid-fuel" }, per_min = { 60 }, surface = "nauvis" })
check("solid fuel takes the light oil route, the cheapest of three equal recipes",
      fuel.ok and find(fuel.r.rows, "solid-fuel").recipe == "solid-fuel-from-light-oil" and per_min(fuel, "light-oil") == 600,
      fuel.ok and F.encode(fuel.r.rows))
check("about 22 crude oil a fuel: 600 light oil is 13.33 crafts of 100 crude",
      fuel.ok and is(per_min(fuel, "crude-oil", "raw"), 1333.33) and is(per_min(fuel, "water", "raw"), 666.67),
      fuel.ok and F.encode(fuel.r.raw))
check("the others are its alternatives, cheapest first", alternatives(fuel, "solid-fuel")
      == '["solid-fuel-from-petroleum-gas","solid-fuel-from-heavy-oil"]', alternatives(fuel, "solid-fuel"))
check("a tie decided the route, so approx says tie", fuel.ok and has(fuel.r.approx, "tie"), fuel.ok and F.encode(fuel.r.approx))
-- Solid fuel from ammonia, Space Age's (recipe.lua: 15 ammonia and 6 crude
-- oil a fuel), comes first by name. Its ammonia is separated from
-- ammoniacal solution, which only Aquilo's ocean gives, so on Nauvis that
-- route is ruled out; on Aquilo, 21 raw units a fuel, it is the cheapest.
S.add_recipe({ name = "solid-fuel-from-ammonia", category = "chemistry", energy = 0.5,
               ingredients = { fluid_in("ammonia", 15), fluid_in("crude-oil", 6) }, products = { product("solid-fuel", 1) } })
S.add_recipe({ name = "ammoniacal-solution-separation", category = "chemistry", energy = 1,
               ingredients = { fluid_in("ammoniacal-solution", 50) }, products = { product("ice", 5), fluid_out("ammonia", 50) } })
S.add_tile("ammoniacal-ocean", "ammoniacal-solution")
S.add_surface({ name = "aquilo", places = { entity = { "crude-oil" }, tile = { "ammoniacal-ocean" } } })
local fuel_nauvis = needs({ force = "player", items = { "solid-fuel" }, per_min = { 60 }, surface = "nauvis" })
check("on Nauvis the ammonia route is ruled out: no ammoniacal solution there",
      fuel_nauvis.ok and find(fuel_nauvis.r.rows, "solid-fuel").recipe == "solid-fuel-from-light-oil"
      and nowhere(fuel_nauvis, "ammoniacal-solution"), fuel_nauvis.ok and F.encode(fuel_nauvis.r))
check("and it is last among the alternatives", alternatives(fuel_nauvis, "solid-fuel")
      == '["solid-fuel-from-petroleum-gas","solid-fuel-from-heavy-oil","solid-fuel-from-ammonia"]', alternatives(fuel_nauvis, "solid-fuel"))
local fuel_aquilo = needs({ force = "player", items = { "solid-fuel" }, per_min = { 60 }, surface = "aquilo" })
check("on Aquilo the ammonia route is the cheapest: 900 ammoniacal solution and 360 crude oil",
      fuel_aquilo.ok and find(fuel_aquilo.r.rows, "solid-fuel").recipe == "solid-fuel-from-ammonia"
      and per_min(fuel_aquilo, "ammoniacal-solution", "raw") == 900 and per_min(fuel_aquilo, "crude-oil", "raw") == 360,
      fuel_aquilo.ok and F.encode(fuel_aquilo.r))
-- A tie whose route loops back on itself still ends: the cost walk stops
-- recipe_cost.DEPTH levels down. test-loop-a takes two feed, and feed is
-- made from the item itself, so its cost doubles a level at a time; one
-- copper plate is cheaper.
S.add_recipe({ name = "test-loop-a", ingredients = { input("test-loop-feed", 2) }, products = { product("test-loop-item", 1) } })
S.add_recipe({ name = "test-loop-b", ingredients = { input("copper-plate", 1) }, products = { product("test-loop-item", 1) } })
S.add_recipe({ name = "test-loop-feed", ingredients = { input("test-loop-item", 1) }, products = { product("test-loop-feed", 1) } })
local loop = needs({ force = "player", items = { "test-loop-item" }, per_min = { 60 }, surface = "nauvis" })
check("a tie through a loop is costed, bounded, and the cheaper route wins",
      loop.ok and find(loop.r.rows, "test-loop-item") ~= nil and find(loop.r.rows, "test-loop-item").recipe == "test-loop-b"
      and per_min(loop, "copper-plate") == 60 and has(loop.r.approx, "tie"), F.encode(loop))
-- One possible route among tied ones is no real choice: no tie in approx.
for _, name in ipairs({ "solid-fuel-from-light-oil", "solid-fuel-from-petroleum-gas", "solid-fuel-from-heavy-oil" }) do
  S.set_recipe("player", name, { enabled = false })
end
S.add_recipe({ name = "solid-fuel-from-unobtainium", category = "chemistry", energy = 1,
               ingredients = { input("test-unobtainium", 1) }, products = { product("solid-fuel", 1) } })
local fuel_one = needs({ force = "player", items = { "solid-fuel" }, per_min = { 60 }, surface = "aquilo" })
check("a tie with one possible route takes it and says nothing of a tie",
      fuel_one.ok and find(fuel_one.r.rows, "solid-fuel").recipe == "solid-fuel-from-ammonia" and not has(fuel_one.r.approx, "tie"),
      fuel_one.ok and F.encode(fuel_one.r))
S.set_recipe("player", "solid-fuel-from-unobtainium", { enabled = false })
for _, name in ipairs({ "solid-fuel-from-light-oil", "solid-fuel-from-petroleum-gas", "solid-fuel-from-heavy-oil" }) do
  S.set_recipe("player", name, { enabled = true })
end

-- ── a machine's own productivity ──────────────────────────────────────
-- The electromagnetic plant carries +50% of its own (2.0.77 and 2.1.17
-- effect_receiver.base_effect.productivity = 0.5), added to research before
-- the recipe's cap: measured with real machines, it applies whatever the
-- recipe's allowed_effects say. A test recipe it alone makes. Mutation-
-- tested: no machine bonus; base productivity not read; the bonus
-- multiplied, not added; no cap; the bonus left out of a machine count;
-- productivity left at research; net left at research on the machine.
S.add_crafter({ "electromagnetic-plant", "assembling-machine", 2, { "electromagnetics" }, { productivity = 0.5 } })
S.add_recipe({ name = "electromagnetic-plant", ingredients = { input("steel-plate", 50) }, products = { product("electromagnetic-plant", 1) } })
S.add_recipe({ name = "test-coil", category = "electromagnetics", ingredients = { input("copper-plate", 1) },
               products = { product("test-coil", 1) } })
local coil = needs({ force = "player", items = { "test-coil" }, per_min = { 60 }, surface = "nauvis" })
check("a machine's own +50% is counted: 60 coils take 40 copper plate",
      coil.ok and per_min(coil, "copper-plate") == 40 and find(coil.r.rows, "test-coil").machine == "electromagnetic-plant",
      coil.ok and F.encode(coil.r.rows))
check("40 crafts of 0.5 s at speed 2 is 0.167 machines", coil.ok and is(find(coil.r.rows, "test-coil").machines, 0.167),
      coil.ok and F.encode(find(coil.r.rows, "test-coil")))
check("and the reply says a machine bonus is in", coil.ok and coil.r.productivity == "research+machine", coil.ok and coil.r.productivity)
check("a tree no such machine makes still says research", red.ok and red.r.productivity == "research", red.ok and red.r.productivity)
S.set_recipe("player", "test-coil", { productivity_bonus = 0.25 })
local coil_research = needs({ force = "player", items = { "test-coil" }, per_min = { 60 }, surface = "nauvis" })
check("research and machine add: +75%, 34.29 copper plate", coil_research.ok and is(per_min(coil_research, "copper-plate"), 34.29),
      coil_research.ok and F.encode(coil_research.r.rows))
S.set_recipe("player", "test-coil", { productivity_bonus = 2.8 })
local coil_capped = needs({ force = "player", items = { "test-coil" }, per_min = { 60 }, surface = "nauvis" })
check("the sum is capped at maximum_productivity: +300%, 15 copper plate", coil_capped.ok and per_min(coil_capped, "copper-plate") == 15,
      coil_capped.ok and F.encode(coil_capped.r.rows))
S.set_recipe("player", "test-coil", { productivity_bonus = 0 })
-- A recipe whose allowed_effects forbid productivity still gets the
-- machine's own: measured, an electromagnetic plant made 18 big electric
-- poles from 12 crafts' ingredients on 2.0.77 and on 2.1.17.
S.add_recipe({ name = "test-coil-pole", category = "electromagnetics", ingredients = { input("copper-plate", 1) },
               products = { product("test-coil-pole", 1) },
               allowed_effects = { productivity = false, speed = true, consumption = true, pollution = true, quality = true } })
local pole = needs({ force = "player", items = { "test-coil-pole" }, per_min = { 60 }, surface = "nauvis" })
check("allowed_effects does not gate a machine's own productivity: 40 copper plate for 60",
      pole.ok and per_min(pole, "copper-plate") == 40 and pole.r.productivity == "research+machine", pole.ok and F.encode(pole.r.rows))
local coil_count = needs({ force = "player", items = { "test-coil" }, machines = { 1 }, surface = "nauvis" })
check("one plant makes 240 crafts a minute, 360 coils with its bonus", coil_count.ok and per_min(coil_count, "test-coil") == 360
      and is(coil_count.r.targets[1].per_min, 360) and per_min(coil_count, "copper-plate") == 240, coil_count.ok and F.encode(coil_count.r))

-- ── the reply fits rows and their alternatives together ───────────────
-- Forty inputs of one target, each with a second recipe, so every row
-- brings an alternative. Setting every alternative aside before fitting a
-- row left all twelve science packs on an endgame force with 0 of 65 rows
-- and 1174 bytes unused. Now the rows stop only where one more, with its
-- alternative, would not fit. Mutation-tested: fitting 400 bytes short of
-- the budget; one row short; every alternative kept; no budget at all.
local alt_wide = {}
for i = 1, 40 do
  local name = string.format("test-alt-%02d", i)
  alt_wide[i] = input(name, 1)
  S.add_recipe({ name = name, ingredients = { input("iron-plate", 1) }, products = { product(name, 1) } })
  S.add_recipe({ name = name .. "-b", ingredients = { input("copper-plate", 1) }, products = { product(name, 1) } })
end
S.add_recipe({ name = "test-alt-wide", ingredients = alt_wide, products = { product("test-alt-wide", 1) } })
local fitted = needs({ force = "player", items = { "test-alt-wide" }, per_min = { 60 }, surface = "nauvis" })
local fitted_bytes = fitted.ok and #F.encode(fitted.r) or 0
local last = fitted.ok and fitted.r.rows[#fitted.r.rows] or {}
local next_row = #F.encode(last) + #F.encode({ [last.item or "?"] = alternatives_for(fitted, last.item) or {} }) - 2 + 2
check("rows are cut, each shown one with its alternative", fitted.ok and fitted.r.shown < fitted.r.total and fitted.r.shown == #fitted.r.rows
      and last.item ~= nil and alternatives_for(fitted, last.item) ~= nil, fitted.ok and (fitted.r.shown .. " of " .. fitted.r.total))
check("within the budget (" .. fitted_bytes .. " bytes)", fitted_bytes <= 4000, fitted_bytes)
check("and full: one more row of the same width and its alternative would not fit",
      4000 - fitted_bytes < next_row, (4000 - fitted_bytes) .. " bytes unused, a row takes " .. next_row)

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
