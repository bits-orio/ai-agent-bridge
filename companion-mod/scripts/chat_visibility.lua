-- AI Agent Bridge - scripts/chat_visibility.lua
-- Author: bits-orio
-- License: MIT
--
-- Who could read one chat line, logged beside it in events.jsonl so the
-- service never builds an answer from a private line for anyone outside the
-- channel it was said in (CONTEXT.md "History").
--
-- A scope provider knows its own channels, so when one answers, its answer
-- stands, the most private one when several do, exactly as for a question.
-- When none answers, the engine's own rule stands: a plain line reaches the
-- speaker's force alone. That is private once a second force has players,
-- and public while every player shares one force and saw it anyway. A
-- provider that fails may be the one that knew the line was private, so
-- after a failure only a private answer stands: a global one falls to the
-- engine's rule as well. A line typed into the server console reached
-- everyone. A question asked with the chat prefix takes its line's scope,
-- so it never reaches more players than the line did.
--
-- The engine's rule keys a private line by its force name only on a server
-- with no scope provider at all. A private key is read back by every later
-- question under it, and a provider may hand a force from one group to the
-- next, so history can hold earlier groups' private lines and questions
-- under that same name. Once a provider is installed, a line the engine's
-- rule decides is still private to the speaker's force, and still reaches
-- it, but it is kept under the global key, which the service never reads
-- private rows under: nobody reads it back, and a question asked with it
-- reads public rows alone.
--
-- Runs on every peer like any event handler. Providers are pure reads and
-- the line is written by the server alone (events.write), so nothing here
-- can desync.

local scope = require("scripts.scope")

local M = {}

--- True once two forces have players, online or not: a line said while the
--- other force is offline was still never theirs to read.
local function forces_are_split()
  local populated = 0
  for _, force in pairs(game.forces) do
    if #force.players > 0 then
      populated = populated + 1
      if populated > 1 then return true end
    end
  end
  return false
end

--- The key the engine's rule keeps a private line under: the speaker's
--- force name on a server with no scope provider, and once one is installed
--- the global key, which no question reads private rows under.
local function engine_key(force, providers_installed)
  if providers_installed then return scope.GLOBAL_KEY end
  return force
end

--- The engine's rule for a line no provider answered for: private to the
--- speaker's force, which is also its audience, once forces are split.
local function engine_scope(player, providers_installed)
  if not forces_are_split() then
    return { key = scope.GLOBAL_KEY, private = false }
  end
  local force = player.force.name
  return { key = engine_key(force, providers_installed), private = true, audience = { force = force } }
end

--- The whole scope of one line a player typed, audience and tag included:
--- a provider's answer when one gives it, the engine's rule otherwise. When
--- a provider failed, a global answer is not enough to open the line up,
--- and the engine's rule decides instead. A question asked with the chat
--- prefix takes this, so its text and its answer reach the same players its
--- line did.
function M.scope_of_line(player, message)
  local found, answered, failed, installed = scope.lookup(player.index, message)
  if answered and (found.private or not failed) then return found end
  return engine_scope(player, installed)
end

--- { scope = <key>, private = <boolean> } for one chat line. player is the
--- speaker, nil for a line typed into the server console.
function M.of(player, message)
  if not player then return { scope = scope.GLOBAL_KEY, private = false } end
  local found = M.scope_of_line(player, message)
  return { scope = found.key, private = found.private == true }
end

return M
