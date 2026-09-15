-- AI Agent Bridge - tests/provider-mod/aab-test-provider/control.lua
-- Author: bits-orio
-- License: MIT
--
-- A provider, and nothing else. It exists so the end-to-end harness can prove
-- two things about a mod the companion has never heard of: that its tools turn
-- up in the catalog through the agent_tools_v1 probe, and that an error inside
-- one of its tools comes back as a provider_error rather than taking anything
-- down with it.
--
-- This is the whole of what a provider has to write (CONTEXT.md "Provider"):
-- one zero-argument probe on an interface it already owns, and one plain
-- function per tool. No dependency on the companion in either direction, no
-- registration, nothing stored.

local INTERFACE = "aab-test-provider"

local MANIFEST = {
  hello = {
    desc = "Greets someone by name. A tool that exists only to prove a third mod's tools reach the agent.",
    params = {
      name = "string! who to greet",
    },
  },
  boom = {
    desc = "Always fails. Used to check that an error inside a provider is reported and never crashes the caller.",
  },
  -- A metric the agent's sweep tool can rank across every force, declared
  -- here and nowhere in the companion (docs/design/phase5-sweep.md,
  -- "Provider-declared metrics"). This is the shape any multi-team mod
  -- declares to make one of its tools sweepable: which reply field holds the
  -- rows, which row field names the force, which holds the number.
  standings = {
    desc = "Each force's score, one row per force, the way a team mod ranks its teams.",
    sweep = { axes = { "force" }, rows = "forces", name = "force", value = "score", unit = "points" },
  },
}

-- `force` is injected into every tool's argument table by the service and is
-- ignored here, which is exactly what a provider that does not care about
-- force should do.
local function hello(args)
  local name = type(args) == "table" and args.name or nil
  if type(name) ~= "string" or name == "" then
    name = "stranger"
  end
  return { greeting = "hello " .. name .. ", from the test provider" }
end

local function boom()
  error("boom from the test provider")
end

-- A score nobody would compute for real, chosen so the leader is fixed by the
-- game rather than by this file: the highest force index wins. total and
-- shown are reported the way every bounded reply does, so the sweep can tell
-- a whole list from a cut one.
local function standings()
  local rows = {}
  for _, force in pairs(game.forces) do
    rows[#rows + 1] = { force = force.name, score = force.index * 10 }
  end
  table.sort(rows, function(x, y) return x.force < y.force end)
  return { total = #rows, shown = #rows, forces = rows }
end

-- A chat scope provider as well (docs/design/phase3-spec.md part 2), so the
-- harness can make one force private and watch an answer stay inside it.
-- The badge strings copy the layout a team-chat mod stamps on its lines;
-- nothing here knows what a team is beyond a force name.
local GLOBAL_TAG = "[color=0.4,0.9,0.4][GLOBAL][/color]"
local TEAM_TAG   = "[color=0.45,0.8,1][TEAM][/color]"

local function private_forces()
  storage.private_forces = storage.private_forces or {}
  return storage.private_forces
end

--- set_private(force_name, on): flips one force between team-only and
--- global. Called by the harness over RCON through /sc.
local function set_private(force_name, on)
  private_forces()[force_name] = on and true or nil
end

--- The probe. A "!" at the start of the line shouts globally, the way a
--- team-chat mod would let a team-only player do.
local function chat_scope_v1(player_index, text)
  local player = player_index and game.get_player(player_index)
  if not (player and player.valid) then return nil end
  local force_name = player.force.name
  local shout = type(text) == "string" and text:sub(1, 1) == "!"
  if private_forces()[force_name] and not shout then
    return { key = force_name, private = true, audience = { force = force_name }, label = force_name, tag = TEAM_TAG }
  end
  return { key = "global", private = false, tag = GLOBAL_TAG }
end

remote.add_interface(INTERFACE, {
  agent_tools_v1 = function() return { v = 1, tools = MANIFEST } end,
  hello = hello,
  boom = boom,
  standings = standings,
  chat_scope_v1 = chat_scope_v1,
  set_private = set_private,
  -- A labels provider too: what players call the player force.
  force_labels_v1 = function()
    return { player = "[color=red]The[/color] Engineers", ["team-3"] = "Team Losers" }
  end,
})
