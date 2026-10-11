-- AI Agent Bridge - scripts/map_id.lua
-- Author: bits-orio
-- License: MIT
--
-- One id per map, so the service can tell a new map from an older save of
-- the same one. Ticks start over on a new map, but they also drop when an
-- autosave of the same map is loaded, and only the map itself can say which
-- of the two happened. The id is made once and kept in storage: every save
-- and every load of one map carries it, and a new map makes its own.
--
-- Made from the map seed, the tick it was made at and math.random. The game's
-- math.random is the map's own deterministic generator, and every peer runs
-- the handler that makes the id at the same tick, so every peer stores the
-- same string and nothing desyncs. Two maps made from one seed at the same
-- tick can still share an id: the game offers no other source of chance.

local RANDOM_MAX = 2147483647

local M = {}

--- The seed nauvis was generated with, or the first surface's when a game
--- has no nauvis, 0 when neither says.
local function seed()
  local surface = game.get_surface("nauvis")
  if not (surface and surface.valid) then
    for _, any in pairs(game.surfaces) do
      surface = any
      break
    end
  end
  local generated = surface and surface.valid and surface.map_gen_settings or nil
  return generated and generated.seed or 0
end

--- The map's id, made the first time it is asked for. Writes storage, so it
--- runs only where every peer runs it at the same tick: on_init, the first
--- event written, or the aab-rpc `status` op, a console command.
function M.ensure()
  storage.aab = storage.aab or {}
  if not storage.aab.map_id then
    storage.aab.map_id = string.format("%d-%d-%d", seed(), game.tick, math.random(RANDOM_MAX))
  end
  return storage.aab.map_id
end

return M
