-- Shared "resolve a surface" helper for every engine tool that takes one. The
-- one contract: a name or an index goes in, a LuaSurface or a found = false
-- reply comes out. No surface-taking tool resolves its own argument.
--
-- Unlike force_lookup, a miss here is not an error. The surface is the argument
-- a model guesses most often: a planet it remembers from another save, a
-- platform that has since been scrapped, a name a player typed from memory. A
-- tool that errors on a guess costs the agent a whole round and tells it
-- nothing, so these tools answer found = false and name the tool that lists the
-- real surfaces instead.
--
-- An index is accepted because list_surfaces publishes one on every row, and a
-- model that passes back what it was just shown deserves an answer rather than
-- "surface is required". That wording is now reserved for a genuinely absent
-- argument.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10):
-- LuaGameScript::surfaces, LuaCustomTable[uint32 or string -> LuaSurface],
-- "this sparse table allows you to find surfaces by indexing it with either
-- their name or index"; LuaSurface::name; LuaSurface::index; LuaSurface::valid.

local M = {}

--- Looks one up by whichever of the two it is. Returns the surface (or nil)
--- plus the label to quote back, or nil, nil when there was no argument at all.
local function resolve(id)
  if type(id) == "number" then
    local index = math.floor(id)
    return game.surfaces[index], tostring(index)
  end
  if type(id) == "string" and id ~= "" then
    local by_name = game.surfaces[id]
    if by_name then return by_name, id end
    -- The declared grammar asks for a name, so an index usually arrives as a
    -- string. Try it as one before calling this a miss. A surface actually
    -- named "2" still wins, because the name lookup came first.
    local index = tonumber(id)
    if index then return game.surfaces[math.floor(index)], id end
    return nil, id
  end
  return nil, nil
end

--- Resolves a surface name or index. Returns the LuaSurface on a hit, or nil
--- plus a ready-to-return reply table on a miss. Callers add their own fields to
--- the miss reply; every one of them carries found = false and says what to do next.
function M.find(id)
  local surface, label = resolve(id)
  if label == nil then
    return nil, {
      found = false,
      surface = tostring(id),
      reason = "surface is required: pass a name or an index from list_surfaces",
    }
  end
  if not surface or not surface.valid then
    return nil, {
      found = false,
      surface = label,
      reason = "no surface by that name or index: call list_surfaces for the ones this game has",
    }
  end
  return surface
end

return M
