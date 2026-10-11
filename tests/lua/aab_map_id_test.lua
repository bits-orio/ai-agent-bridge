-- AI Agent Bridge - tests/lua/aab_map_id_test.lua
-- Author: bits-orio
-- License: MIT
--
-- The map id (companion-mod/scripts/map_id.lua) and the session event that
-- carries it. The service starts a new map epoch when the id changes and
-- keeps the old one when it does not, so a rollback to an older save of the
-- same map stays one history and a new map starts another.
--
-- Mutation-tested, one break at a time, each turning a check below red:
--   * on_init no longer making the id: the on_init checks
--   * events.write no longer making a missing id: the old-save checks
--   * the id made afresh on every call instead of kept: the reload checks
--   * the seed or the tick left out of the id: the shape checks
--   * the session line written on every write, or never: the session checks
--   * the session line written after the event instead of first: the order
--     check
--   * status without map_id, reading storage without making the id, or
--     making it afresh on every call: the status checks

local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local SEED = 3141592
local S = F.install({ seed = SEED, tick = 0 })

local passes, fails = 0, 0
local function check(name, ok, detail)
  if ok then passes = passes + 1; print("PASS " .. name)
  else fails = fails + 1; print("FAIL " .. name .. ": " .. tostring(detail)) end
end

--- Loads control.lua into a fresh copy of every companion module, the way a
--- save being loaded starts a new Lua state: storage survives, module locals
--- do not.
local function load_companion()
  for name in pairs(package.loaded) do
    if name:sub(1, 8) == "scripts." then package.loaded[name] = nil end
  end
  S.commands = {}
  local loaded, err = pcall(dofile, MOD .. "control.lua")
  check("companion control.lua loads", loaded, err)
  if not loaded then os.exit(1) end
end

local EVENTS = "ai-agent-bridge/events.jsonl"

