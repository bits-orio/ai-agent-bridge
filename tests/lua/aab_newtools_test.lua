-- AI Agent Bridge - tests/lua/aab_newtools_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The five question-shaped tools (machine_status, power, research_cost, mods,
-- resources) and the two sweep follow-ups (the envelope's `better` field and
-- the enemies_killed metric), driven through the aab-rpc call op against the
-- fake game. Every figure is worked out from the fixture by hand.

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
local function call(fn, args)
  args = args or {}
  args.force = args.force or "player"
  return rpc({ op = "call", i = "ai-agent-bridge-tools", f = fn, a = args })
end
local bounded = require("scripts.tools.bounded")
local BUDGET = bounded.REPLY_BUDGET
local function bytes_of(reply) return reply.ok and #F.encode(reply.r) or math.huge end

local nauvis = S.surfaces.nauvis
local player_force = { name = "player", valid = true }

-- ── fixture helpers ───────────────────────────────────────────────────
local function machine(name, etype, x, y, status)
  return { name = name, type = etype, valid = true, position = { x = x, y = y },
           force = player_force, status = status }
end
--- count_entities_filtered over S.entities honouring name, type, force and
--- limit, which the stock fake does not (it asks for a name string).
local function install_counter()
  nauvis.count_entities_filtered = function(f)
    local n = 0
    for _, e in ipairs(S.entities) do
      local ok = (f.name == nil or f.name == e.name) and (f.type == nil or f.type == e.type)
        and (f.force == nil or e.force.name == f.force)
      if ok then
        n = n + 1
        if f.limit and n >= f.limit then break end
      end
    end
    return n
  end
end
local ES = defines.entity_status

