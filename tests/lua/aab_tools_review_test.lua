-- AI Agent Bridge - tests/lua/aab_tools_review_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The review fixes to the engine tools (T2 paging and the reply budget, T3
-- robots, T4 entity_count passes, T6 window ages, T11 tech_status, T12
-- kills, T13 online players, T14 find_entities, T16 shared labels, F4 lists,
-- F6 tech_status all, F7 single-match names, F9 optional surface), driven
-- through the aab-rpc call op against the fake game the way the stage5
-- suites drive theirs. Every figure is worked out from the fixture by hand.

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
  return F.decode(S.rcon_replies[#S.rcon_replies]), S.rcon_replies[#S.rcon_replies]
end
local function call(fn, args) return rpc({ op = "call", i = "ai-agent-bridge-tools", f = fn, a = args }) end
local function bytes_of(reply) return reply.ok and #F.encode(reply.r) or math.huge end

local bounded = require("scripts.tools.bounded")
-- The service's 4096 cut less a margin; read off bounded where it exists.
local BUDGET = bounded.REPLY_BUDGET or 3800

-- ── synthetic fixtures, added and removed by each section ─────────────
--- A force with nobody's state but what a sweep reads. `fields` overrides.
local function synth_force(name, fields)
  local f = {
    name = name, valid = true, players = { { name = name .. "-p", connected = false } }, connected_players = {},
    rockets_launched = 0, items_launched = {}, platforms = {}, logistic_networks = {},
    current_research = nil, research_progress = 0, technologies = {},
    get_entity_count = function() return 0 end,
    get_kill_count_statistics = function() return { input_counts = {}, output_counts = {} } end,
    get_entity_build_count_statistics = function() return { input_counts = {}, output_counts = {} } end,
    get_fluid_production_statistics = function()
      return { get_flow_count = function() return 0 end }
    end,
  }
  for k, v in pairs(fields or {}) do f[k] = v end
  return f
end

--- Adds a surface to the fake game, a platform owned by `owner` when given.
local function add_surface(name, owner)
  local surface = { name = name, index = #S.surfaces + 1, valid = true }
  surface.count_entities_filtered = function() return 0 end
  if owner then
    surface.platform = { name = name, valid = true, force = owner, scheduled_for_deletion = 0,
                         state = defines.space_platform_state.on_the_path }
  end
  rawset(S.surfaces, surface.index, surface)
  getmetatable(S.surfaces).__index[name] = surface
  return surface
end
local function drop_surfaces(from)
  local index = getmetatable(S.surfaces).__index
  for i = #S.surfaces, from, -1 do
    index[S.surfaces[i].name] = nil
    rawset(S.surfaces, i, nil)
  end
end

-- ══ T2: one reply budget, and pages that reach every row ════════════════
check("T2 the reply budget sits under the service's 4096-byte cut with room for re-escaping",
      bounded.REPLY_BUDGET ~= nil and bounded.REPLY_BUDGET <= 3800 and bounded.REPLY_BUDGET >= 3000, bounded.REPLY_BUDGET)

-- 60 platforms with the long names players give ships: past the budget.
local first_added = #S.surfaces + 1
for i = 1, 60 do add_surface(string.format("Cargo Hauler Mark %02d of the Outer Belt", i), S.force) end
local page1 = call("list_surfaces", { force = "player" })
check("T2 list_surfaces on 62 surfaces fits the budget (" .. bytes_of(page1) .. " bytes)",
      page1.ok and bytes_of(page1) <= BUDGET, bytes_of(page1))
check("T2 a cut list_surfaces says shown, total, offset and next_offset",
      page1.ok and page1.r.total == 62 and page1.r.shown < 62 and page1.r.offset == 0
      and page1.r.next_offset == page1.r.shown, page1.ok and F.encode({ page1.r.total, page1.r.shown, page1.r.offset, page1.r.next_offset }))
-- Every page in turn: together they hold every surface once, in order.
local seen, order_ok, offset, pages = {}, true, 0, 0
while offset and pages < 20 do
  local page = call("list_surfaces", { force = "player", offset = offset })
  if not page.ok then break end
  for _, row in ipairs(page.r.surfaces) do
    if seen[row.name] then order_ok = false end
    seen[row.name] = true
  end
  offset, pages = page.r.next_offset, pages + 1
end
local seen_count = 0
for _ in pairs(seen) do seen_count = seen_count + 1 end
check("T2 paging list_surfaces by next_offset reaches all 62 surfaces once each",
      seen_count == 62 and order_ok and pages > 1, seen_count .. " in " .. pages .. " pages")
local platforms_only = call("list_surfaces", { force = "player", kind = "platforms" })
local all_platforms = platforms_only.ok
for _, row in ipairs(platforms_only.ok and platforms_only.r.surfaces or {}) do
  if not row.platform then all_platforms = false end
end
check("T2 kind=platforms lists platforms alone, 61 of them",
      all_platforms and platforms_only.r.total == 61, platforms_only.ok and platforms_only.r.total)
local planets_only = call("list_surfaces", { force = "player", kind = "planets" })
check("T2 kind=planets lists nauvis alone",
      planets_only.ok and planets_only.r.total == 1 and planets_only.r.surfaces[1].name == "nauvis",
      F.encode(planets_only))
local small = call("list_surfaces", { force = "player", kind = "planets" })
check("T2 an uncut page carries no offset or next_offset",
      small.ok and small.r.offset == nil and small.r.next_offset == nil, F.encode(small))
drop_surfaces(first_added)

-- 120 forces researching: current_research all is byte-fitted and paged.
local tech = { name = "a-technology-with-a-long-modded-name", level = 1 }
for i = 1, 120 do
  game.forces[string.format("team-with-long-name-%03d", i)] =
    synth_force(string.format("team-with-long-name-%03d", i), { current_research = tech, research_progress = 0.5 })
end
local research_all = call("current_research", { all = true })
check("T2 current_research all on 121 forces fits the budget (" .. bytes_of(research_all) .. " bytes)",
      research_all.ok and bytes_of(research_all) <= BUDGET and research_all.r.shown > 10
      and research_all.r.next_offset == research_all.r.shown, research_all.ok and research_all.r.shown or F.encode(research_all))
local research_2 = call("current_research", { all = true, offset = research_all.ok and research_all.r.next_offset or 0 })
check("T2 current_research all offset starts the next page where the last stopped",
      research_2.ok and research_2.r.offset == research_all.r.shown and research_2.r.forces[1].force
      ~= research_all.r.forces[1].force, F.encode(research_2.r and research_2.r.forces and research_2.r.forces[1]))
local forces_page = call("list_forces", { force = "player", offset = 100 })
check("T2 list_forces takes an offset",
      forces_page.ok and forces_page.r.offset == 100 and forces_page.r.forces[1] ~= nil
      and bytes_of(forces_page) <= BUDGET, F.encode(forces_page.r and forces_page.r.offset))
local players_all = call("list_players", { all = true, connected = false })
check("T2 list_players all is byte-fitted and paged",
      players_all.ok and bytes_of(players_all) <= BUDGET and players_all.r.total == 122
      and players_all.r.next_offset ~= nil, players_all.ok and F.encode({ players_all.r.total, players_all.r.shown }))
local players_tail = call("list_players", { all = true, connected = false, offset = 120 })
check("T2 list_players all offset 120 shows the last two",
      players_tail.ok and players_tail.r.shown == 2 and players_tail.r.next_offset == nil, F.encode(players_tail))
for _, path in ipairs({ { "kills", { all = true } }, { "built", { all = true } }, { "trains", { all = true } },
                        { "fluid_rate", { all = true, fluid = "crude-oil", window = "one_minute" } },
                        { "rockets", { all = true } }, { "entity_count", { all = true, name = "lab" } } }) do
  local name, args = path[1], path[2]
  local first = call(name, args)
  args.offset = first.ok and first.r.next_offset or 0
  local second = call(name, args)
  check("T2 " .. name .. " all pages: within budget, next_offset leads on",
        first.ok and bytes_of(first) <= BUDGET and first.r.total == 121 and first.r.next_offset
        and second.ok and second.r.offset == first.r.next_offset,
        first.ok and F.encode({ first.r.total, first.r.shown, first.r.next_offset }) or F.encode(first))
end
for i = 1, 120 do game.forces[string.format("team-with-long-name-%03d", i)] = nil end

-- locate_player all with many connected players: fitted, not refused whole.
local saved_connected = game.connected_players
local crowd = {}
for i = 1, 80 do
  crowd[i] = { name = string.format("player-with-a-long-name-%02d", i), valid = true, connected = true,
               surface = game.surfaces.nauvis, physical_surface = game.surfaces.nauvis,
               position = { x = i, y = i }, physical_position = { x = i, y = i } }
end
game.connected_players = crowd
local located = call("locate_player", { all = true })
check("T2 locate_player all on 80 players fits the budget and pages",
      located.ok and bytes_of(located) <= BUDGET and located.r.total == 80
      and located.r.next_offset == located.r.shown, located.ok and F.encode({ located.r.total, located.r.shown }) or F.encode(located))
game.connected_players = saved_connected

-- ══ T13: online players beside every player the force has had ══════════
local surfaces_now = call("list_surfaces", { force = "player" })
local nauvis_row
for _, row in ipairs(surfaces_now.ok and surfaces_now.r.surfaces or {}) do
  if row.name == "nauvis" then nauvis_row = row end
end
-- Bob is connected and his controller is on nauvis; Ann is offline on nauvis.
check("T13 force_players still counts every player the force has had on nauvis",
      nauvis_row and nauvis_row.force_players == 2, F.encode(nauvis_row))
check("T13 force_players_online counts the connected ones only",
      nauvis_row and nauvis_row.force_players_online == 1, F.encode(nauvis_row))

-- ══ T16: a label two forces share is ambiguous, never a coin toss ═══════
local labels = require("scripts.labels")
local saved_map = labels.cached_map
labels.cached_map = function() return { ["team-3"] = "Red Team", player = "red team" } end
local ambiguous = call("game_time", { force = "Red Team" })
check("T16 a label two forces share is refused and names both",
      ambiguous.ok == false and tostring(ambiguous.m):find("player", 1, true)
      and tostring(ambiguous.m):find("team-3", 1, true), F.encode(ambiguous))
labels.cached_map = function() return { ["team-3"] = "Blue Team" } end
local unique = call("game_time", { force = "blue team" })
check("T16 a label one force holds still resolves", unique.ok, F.encode(unique))
labels.cached_map = saved_map

-- ══ T3: robots counts both kinds ═════════════════════════════════════════
-- Fixture networks on nauvis: 120 + 5 logistic, 40 + 0 construction.
local logi = call("logistics_summary", { force = "player", surface = "nauvis" })
check("T3 logistic_robots_total keeps its meaning: 125", logi.ok and logi.r.logistic_robots_total == 125, F.encode(logi))
check("T3 construction_robots_total is 40", logi.ok and logi.r.construction_robots_total == 40, F.encode(logi))
check("T3 robots_total is both: 165", logi.ok and logi.r.robots_total == 165, F.encode(logi))

-- ══ T4: per_surface refuses on the passes it would really run ════════════
-- 29 forces with players on 30 surfaces is 870 passes before pruning, past
-- 600. Only player owns any lab, and 28 of the surfaces are platforms other
-- forces own: player's passes are nauvis and platform-1, two in all.
local t4_from = #S.surfaces + 1
for i = 1, 28 do
  local owner = synth_force(string.format("t4-%02d", i))
  game.forces[owner.name] = owner
  add_surface(string.format("t4-ship-%02d", i), owner)
end
local per_surface = call("entity_count", { all = true, per_surface = true, name = "lab" })
check("T4 per_surface answers when the forces that own the entity need few passes",
      per_surface.ok and per_surface.r.found == true and per_surface.r.forces[1].count == 12, F.encode(per_surface))
for i = 1, 28 do game.forces[string.format("t4-%02d", i)] = nil end
drop_surfaces(t4_from)

-- ══ T6: a rate window longer than the map says so ══════════════════════
-- The fake map is 100000 ticks old; one_hour is 216000 ticks.
local hour = call("item_rate", { force = "player", surface = "nauvis", item = "iron-plate", window = "one_hour" })
check("T6 item_rate names its window and the map's age in ticks",
      hour.ok and hour.r.window_ticks == 216000 and hour.r.map_age_ticks == 100000, F.encode(hour.r))
check("T6 a window longer than the map is marked partial_window, never rescaled",
      hour.ok and hour.r.partial_window == true and tonumber(hour.r.produced_per_min) == 90, F.encode(hour.r))
local minute = call("item_rate", { force = "player", surface = "nauvis", item = "iron-plate", window = "one_minute" })
check("T6 a window the map covers carries no partial_window",
      minute.ok and minute.r.partial_window == nil and minute.r.window_ticks == 3600, F.encode(minute.r))
local fluid_hour = call("fluid_rate", { force = "player", surface = "nauvis", fluid = "crude-oil", window = "one_hour" })
check("T6 fluid_rate marks a partial window too",
      fluid_hour.ok and fluid_hour.r.partial_window == true and fluid_hour.r.map_age_ticks == 100000, F.encode(fluid_hour.r))
local top_hour = call("top_items", { force = "player", surface = "nauvis", window = "one_hour", n = 3 })
check("T6 top_items marks a partial window too",
      top_hour.ok and top_hour.r.partial_window == true and top_hour.r.window_ticks == 216000, F.encode(top_hour.r))

-- ══ T7: production_since no longer promises "exactly" ═══════════════════
local manifest = rpc({ op = "manifest", i = "ai-agent-bridge-tools" }).r.tools
check("T7 production_since's desc drops exactly and says totals cover surfaces that still exist",
      not manifest.production_since.desc:find("exactly", 1, true)
      and manifest.production_since.desc:find("still exist", 1, true) ~= nil, manifest.production_since.desc)

-- ══ T11: tech_status tells researched from available, and the cost ═════
local techs = S.technologies
techs.automation.research_unit_ingredients = { { name = "automation-science-pack", amount = 1 } }
techs.automation.research_unit_energy = 600
techs["logistics-2"].research_unit_ingredients = { { name = "automation-science-pack", amount = 1 },
                                                   { name = "logistic-science-pack", amount = 1 } }
techs["logistics-2"].research_unit_energy = 1800
techs["logistics-2"].prototype = { research_trigger = nil }
local done = call("tech_status", { force = "player", tech = "automation" })
check("T11 a researched technology is not available", done.ok and done.r.researched == true and done.r.available == false,
      F.encode(done.r))
local cost = call("tech_status", { force = "player", tech = "logistics-2" })
check("T11 tech_status lists the packs per unit, the units and the seconds per unit",
      cost.ok and cost.r.units == 200 and cost.r.unit_time == 30 and #cost.r.ingredients == 2
      and cost.r.ingredients[2].name == "logistic-science-pack" and cost.r.ingredients[2].amount == 1, F.encode(cost.r))
techs.logistics.prototype = { research_trigger = { type = "craft-item", item = "iron-gear-wheel", count = 10 } }
local trigger = call("tech_status", { force = "player", tech = "logistics" })
check("T11 a trigger technology says its trigger type and sends no units",
      trigger.ok and trigger.r.research_trigger == "craft-item" and trigger.r.units == nil, F.encode(trigger.r))

-- ══ F6: tech_status for every force in one call ═════════════════════════
for i = 1, 70 do
  game.forces[string.format("tech-team-%02d", i)] = synth_force(string.format("tech-team-%02d", i), {
    technologies = { ["logistics-2"] = { name = "logistics-2", researched = i % 2 == 0, level = 1, saved_progress = 0.5 } },
  })
end
local tech_all = call("tech_status", { tech = "logistics-2", all = true })
check("F6 tech_status all lists every force with players, byte-fitted and paged",
      tech_all.ok and tech_all.r.total == 71 and tech_all.r.next_offset ~= nil
      and bytes_of(tech_all) <= BUDGET, tech_all.ok and F.encode({ tech_all.r.total, tech_all.r.shown }) or F.encode(tech_all))
local first_row
for _, row in ipairs(tech_all.ok and tech_all.r.forces or {}) do
  if row.force == "tech-team-02" then first_row = row end
end
check("F6 a tech_status all row carries researched, level and progress",
      first_row and first_row.researched == true and first_row.level == 1, F.encode(first_row))
local tech_tail = call("tech_status", { tech = "logistics-2", all = true, offset = 70 })
check("F6 tech_status all pages to the last row", tech_tail.ok and tech_tail.r.shown == 1 and tech_tail.r.offset == 70,
      F.encode(tech_tail))
for i = 1, 70 do game.forces[string.format("tech-team-%02d", i)] = nil end

-- ══ T12: kills says what it counted ═════════════════════════════════════
-- player killed 45 small biters and 10 medium ones; add a tree and a rock.
local saved_entities = prototypes.entity
prototypes.entity = setmetatable({
  ["small-biter"] = { name = "small-biter", type = "unit" }, ["medium-biter"] = { name = "medium-biter", type = "unit" },
  ["tree-01"] = { name = "tree-01", type = "tree" }, ["big-rock"] = { name = "big-rock", type = "simple-entity" },
  ["car"] = { name = "car", type = "car" },
}, { __index = saved_entities })
S.kill_counts.player.nauvis.input_counts["tree-01"] = 30
S.kill_counts.player.nauvis.input_counts["big-rock"] = 4
S.kill_counts.player.nauvis.input_counts["car"] = 1
local killed = call("kills", { force = "player" })
check("T12 kills keeps its total: 45 + 10 + 30 + 4 + 1 = 90", killed.ok and killed.r.kills == 90, F.encode(killed.r))
check("T12 kills splits enemies (55), trees_and_rocks (34) and other (1)",
      killed.ok and killed.r.enemies == 55 and killed.r.trees_and_rocks == 34 and killed.r.other == 1, F.encode(killed.r))
S.kill_counts.player.nauvis.input_counts["tree-01"] = nil
S.kill_counts.player.nauvis.input_counts["big-rock"] = nil
S.kill_counts.player.nauvis.input_counts["car"] = nil
prototypes.entity = saved_entities

-- ══ T14: idle furnaces, and fluids among the suggestions ═══════════════
prototypes.recipe["iron-plate"] = { name = "iron-plate", products = { { type = "item", name = "iron-plate", amount = 1 } } }
local idle = { name = "stone-furnace", type = "furnace", valid = true, position = { x = 40, y = 40 },
               force = { name = "player", valid = true }, get_recipe = function() return nil end,
               previous_recipe = { name = "iron-plate", quality = "normal" } }
S.entities[#S.entities + 1] = idle
local furnaces = call("find_entities", { force = "player", surface = "nauvis", recipe = "iron-plate" })
local found_idle = false
for _, row in ipairs(furnaces.ok and furnaces.r.entities or {}) do
  if row.x == 40 and row.idle == true then found_idle = true end
end
check("T14 a recipe search finds an idle furnace by its previous recipe, marked idle",
      found_idle and furnaces.r.total == 2, F.encode(furnaces.r))
S.entities[#S.entities] = nil
prototypes.recipe["iron-plate"] = nil
local fluid_guess = call("find_entities", { force = "player", surface = "nauvis", product = "crude-gas" })
check("T14 a product miss suggests fluids too", fluid_guess.ok and fluid_guess.r.found == false
      and table.concat(fluid_guess.r.suggestions or {}, ","):find("crude-oil", 1, true) ~= nil, F.encode(fluid_guess.r))

-- ══ F4: lists instead of one name per call ══════════════════════════════
prototypes.item["copper-plate"] = { name = "copper-plate" }
local both = call("production_since", { force = "player", items = { "iron-plate", "copper-plate" }, since_tick = 0 })
prototypes.item["copper-plate"] = nil
local iron_row, copper_row
for _, row in ipairs(both.ok and both.r.items or {}) do
  if row.item == "iron-plate" then iron_row = row elseif row.item == "copper-plate" then copper_row = row end
end
check("F4 production_since items: one row per item, iron 1500 made 600 used, copper 750",
      iron_row and iron_row.produced == 1500 and iron_row.consumed == 600 and copper_row and copper_row.produced == 750,
      F.encode(both))
local one_old = call("production_since", { force = "player", item = "iron-plate", since_tick = 0 })
check("F4 production_since with item alone keeps its shape: no items list",
      one_old.ok and one_old.r.items == nil and one_old.r.produced == 1500, F.encode(one_old.r))
local too_many = call("production_since", { force = "player", since_tick = 0,
  items = { "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m" } })
check("F4 production_since refuses more than 12 items", too_many.ok == false, F.encode(too_many))
local counted = call("entity_count", { force = "player", surface = "nauvis", names = { "lab", "assembling-machine-2" } })
local lab_row
for _, row in ipairs(counted.ok and counted.r.names or {}) do if row.name == "lab" then lab_row = row end end
check("F4 entity_count names: per name and a total, 12 labs + 340 assemblers",
      counted.ok and lab_row and lab_row.count == 12 and counted.r.count == 352, F.encode(counted.r))
local one_count = call("entity_count", { force = "player", surface = "nauvis", name = "lab" })
check("F4 entity_count with name alone is unchanged",
      one_count.ok and one_count.r.count == 12 and one_count.r.names == nil, F.encode(one_count.r))
prototypes.entity.lab.type = "lab"
prototypes.entity["assembling-machine-2"].type = "assembling-machine"
prototypes.get_entity_filtered = function(filters)
  local out = {}
  for name, proto in pairs(prototypes.entity) do
    if proto.type == filters[1].type then out[name] = proto end
  end
  return out
end
local by_type = call("entity_count", { all = true, type = "assembling-machine" })
check("F4 entity_count all=true type counts every name of that type per force",
      by_type.ok and by_type.r.forces and by_type.r.forces[1].force == "player" and by_type.r.forces[1].count == 340,
      F.encode(by_type.r))
local found_two = call("find_entities", { force = "player", surface = "nauvis", names = { "lab", "assembling-machine-2" }, limit = 10 })
check("F4 find_entities names finds labs and assemblers together: 2 + 2 built + 2 ghosts",
      found_two.ok and found_two.r.total == 6, F.encode(found_two.r and found_two.r.total))
prototypes.get_entity_filtered = nil
prototypes.entity.lab.type = nil
prototypes.entity["assembling-machine-2"].type = nil

-- ══ F9: item_rate and top_items with no surface ═════════════════════════
S.lifetime_surfaces = { nauvis = true, ["platform-1"] = true }
local everywhere = call("item_rate", { force = "player", item = "iron-plate", window = "one_minute" })
check("F9 item_rate with no surface totals every surface the force has the item on: 90 + 90",
      everywhere.ok and tonumber(everywhere.r.produced_per_min) == 180 and everywhere.r.surfaces_covered == 2
      and everywhere.r.surface == "all" and #everywhere.r.surfaces == 2, F.encode(everywhere.r))
S.lifetime_surfaces = nil
local nauvis_only = call("item_rate", { force = "player", item = "iron-plate", window = "one_minute" })
check("F9 a surface the force never made the item on is not read",
      nauvis_only.ok and nauvis_only.r.surfaces_covered == 1 and tonumber(nauvis_only.r.produced_per_min) == 90,
      F.encode(nauvis_only.r))
local top_all = call("top_items", { force = "player", window = "one_minute", n = 5 })
check("F9 top_items with no surface sums the surfaces it covers",
      top_all.ok and top_all.r.surface == "all" and top_all.r.surfaces_covered >= 1 and top_all.r.items[1] ~= nil,
      F.encode(top_all.r))

-- ══ F7: one close name is used, and the reply says so ═══════════════════
prototypes.item["pm-gold-ore"] = { name = "pm-gold-ore" }
local gold = call("production_since", { force = "player", item = "gold-ore", since_tick = 0 })
check("F7 production_since resolves gold-ore to its one close match pm-gold-ore",
      gold.ok and gold.r.item == "pm-gold-ore" and gold.r.resolved_from and gold.r.resolved_from.asked == "gold-ore"
      and gold.r.resolved_from.used == "pm-gold-ore", F.encode(gold))
local gold_rate = call("item_rate", { force = "player", surface = "nauvis", item = "gold-ore", window = "one_minute" })
check("F7 item_rate resolves the same way", gold_rate.ok and gold_rate.r.item == "pm-gold-ore"
      and gold_rate.r.resolved_from.used == "pm-gold-ore", F.encode(gold_rate))
prototypes.item["pm-gold-plate-ore"] = { name = "pm-gold-plate-ore" }
local two = call("production_since", { force = "player", item = "gold-ore", since_tick = 0 })
check("F7 with two close matches nothing is guessed: found=false with both suggested",
      two.ok and two.r.found == false and #(two.r.suggestions or {}) == 2, F.encode(two))
prototypes.item["pm-gold-ore"], prototypes.item["pm-gold-plate-ore"] = nil, nil
local labs = call("entity_count", { force = "player", surface = "nauvis", name = "labs" })
check("F7 entity_count resolves a plural to the one entity it names",
      labs.ok and labs.r.count == 12 and labs.r.resolved_from and labs.r.resolved_from.used == "lab", F.encode(labs))
local find_labs = call("find_entities", { force = "player", surface = "nauvis", name = "Labs" })
check("F7 find_entities resolves the same way",
      find_labs.ok and find_labs.r.name == "lab" and find_labs.r.resolved_from.asked == "Labs", F.encode(find_labs))

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
