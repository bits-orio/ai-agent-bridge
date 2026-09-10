-- One guarded player lookup, shared by every path that resolves an asker's
-- player_index (scripts/questions.lua, scripts/render.lua).
--
-- A player_index reaching those paths came from a caller, not from the engine:
-- the ai-agent-bridge-v1 remote interface takes whatever another mod passes,
-- and a question keeps that value in storage until it is answered. get_player
-- returns nil for an index nobody holds, but raises for one the engine refuses
-- outright (anything above 65536), so both cases have to land on nil here or a
-- caller's mistake becomes a crash in the companion.

local M = {}

--- The player with this index, or nil for any index that does not name one.
function M.by_index(index)
  if index == nil then return nil end
  local found_ok, found = pcall(game.get_player, index)
  if not found_ok then return nil end
  return found
end

return M
