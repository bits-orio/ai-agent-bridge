-- AI Agent Bridge - tests/lua/aab_force_card_test.lua
-- Author: bits-orio
-- License: MIT
--
-- force_card: one compact summary of one force, so "how is force X doing" is
-- one read. Every figure is worked out from the fixture by hand.

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
local function card(force)
  return rpc({ op = "call", i = "ai-agent-bridge-tools", f = "force_card", a = { force = force } })
end
local bounded = require("scripts.tools.bounded")

-- Kill statistics classify by prototype type, so the fixture's kinds need one.
prototypes.entity["small-biter"] = { name = "small-biter", type = "unit" }
prototypes.entity["medium-biter"] = { name = "medium-biter", type = "unit" }

local reply, raw = card("player")
local r = reply.ok and reply.r or {}
print(raw)
check("FC1 force_card answers found for the player force", reply.ok and r.found == true and r.force == "player", raw)
check("FC1 players: two ever, one online", r.players_total == 2 and r.players_online == 1, raw)
check("FC1 researched counts researched technologies: automation alone", r.researched == 1, raw)
check("FC1 current research and its progress", r.current_research == "logistics" and tonumber(r.research_progress) == 0.25, raw)
check("FC1 queue is the number of queued technologies", r.queue == 2, raw)
check("FC1 rockets launched", r.rockets == 7, raw)
check("FC1 robots: 120 + 5 logistic, 40 construction, 165 in all",
      r.logistic_robots == 125 and r.construction_robots == 40 and r.robots_total == 165, raw)
check("FC1 enemies killed: 40 + 10 on nauvis, 5 on the platform", r.enemies_killed == 55, raw)
-- The fixture's item counters answer for every surface, so nauvis and platform-1 both count.
check("FC1 surfaces counts the two with production statistics", r.surfaces == 2, raw)
-- 60 a minute at normal plus 30 at uncommon is 90 on each of the two surfaces.
check("FC1 top_items: both plates at 180 a minute, name the tiebreak",
      r.top_items and #r.top_items == 2 and r.top_items[1].item == "copper-plate" and r.top_items[1].per_min == 180
        and r.top_items[2].item == "iron-plate" and r.top_items[2].per_min == 180, raw)
check("FC1 window is one_hour", r.window == "one_hour", raw)
check("FC1 a map younger than an hour says partial_window", r.partial_window == true, raw)
check("FC1 nothing was cut, so surfaces_cut is absent", r.surfaces_cut == nil, raw)
check("FC1 the reply fits the reply budget", #raw < bounded.REPLY_BUDGET, #raw)
for key, value in pairs(r) do
  check("FC1 no null-like value under " .. key, value ~= nil and value ~= F.null, key)
end

-- A force with nothing: keys without a value are omitted, not null.
S.team3.technologies = {}  -- the fixture's idle force has no technology table
local empty = card("team-3")
local e = empty.ok and empty.r or {}
check("FC2 an idle force has no current_research key", e.found == true and e.current_research == nil and e.research_progress == nil,
      F.encode(empty))
check("FC2 zero robots stay as numbers", e.robots_total == 0, F.encode(empty))

-- Unknown force errors like the other tools.
local unknown = card("nope")
check("FC3 an unknown force is a provider_error", unknown.ok == false and unknown.e == "provider_error"
      and tostring(unknown.m):find("unknown force"), F.encode(unknown))

-- Bounded: 16 surfaces with production, 12 read, the cut said.
local meta = getmetatable(S.surfaces)
for i = 1, 14 do
  local name = "plat-" .. i
  local surface = { name = name, index = 10 + i, valid = true, planet = nil }
  rawset(S.surfaces, surface.index, surface)
  meta.__index[name] = surface
end
S.lifetime_surfaces = { nauvis = true }
for i = 1, 14 do S.lifetime_surfaces["plat-" .. i] = true end
local wide = card("player")
local w = wide.ok and wide.r or {}
check("FC4 the surface count is the whole count", w.surfaces == 16, F.encode(wide))
check("FC4 more than 12 surfaces says surfaces_cut", w.surfaces_cut == true, F.encode(wide))
check("FC4 the wide reply still fits the budget", #select(2, card("player")) < bounded.REPLY_BUDGET)

-- The manifest names the tool and stays small.
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
local entry = mine.ok and mine.r.tools.force_card
check("FC5 the manifest lists force_card with a one-line desc", entry ~= nil and not entry.desc:find("\n"), F.encode(entry))
check("FC5 force_card declares no params of its own", entry ~= nil and (entry.params == nil or next(entry.params) == nil), F.encode(entry))
print("MANIFEST BYTES " .. #F.encode(mine.r))
check("FC5 the manifest stays under 24576", #F.encode(mine.r) < 24576, #F.encode(mine.r))

print(passes .. " passed, " .. fails .. " failed")
if fails > 0 then os.exit(1) end