--- Every line of events.jsonl, decoded, oldest first.
local function lines()
  local out = {}
  for line in (S.files[EVENTS] or ""):gmatch("[^\n]+") do out[#out + 1] = F.decode(line) end
  return out
end

local function say(message)
  S.handlers[defines.events.on_console_chat]({ player_index = 1, message = message })
end

--- seed, tick and the random part of an id, or nil when it is not that shape.
local function parts(id)
  if type(id) ~= "string" then return nil end
  local seed, tick, random = id:match("^(%d+)%-(%d+)%-(%d+)$")
  if not seed then return nil end
  return { seed = tonumber(seed), tick = tonumber(tick), random = tonumber(random) }
end

-- ── a new map: on_init makes the id ───────────────────────────────────
load_companion()
S.on_init()
local id = storage.aab and storage.aab.map_id
local shape = parts(id)
check("on_init makes a map id", type(id) == "string", tostring(id))
check("the id is the map seed, the tick it was made at and a random number",
      shape and shape.seed == SEED and shape.tick == 0 and shape.random >= 1, tostring(id))
check("nothing is written to the event file by on_init alone", S.files[EVENTS] == nil, S.files[EVENTS])

-- ── the session event opens every session ────────────────────────────
S.tick = 600
say("hello")
local all = lines()
check("the first write of a session writes the session event, then the line",
      #all == 2 and all[1].event == "session" and all[2].event == "console_chat", F.encode(all))
check("the session event carries the map id and the tick it was written at",
      all[1] and all[1].data.map_id == id and all[1].tick == 600, F.encode(all[1]))
say("again")
all = lines()
check("a later write in the same session adds its line alone",
      #all == 3 and all[3].event == "console_chat" and all[3].data.message == "again", F.encode(all))

-- ── a load: the same map, a new session ──────────────────────────────
S.tick = 900
load_companion()
say("after the load")
all = lines()
check("the first write after a load truncates the file and opens with the session event",
      #all == 2 and all[1].event == "session" and all[2].data.message == "after the load", F.encode(all))
check("a load of the same map keeps its id", all[1] and all[1].data.map_id == id, F.encode(all[1]))

-- An older autosave of this map, loaded later at a lower tick, is still
-- this map: the id is in its storage, so it says so however far ticks drop.
S.tick = 300
load_companion()
say("rolled back")
all = lines()
check("a rollback to an earlier tick keeps the id", all[1] and all[1].event == "session" and all[1].data.map_id == id,
      F.encode(all[1]))

-- ── a save from before map ids: made on the first event written ──────
storage.aab.map_id = nil
S.tick = 5000
load_companion()
say("first line after the update")
all = lines()
local late = storage.aab.map_id
local late_shape = parts(late)
check("a save with no map id gets one on the first event written",
      late_shape and late_shape.seed == SEED and late_shape.tick == 5000, tostring(late))
check("and the session event carries it", all[1] and all[1].event == "session" and all[1].data.map_id == late,
      F.encode(all[1]))

-- With the event file off, nothing is written and nothing is made either;
-- the first write once it is on again makes the id.
storage.aab.map_id = nil
S.settings["aab-events-enabled"].value = false
S.tick = 6000
load_companion()
say("not logged")
check("with events off no id is made", storage.aab.map_id == nil, tostring(storage.aab.map_id))
S.settings["aab-events-enabled"].value = true
S.tick = 6060
say("logged")
local resumed = parts(storage.aab.map_id)
check("the first write after events are turned on makes it", resumed and resumed.tick == 6060,
      tostring(storage.aab.map_id))
all = lines()
check("and opens the file with the session event", all[1] and all[1].event == "session"
      and all[1].data.map_id == storage.aab.map_id and #all == 2, F.encode(all))

-- ── the id is the map's ──────────────────────────────────────────────
-- A map with no nauvis takes the first surface's seed; a surface that says
-- no seed gives 0.
local real_get_surface, real_surfaces = game.get_surface, game.surfaces
local orbit = S.surfaces["platform-1"]
orbit.map_gen_settings = { seed = 777 }
game.get_surface = function(name) if name == "nauvis" then return nil end return real_get_surface(name) end
game.surfaces = { orbit }
storage.aab.map_id = nil
say("no nauvis")
local no_nauvis = parts(storage.aab.map_id)
check("a game with no nauvis takes the first surface's seed", no_nauvis and no_nauvis.seed == 777,
      tostring(storage.aab.map_id))
orbit.map_gen_settings = {}
storage.aab.map_id = nil
say("no seed at all")
local no_seed = parts(storage.aab.map_id)
check("a surface that gives no seed makes the seed part 0", no_seed and no_seed.seed == 0, tostring(storage.aab.map_id))
game.get_surface, game.surfaces = real_get_surface, real_surfaces
orbit.map_gen_settings = nil

-- A second map from another seed, made at the same tick, is another map.
local first_map = parts(id)
S.surfaces["nauvis"].map_gen_settings = { seed = SEED + 1 }
storage.aab = nil
S.tick = 0
load_companion()
S.on_init()
local second_map = parts(storage.aab.map_id)
check("a map from another seed gets another id", second_map and first_map and second_map.seed == SEED + 1
      and storage.aab.map_id ~= id, tostring(storage.aab.map_id))

-- ── the status op reports the id ─────────────────────────────────────
-- The service reads the id from status too, so it knows which map it talks
-- to before any event is written. status reports the id the save holds,
-- and on a save from before map ids it makes it, the same id the session
-- event written next names.
local function status()
  S.rcon_replies = {}
  S.commands["aab-rpc"]({ parameter = F.encode({ v = 1, op = "status" }), tick = S.tick })
  local reply = F.decode(S.rcon_replies[#S.rcon_replies] or "{}")
  return reply and reply.ok and reply.r or {}
end
local held = storage.aab.map_id
check("status reports the map id the save holds", status().map_id == held, F.encode(status()))
check("and asking again leaves it as it was", status().map_id == held and storage.aab.map_id == held,
      tostring(storage.aab.map_id))

storage.aab.map_id = nil
S.tick = 7200
load_companion()
local made = status().map_id
local made_shape = parts(made)
check("on a save with no map id, status makes one from the seed and the tick",
      made_shape and made_shape.seed == SEED + 1 and made_shape.tick == 7200 and storage.aab.map_id == made,
      tostring(made))
S.tick = 7260
check("a later status keeps it", status().map_id == made, tostring(storage.aab.map_id))
say("first line after status")
all = lines()
check("the session event written next names the id status made",
      all[1] and all[1].event == "session" and all[1].data.map_id == made, F.encode(all[1]))

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
