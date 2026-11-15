-- AI Agent Bridge - scripts/tools/entity_view.lua
-- Author: bits-orio
-- License: MIT
--
-- How the "where" tools read one entity, ghost or built, and write one
-- position: shared by find_entities (locate.lua) and locate_player
-- (locate_player.lua) so the two cannot drift on either.
--
-- API names verified against runtime-api.json 2.1.23: LuaEntity::type,
-- ::name, ::ghost_name, ::ghost_type, ::position.

local M = {}

--- A ghost is an entity of type entity-ghost whose real prototype sits in
--- ghost_name and ghost_type; everything below asks these two so a ghost
--- and a built machine read the same way.
function M.is_ghost(entity) return entity.type == "entity-ghost" end
function M.real_name(entity) return M.is_ghost(entity) and entity.ghost_name or entity.name end
function M.real_type(entity) return M.is_ghost(entity) and entity.ghost_type or entity.type end

--- A position rounded to whole tiles, and the [gps=x,y,surface] tag.
function M.gps(position, surface_name)
  local x, y = math.floor(position.x + 0.5), math.floor(position.y + 0.5)
  return x, y, string.format("[gps=%d,%d,%s]", x, y, surface_name)
end

return M
