-- Shared "resolve a surface by name" helper for the engine tools that take one.
--
-- Unlike force_lookup, a miss here is not an error. The surface name is the
-- argument a model guesses most often: a planet it remembers from another save,
-- a platform that has since been scrapped, a name a player typed from memory. A
-- tool that errors on a guess costs the agent a whole round and tells it
-- nothing, so these tools answer found = false and name the tool that lists the
-- real surfaces instead.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10):
-- LuaGameScript::surfaces, LuaCustomTable[uint32 or string -> LuaSurface];
-- LuaSurface::name; LuaSurface::valid.

local M = {}

--- Resolves a surface name. Returns the LuaSurface on a hit, or nil plus a
--- ready-to-return reply table on a miss. Callers add their own fields to the
--- miss reply; every one of them carries found = false and says what to do next.
function M.find(name)
  if type(name) ~= "string" or name == "" then
    return nil, {
      found = false,
      surface = tostring(name),
      reason = "surface is required: pass one of the names list_surfaces returns",
    }
  end
  local surface = game.surfaces[name]
  if not surface or not surface.valid then
    return nil, {
      found = false,
      surface = name,
      reason = "no surface by that name: call list_surfaces for the ones this game has",
    }
  end
  return surface
end

return M