-- ══ manifest ═════════════════════════════════════════════════════════
local mine = rpc({ op = "manifest", i = "ai-agent-bridge-tools" })
for _, n in ipairs({"machine_status","power","research_cost","mods","resources"}) do print("TOOLBYTES " .. n .. " " .. #F.encode(mine.r.tools[n])) end
local tools = mine.ok and mine.r.tools or {}
print("NEWTOOLS manifest op reply bytes: " .. #F.encode(mine.r))
for _, name in ipairs({ "machine_status", "power", "research_cost", "mods", "resources" }) do
  local t = tools[name]
  check("manifest declares " .. name, t ~= nil and type(t.desc) == "string", name)
  local one_line = t and not t.desc:find("\n", 1, true)
  check(name .. " desc is one line", one_line, name)
  for pname in pairs(t and t.params or {}) do
    check(name .. " does not declare the reserved force param", pname ~= "force", pname)
  end
end
check("walking tools declare tier 2",
      tools.machine_status and tools.machine_status.tier == "2" and tools.resources and tools.resources.tier == "2"
      and tools.power and tools.power.tier == "2",
      F.encode({ tools.machine_status, tools.power, tools.resources }))

-- ══ machine_status ═══════════════════════════════════════════════════
S.entities = {
  machine("assembling-machine-2", "assembling-machine", 1, 1, ES.working),
  machine("assembling-machine-2", "assembling-machine", 2, 1, ES.working),
  machine("assembling-machine-2", "assembling-machine", 3, 1, ES.working),
  machine("assembling-machine-2", "assembling-machine", 4, 1, ES.no_power),
  machine("assembling-machine-2", "assembling-machine", 5, 1, nil),
  machine("lab", "lab", 6, 1, ES.missing_science_packs),
  machine("assembling-machine-2", "assembling-machine", 400, 400, ES.working),
  -- A ghost is an entity-ghost whatever it will become: never in a type walk.
  { name = "entity-ghost", type = "entity-ghost", ghost_name = "assembling-machine-2",
    ghost_type = "assembling-machine", valid = true, position = { x = 9, y = 9 }, force = player_force },
}
install_counter()
local function statuses_of(reply)
  local by = {}
  for _, row in ipairs(reply.r.statuses or {}) do by[row.status] = row end
  return by
end

local m = call("machine_status", { surface = "nauvis", type = "assembling-machine", limit = 2 })
local by = m.ok and statuses_of(m) or {}
check("machine_status counts working machines by status name", by.working and by.working.count == 4, F.encode(m.r))
check("machine_status maps numeric status back to its define key", by.no_power and by.no_power.count == 1, F.encode(m.r))
check("machine_status counts a nil status under unknown", by.unknown and by.unknown.count == 1, F.encode(m.r))
check("machine_status keeps at most limit examples as gps tags",
      by.working and #by.working.examples == 2 and by.working.examples[1] == "[gps=1,1,nauvis]", F.encode(m.r))
check("machine_status single example for a single machine",
      by.no_power and #by.no_power.examples == 1 and by.no_power.examples[1] == "[gps=4,1,nauvis]", F.encode(m.r))
check("machine_status statuses are sorted by count, largest first",
      m.ok and m.r.statuses[1].status == "working", F.encode(m.r.statuses))
check("machine_status reports 6 scanned, not truncated, ghosts left out",
      m.r.scanned == 6 and m.r.truncated == false and m.r.found == true, F.encode(m.r))
local sum = 0
for _, row in ipairs(m.r.statuses) do sum = sum + row.count end
check("per-status counts sum to the scanned count", sum == 6, sum)

local byname = call("machine_status", { surface = "nauvis", name = "lab" })
check("machine_status by name: one lab missing science packs",
      byname.ok and statuses_of(byname).missing_science_packs.count == 1 and byname.r.scanned == 1, F.encode(byname.r))

local limit_cap = call("machine_status", { surface = "nauvis", type = "assembling-machine", limit = 99 })
check("machine_status clamps limit to 5 examples", limit_cap.ok and #statuses_of(limit_cap).working.examples == 4, F.encode(limit_cap.r))

-- Bob stands at (10.4, -3.6) on nauvis; (6,1) is 6.4 tiles away, (400,400) is not.
local near = call("machine_status", { surface = "nauvis", type = "assembling-machine", near = "Bob", radius = 20 })
check("machine_status near a player walks that circle only: the far machine is left out",
      near.ok and near.r.scanned == 5 and statuses_of(near).working.count == 3, F.encode(near.r))
check("machine_status near sends position and radius to the engine",
      S.last_find_filter.radius == 20 and math.abs(S.last_find_filter.position.x - 10.4) < 0.01, F.encode(S.last_find_filter))
local near_default = call("machine_status", { surface = "nauvis", type = "assembling-machine", near = "Bob" })
check("machine_status near without a radius uses 256", near_default.ok and S.last_find_filter.radius == 256, F.encode(S.last_find_filter))
local near_big = call("machine_status", { surface = "nauvis", type = "assembling-machine", near = "Bob", radius = 99999 })
check("machine_status clamps radius to 1024", near_big.ok and S.last_find_filter.radius == 1024, F.encode(S.last_find_filter))
local near_nobody = call("machine_status", { surface = "nauvis", type = "lab", near = "Nobody" })
check("machine_status near an unknown player: found=false with a reason",
      near_nobody.ok and near_nobody.r.found == false and near_nobody.r.reason ~= nil, F.encode(near_nobody.r))
local no_surface = call("machine_status", { surface = "nope", type = "lab" })
check("machine_status unknown surface: found=false", no_surface.ok and no_surface.r.found == false, F.encode(no_surface.r))
local neither = call("machine_status", { surface = "nauvis" })
check("machine_status needs a name or a type", not neither.ok, F.encode(neither))
local bad_name = call("machine_status", { surface = "nauvis", name = "zzzz-nothing" })
check("machine_status unknown prototype name: found=false",
      bad_name.ok and bad_name.r.found == false, F.encode(bad_name.r))

-- Truncation: 4001 machines on the surface, 4000 scanned, the real total said.
S.entities = {}
for i = 1, 4001 do S.entities[i] = machine("assembling-machine-2", "assembling-machine", i % 100, math.floor(i / 100), ES.working) end
install_counter()
local big = call("machine_status", { surface = "nauvis", type = "assembling-machine" })
check("machine_status stops at 4000 scanned and says truncated",
      big.ok and big.r.scanned == 4000 and big.r.truncated == true, F.encode(big.r))
check("machine_status truncated says how many match in all", big.ok and big.r.matching == 4001, F.encode(big.r))
check("machine_status counts the scanned machines under their status", big.ok and statuses_of(big).working.count == 4000, F.encode(big.r))

-- ══ power ════════════════════════════════════════════════════════════
local function pole(id, stats)
  return { name = "medium-electric-pole", type = "electric-pole", valid = true, position = { x = id, y = 0 },
           force = player_force, electric_network_id = id, electric_network_statistics = stats }
end
local function accumulator(id, energy, size)
  return { name = "accumulator", type = "accumulator", valid = true, position = { x = id, y = 5 },
           force = player_force, electric_network_id = id, energy = energy, electric_buffer_size = size }
end
--- Electric statistics: per-tick joules, normal quality full, uncommon half
--- of it so a sum over qualities is visibly more than normal alone. Shaped
--- as the engine has them (measured on 2.1.23 and 2.1.24): consumers in
--- input_counts, producers in output_counts.
local function electric_stats(input, output)
  local st = { input_counts = {}, output_counts = {} }
  for name in pairs(input) do st.input_counts[name] = 1 end
  for name in pairs(output) do st.output_counts[name] = 1 end
  st.get_flow_count = function(a)
    assert(a.precision_index == defines.flow_precision_index.five_seconds, "now means five_seconds")
    local table_ = a.category == "input" and input or output
    local name, quality = a.name, "normal"
    if type(name) == "table" then name, quality = a.name.name, a.name.quality or "normal" end
    return (table_[name] or 0) * (quality == "uncommon" and 0.5 or 1)
  end
  return st
end
local big_stats = electric_stats({ assembling = 300, lab = 500 }, { ["steam-engine"] = 1000, ["solar-panel"] = 200 })
S.entities = {
  pole(7, big_stats), pole(7, big_stats), pole(7, big_stats),
  pole(9, electric_stats({ lab = 4 }, { ["solar-panel"] = 10 })),
  accumulator(7, 3e6, 5e6), accumulator(7, 1e6, 5e6), accumulator(9, 100, 200),
}
nauvis.count_entities_filtered = nil
local pw = call("power", { surface = "nauvis" })
check("power answers found with two networks", pw.ok and pw.r.found == true and pw.r.networks_total == 2, F.encode(pw.r))
local big_net = pw.ok and pw.r.networks[1] or {}
check("power ranks the network with the most poles first", big_net.network == 7 and big_net.poles == 3, F.encode(pw.r))
-- 1000 + 200 per tick at normal, plus half of it at uncommon: 1800 J/tick * 60 ticks/s = 108000 W.
check("power production is per-tick joules times 60 as watts, summed over qualities",
      big_net.production_w == 108000, F.encode(big_net))
check("power consumption: (300 + 500) * 1.5 * 60 = 72000 W", big_net.consumption_w == 72000, F.encode(big_net))
check("power sends no ratio of delivered figures, which cannot show a shortage", big_net.utilization == nil, F.encode(big_net))
check("power sums accumulator charge and capacity in megajoules over that network's accumulators",
      big_net.accumulator_charge_mj == 4 and big_net.accumulator_capacity_mj == 10, F.encode(big_net))
check("power names the top producer first with its watts",
      big_net.producers and big_net.producers[1].name == "steam-engine" and big_net.producers[1].w == 90000, F.encode(big_net))
check("power names the top consumer", big_net.consumers and big_net.consumers[1].name == "lab" and big_net.consumers[1].w == 45000, F.encode(big_net))
check("power no longer calls its producer side unverified: output_counts is production, measured", pw.r.unverified == nil, F.encode(pw.r))
check("power reply fits the budget", bytes_of(pw) <= BUDGET, bytes_of(pw))
local pw1 = call("power", { surface = "nauvis", limit = 1 })
check("power limit 1 shows one network and says the rest exist",
      pw1.ok and #pw1.r.networks == 1 and pw1.r.networks_total == 2, F.encode(pw1.r))
local pw_orbit = call("power", { surface = "platform-1" })
check("power on a surface with no poles: found=false with a reason",
      pw_orbit.ok and pw_orbit.r.found == false and pw_orbit.r.reason ~= nil, F.encode(pw_orbit.r))
local pw_bad = call("power", { surface = "nope" })
check("power unknown surface: found=false", pw_bad.ok and pw_bad.r.found == false, F.encode(pw_bad.r))
S.entities = {}
for i = 1, 2001 do S.entities[i] = pole(1, nil) end
local pw_cap = call("power", { surface = "nauvis" })
check("power says truncated when the pole cap (2000) is hit and counts only the poles seen",
      pw_cap.ok and pw_cap.r.truncated == true and pw_cap.r.networks[1].poles == 2000, F.encode(pw_cap.r))

-- ══ research_cost ════════════════════════════════════════════════════
local T = S.technologies
T.logistics.research_unit_ingredients = { { name = "automation-science-pack", amount = 1 } }
T.logistics.research_unit_energy = 600
T["logistics-2"].research_unit_ingredients = {
  { name = "automation-science-pack", amount = 1 }, { name = "logistic-science-pack", amount = 1 } }
T["logistics-2"].research_unit_energy = 900
T["mining-productivity"].research_unit_ingredients = { { name = "automation-science-pack", amount = 2 } }
T["mining-productivity"].research_unit_energy = 1800
T["steel-axe"] = { name = "steel-axe", researched = false, enabled = true, level = 1, research_unit_count = 0,
                   prerequisites = {}, saved_progress = 0, research_unit_ingredients = {},
                   prototype = { research_trigger = { type = "craft-item" } } }
for _, p in ipairs({ "automation-science-pack", "logistic-science-pack" }) do
  _G.prototypes.item[p] = { name = p }
end

local rc = call("research_cost", {})
check("research_cost walks the queue in order", rc.ok and rc.r.technologies[1].tech == "logistics"
      and rc.r.technologies[2].tech == "logistics-2", F.encode(rc.r))
-- logistics is current at progress 0.25: 20 units * 0.75 = 15. logistics-2 reads saved_progress 0.5: 200 * 0.5 = 100.
check("research_cost remaining units: current uses force progress", rc.ok and rc.r.technologies[1].remaining == 15, F.encode(rc.r))
check("research_cost remaining units: queued uses saved progress", rc.ok and rc.r.technologies[2].remaining == 100, F.encode(rc.r))
check("research_cost carries units, unit seconds and ingredients per unit",
      rc.r.technologies[2].units == 200 and rc.r.technologies[2].unit_time == 15
      and #rc.r.technologies[2].ingredients == 2, F.encode(rc.r.technologies[2]))
local function pack(reply, name)
  for _, p in ipairs(reply.r.packs or {}) do if p.pack == name then return p end end
end
check("research_cost totals each pack over the queue: 15 + 100 automation, 100 logistic",
      pack(rc, "automation-science-pack").needed == 115 and pack(rc, "logistic-science-pack").needed == 100, F.encode(rc.r.packs))
check("research_cost says no_current_consumption when a pack has no rate",
      pack(rc, "automation-science-pack").no_current_consumption == true and rc.r.minutes_at_current_rate == nil, F.encode(rc.r))

-- Packs seen on nauvis: the fake consumes 20 a minute at normal and 10 at uncommon, 30 in all.
S.stats.input_counts["automation-science-pack"] = 100
S.stats.input_counts["logistic-science-pack"] = 100
local rc2 = call("research_cost", {})
check("research_cost per-pack rate is the ten-minute consumption summed over qualities",
      pack(rc2, "automation-science-pack").per_minute == 30, F.encode(rc2.r.packs))
-- 115 / 30 = 3.83 minutes, 100 / 30 = 3.33: automation is the slower, so the bottleneck.
check("research_cost reports the slowest pack as the bottleneck and its minutes",
      rc2.r.bottleneck == "automation-science-pack" and rc2.r.minutes_at_current_rate == "3.83", F.encode(rc2.r))
check("research_cost does not say no_current_consumption when every pack has a rate",
      pack(rc2, "logistic-science-pack").no_current_consumption == nil, F.encode(rc2.r.packs))
check("research_cost reply fits the budget", bytes_of(rc2) <= BUDGET, bytes_of(rc2))

local one = call("research_cost", { tech = "logistics-2" })
check("research_cost for one tech lists only it", one.ok and #one.r.technologies == 1 and one.r.technologies[1].remaining == 100, F.encode(one.r))
local trig = call("research_cost", { tech = "steel-axe" })
check("research_cost lists a trigger technology with its trigger type and no units",
      trig.ok and trig.r.technologies[1].research_trigger == "craft-item" and trig.r.technologies[1].units == nil, F.encode(trig.r))
local inf = call("research_cost", { tech = "mining-productivity" })
check("research_cost infinite tech: the engine's count for the current level",
      inf.ok and inf.r.technologies[1].units == 2000 and inf.r.technologies[1].level == 4
      and inf.r.technologies[1].remaining == 1600, F.encode(inf.r))
local inf_same = call("research_cost", { tech = "mining-productivity", level = 4 })
check("research_cost level equal to the current level is accepted", inf_same.ok and inf_same.r.found == true, F.encode(inf_same.r))
local inf_other = call("research_cost", { tech = "mining-productivity", level = 9 })
check("research_cost a level other than the current one: found=false, no formula guessed",
      inf_other.ok and inf_other.r.found == false and tostring(inf_other.r.reason):find("4", 1, true), F.encode(inf_other.r))
local unknown = call("research_cost", { tech = "no-such-tech" })
check("research_cost unknown tech: found=false", unknown.ok and unknown.r.found == false, F.encode(unknown.r))
S.research_queue[1], S.research_queue[2] = T["mining-productivity"], T["mining-productivity"]
local dup = call("research_cost", {})
check("research_cost lists a repeated levelled tech once and says how many times it is queued",
      dup.ok and #dup.r.technologies == 1 and dup.r.technologies[1].queued == 2, F.encode(dup.r))
for i = #S.research_queue, 1, -1 do S.research_queue[i] = nil end
local empty = call("research_cost", {})
check("research_cost on an empty queue says so", empty.ok and empty.r.found == true and #empty.r.technologies == 0, F.encode(empty.r))

-- ══ mods ═════════════════════════════════════════════════════════════
local real_mods = script.active_mods
script.active_mods = { ["ai-agent-bridge"] = "1.1.1", base = "2.1.23", ["a-mod"] = "0.3.0" }
local md = call("mods", {})
check("mods lists every active mod sorted by name with its version",
      md.ok and md.r.count == 3 and md.r.mods[1].name == "a-mod" and md.r.mods[1].version == "0.3.0"
      and md.r.mods[2].name == "ai-agent-bridge" and md.r.mods[3].name == "base", F.encode(md.r))
check("mods carries no paging fields for a whole list", md.r.next_offset == nil, F.encode(md.r))
script.active_mods = {}
for i = 1, 300 do script.active_mods[string.format("some-long-mod-name-%03d", i)] = "1.2." .. i end
local page1 = call("mods", {})
check("mods count is the real number of mods even when the page is cut", page1.ok and page1.r.count == 300, F.encode(page1.r))
check("mods cuts to the reply budget and offers next_offset",
      bytes_of(page1) <= BUDGET and page1.r.next_offset ~= nil and page1.r.shown == #page1.r.mods, bytes_of(page1))
local page2 = call("mods", { offset = page1.r.next_offset })
check("mods next page continues where the first stopped",
      page2.ok and page2.r.mods[1].name == string.format("some-long-mod-name-%03d", page1.r.next_offset + 1), F.encode(page2.r.mods and page2.r.mods[1]))
script.active_mods = real_mods

-- ══ resources ════════════════════════════════════════════════════════
local function resource(name, x, y, amount)
  return { name = name, type = "resource", valid = true, position = { x = x, y = y }, amount = amount, force = { name = "neutral" } }
end
_G.prototypes.entity["iron-ore"] = { name = "iron-ore", type = "resource", infinite_resource = false }
_G.prototypes.entity["copper-ore"] = { name = "copper-ore", type = "resource", infinite_resource = false }
_G.prototypes.entity["crude-oil"] = { name = "crude-oil", type = "resource", infinite_resource = true,
                                      normal_resource_amount = 300000 }
-- Bob is at (10.4, -3.6).
S.entities = {
  resource("iron-ore", 10, -3, 100), resource("iron-ore", 12, -3, 500), resource("iron-ore", 14, -5, 300),
  resource("copper-ore", 20, 0, 40),
  resource("crude-oil", 11, -6, 600000), resource("crude-oil", 13, -6, 300000),
  resource("iron-ore", 900, 900, 7777),
}
local rs = call("resources", { surface = "nauvis", near = "Bob" })
local function res_row(reply, name)
  for _, r in ipairs(reply.r.resources or {}) do if r.resource == name then return r end end
end
local iron = rs.ok and res_row(rs, "iron-ore") or {}
check("resources sums amount and counts entities per kind within the radius",
      iron.entities == 3 and iron.total == 900, F.encode(rs.r))
check("resources names the richest tile of each kind as a gps tag", iron.richest == "[gps=12,-3,nauvis]" and iron.richest_amount == 500, F.encode(iron))
local oil = rs.ok and res_row(rs, "crude-oil") or {}
-- (600000 + 300000) / 2 entities / 300000 normal = 1.5 = 150 percent.
check("resources reports an infinite resource as an average yield percent, no total",
      oil.infinite == true and oil.avg_yield_percent == 150 and oil.total == nil, F.encode(oil))
check("resources default radius is 128 and the filter is a circle on the player",
      S.last_find_filter.radius == 128 and S.last_find_filter.type == "resource", F.encode(S.last_find_filter))
check("resources says it was not truncated", rs.r.truncated == false and rs.r.scanned == 6, F.encode(rs.r))
check("resources reply fits the budget", bytes_of(rs) <= BUDGET, bytes_of(rs))
local only = call("resources", { surface = "nauvis", near = "Bob", resource = "copper-ore", radius = 5000 })
check("resources for one kind: just that kind, radius clamped to 512",
      only.ok and #only.r.resources == 1 and only.r.resources[1].total == 40 and S.last_find_filter.radius == 512
      and S.last_find_filter.name == "copper-ore", F.encode(only.r))
local nonear = call("resources", { surface = "nauvis" })
check("resources without near: found=false with a reason, not an error",
      nonear.ok and nonear.r.found == false and tostring(nonear.r.reason):find("near", 1, true), F.encode(nonear))
local nonear_res = call("resources", { surface = "nauvis", resource = "iron-ore" })
check("resources with only a resource name still needs near", nonear_res.ok and nonear_res.r.found == false, F.encode(nonear_res))
local nobody = call("resources", { surface = "nauvis", near = "Nobody" })
check("resources near an unknown player: found=false", nobody.ok and nobody.r.found == false, F.encode(nobody))
local badres = call("resources", { surface = "nauvis", near = "Bob", resource = "zzzz-nothing" })
check("resources unknown resource name: found=false", badres.ok and badres.r.found == false, F.encode(badres.r))
S.entities = {}
for i = 1, 20001 do S.entities[i] = resource("iron-ore", 10 + (i % 50), -3, 10) end
local rcap = call("resources", { surface = "nauvis", near = "Bob" })
check("resources stops at 20000 entities and says truncated",
      rcap.ok and rcap.r.truncated == true and rcap.r.scanned == 20000 and res_row(rcap, "iron-ore").entities == 20000, F.encode(rcap.r))
check("resources a truncated sum is still a single-reply size", bytes_of(rcap) <= BUDGET, bytes_of(rcap))
S.entities = {}

-- ══ sweep: envelope `better` and the enemies_killed metric ═══════════
local envelope = require("scripts.sweep.envelope")
local function rows_for() return { { name = "a", value = 3 }, { name = "b", value = 1 } } end
local lower = envelope.build("m", "force", { unit = "ms", places = 0, better = "lower" }, rows_for(), {})
check("envelope.build emits better when the metric sets it", lower.better == "lower", F.encode(lower))
local plain = envelope.build("m", "force", { unit = "ms", places = 0 }, rows_for(), {})
check("envelope.build leaves better out when the metric does not set it", plain.better == nil, F.encode(plain))

_G.prototypes.entity["small-biter"] = { name = "small-biter", type = "unit" }
_G.prototypes.entity["medium-biter"] = { name = "medium-biter", type = "unit" }
_G.prototypes.entity["tree-01"] = { name = "tree-01", type = "tree" }
-- player: 55 biters (40 small, 10 medium, 5 on the platform) and 1000 trees. kills
-- counts all 1055; enemies_killed counts the 55. team-3 has no players, so
-- the all=true read leaves it out.
S.kill_counts.player.nauvis.input_counts["tree-01"] = 1000
local function sweep(a) return call("sweep", a) end
local kills = sweep({ metric = "kills" })
check("sweep kills counts trees: player 1055 leads", kills.ok and kills.r.rows[1][2] == 1055, F.encode(kills.r))
local en = sweep({ metric = "enemies_killed" })
check("sweep enemies_killed counts enemies only: player 55, trees left out",
      en.ok and en.r.rows and en.r.rows[1][1] == "player" and en.r.rows[1][2] == 55, F.encode(en.r))
check("sweep enemies_killed is on the force axis with a count unit",
      en.ok and en.r.axis == "force" and en.r.unit == "count" and en.r.metric == "enemies_killed", F.encode(en.r))
check("sweep enemies_killed is listed in the sweep manifest",
      tostring(rpc({ op = "manifest", i = "ai-agent-bridge-tools" }).r.tools.sweep.desc):find("enemies_killed", 1, true), "desc")

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
