-- AI Agent Bridge - tests/lua/aab_sweep_review_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The review fixes to the sweep tool: T5 (prune before predicting, refusal
-- text per metric), T3 (robots counts both kinds), T10 (researched, and the
-- research metric's unit), F6 (item_used). Driven through the aab-rpc call
-- op against the fake game, the same way aab_stage5_metrics_test.lua does.

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
local function sweep(args) return call("sweep", args) end
local function row_at(reply, n) return reply.ok and (reply.r.rows or {})[n] or nil end
local function row_eq(row, name, value)
  return type(row) == "table" and row[1] == name and tonumber(row[2]) == value
end

-- ── a big server: 40 teams, 16 platforms ───────────────────────────────
-- Each synthetic team has made nothing anywhere, has no network and no
-- train, and every surface beyond nauvis is a platform it does not own.
local zero_stats = {
  get_input_count = function() return 0 end, get_output_count = function() return 0 end,
  get_flow_count = function() return 0 end, input_counts = {}, output_counts = {},
}
local made_everywhere = false
local busy_stats = {
  get_input_count = function() return made_everywhere and 5 or 0 end,
  get_output_count = function() return 0 end,
  get_flow_count = function() return 10 end, input_counts = {}, output_counts = {},
}
local function team(name)
  local f = {
    name = name, valid = true, players = { { name = name .. "-p", connected = true, valid = true } },
    connected_players = {}, logistic_networks = {}, technologies = {},
    get_item_production_statistics = function() return busy_stats end,
    get_fluid_production_statistics = function() return zero_stats end,
    get_evolution_factor = function() return 0.1 end, get_evolution_factor_by_time = function() return 0.1 end,
    get_evolution_factor_by_pollution = function() return 0 end,
    get_evolution_factor_by_killing_spawners = function() return 0 end,
    get_entity_count = function() return 0 end,
  }
  return f
end
for i = 1, 40 do
  local name = string.format("team-%02d", i)
  game.forces[name] = team(name)
end
local first_platform = #S.surfaces + 1
for i = 1, 16 do
  local surface = { name = string.format("ship-%02d", i), index = #S.surfaces + 1, valid = true,
                    platform = { name = "ship", valid = true, force = game.forces["team-01"], scheduled_for_deletion = 0 } }
  rawset(S.surfaces, surface.index, surface)
  getmetatable(S.surfaces).__index[surface.name] = surface
end
-- 41 forces with players on 18 surfaces: 738 cells before any pruning.

-- ══ T5: prune, then predict ═════════════════════════════════════════════
local rate = sweep({ metric = "item_rate", subject = "iron-plate", axis = "force" })
check("T5 item_rate by force answers 41 teams on 18 surfaces: only player's nauvis has iron plate",
      rate.ok and rate.r.found == nil and row_eq(row_at(rate, 1), "player", 90) and rate.r.total == 41,
      rate.ok and (rate.r.reason or F.encode(rate.r.rows and row_at(rate, 1))))
local rate_cells = sweep({ metric = "item_rate", subject = "iron-plate" })
check("T5 item_rate by force+surface reads the one cell that has the item",
      rate_cells.ok and rate_cells.r.total == 1 and row_eq(row_at(rate_cells, 1), "player on nauvis", 90),
      F.encode(rate_cells.r))
made_everywhere = true
local refused = sweep({ metric = "item_rate", subject = "iron-plate", axis = "force" })
check("T5 a sweep still past the budget after pruning is refused: 40 x 18 cells made it, 720 + 1",
      refused.ok and refused.r.found == false and refused.r.passes == 721, F.encode(refused.r))
check("T5 the refusal names asks that pass, not the force axis that costs the same",
      refused.ok and tostring(refused.r.reason):find("item_made", 1, true)
      and not tostring(refused.r.reason):find("force axis", 1, true), refused.ok and refused.r.reason)
made_everywhere = false
local fluid = sweep({ metric = "fluid_rate", subject = "crude-oil" })
check("T5 fluid_rate answers: crude oil only where player ever pumped it, 90 + 10",
      fluid.ok and fluid.r.found == nil and row_eq(row_at(fluid, 1), "player", 100), F.encode(fluid.r))
local robots = sweep({ metric = "robots" })
check("T5 robots answers: only player has a network, on nauvis",
      robots.ok and robots.r.found == nil and row_eq(row_at(robots, 1), "player", 165) and robots.r.total == 41,
      F.encode(robots.r))
local networks = sweep({ metric = "networks", axis = "force+surface" })
check("T5 networks by force+surface reads only the surfaces with a network",
      networks.ok and networks.r.total == 1 and row_eq(row_at(networks, 1), "player on nauvis", 2),
      F.encode(networks.r))
local evolution = sweep({ metric = "evolution" })
check("T5 evolution answers: platforms are left out, 41 forces on nauvis alone",
      evolution.ok and evolution.r.found == nil and evolution.r.total == 41, F.encode(evolution.r))
local trains = sweep({ metric = "trains" })
check("T5 trains answers 41 forces with one get_trains read per force",
      trains.ok and trains.r.found == nil and row_eq(row_at(trains, 1), "player", 4), F.encode(trains.r))

-- ══ T3: the robots metrics ══════════════════════════════════════════════
local logistic = sweep({ metric = "logistic_robots" })
local construction = sweep({ metric = "construction_robots" })
check("T3 logistic_robots ranks player at 125", logistic.ok and row_eq(row_at(logistic, 1), "player", 125),
      F.encode(logistic.r))
check("T3 construction_robots ranks player at 40", construction.ok and row_eq(row_at(construction, 1), "player", 40),
      F.encode(construction.r))
check("T3 robots counts both kinds: 165", robots.ok and row_eq(row_at(robots, 1), "player", 165), F.encode(robots.r))

-- ══ T10: research ranks the current technology only; researched counts ══
local research = sweep({ metric = "research" })
check("T10 the research metric's unit says it is the current technology, not overall progress",
      research.ok and tostring(research.r.unit):find("current", 1, true), research.ok and research.r.unit)
-- player has researched automation; every synthetic team has nothing.
game.forces["team-01"].technologies = { a = { researched = true }, b = { researched = true }, c = { researched = false } }
local researched = sweep({ metric = "researched" })
check("T10 researched counts researched technologies: team-01 2, player 1",
      researched.ok and row_eq(row_at(researched, 1), "team-01", 2) and row_eq(row_at(researched, 2), "player", 1),
      F.encode(researched.r))

-- ══ F6: item_used, the consumption side of item_made ═══════════════════
-- player used 400 iron plates at normal and 200 at uncommon, on nauvis.
local used = sweep({ metric = "item_used", subject = "iron-plate" })
check("F6 item_used ranks player at 600 iron plates used",
      used.ok and row_eq(row_at(used, 1), "player", 600) and used.r.axis == "force", F.encode(used.r))
local used_bad = sweep({ metric = "item_used", subject = "no-such-item" })
check("F6 item_used refuses a name that is not an item before any force is asked",
      used_bad.ok and used_bad.r.found == false, F.encode(used_bad.r))
local made = sweep({ metric = "item_made", subject = "iron-plate" })
check("F6 item_made is unchanged: 1500 made", made.ok and row_eq(row_at(made, 1), "player", 1500), F.encode(made.r))

for i = 1, 40 do game.forces[string.format("team-%02d", i)] = nil end
for i = #S.surfaces, first_platform, -1 do
  getmetatable(S.surfaces).__index[S.surfaces[i].name] = nil
  rawset(S.surfaces, i, nil)
end

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
