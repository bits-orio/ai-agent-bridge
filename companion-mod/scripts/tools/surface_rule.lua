-- AI Agent Bridge - scripts/tools/surface_rule.lua
-- Author: bits-orio
-- License: MIT
--
-- Whether a prototype's surface conditions hold on one surface: the rule
-- behind "a crusher cannot be built on Vulcanus" and "a stone furnace
-- cannot burn in space". Space Age gives entities and recipes
-- surface_conditions, each {property, min, max}, and a surface its own
-- values: Vulcanus pressure 4000 and gravity 40, a platform 0 and 0. A
-- plan that ignored them routed Vulcanus iron through asteroid crushing in
-- crushers, which need gravity 0 (data/space-age entities.lua).
--
-- A property is read once per surface per call. LuaSurface::get_property
-- answers for any surface, its planet's value or the property's default; a
-- property the game does not know raises, and a condition on such a
-- property is treated as unmet rather than as a crash, with the prototype
-- default as a fallback when the surface will not say.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaEntityPrototype::surface_conditions, LuaRecipePrototype::
--     surface_conditions: optional array[SurfaceCondition].
--   SurfaceCondition: {property :: string, min :: double, max :: double}.
--   LuaSurface::get_property(property :: SurfacePropertyID) -> double.
--   LuaPrototypes::surface_property, LuaCustomTable[string ->
--     LuaSurfacePropertyPrototype]; ::default_value (double).

local M = {}

local function default_of(name)
  local protos = prototypes.surface_property
  local proto = protos and protos[name]
  return proto and proto.default_value or nil
end

--- The rule for one surface, or nil when no surface was given: nothing is
--- then ruled out, as before surface conditions were read.
function M.of(surface)
  if not surface then return nil end
  local values = {}
  local function value(name)
    if values[name] == nil then
      local ok, v = pcall(surface.get_property, name)
      values[name] = (ok and type(v) == "number") and v or (default_of(name) or false)
    end
    return values[name]
  end
  local rule = { surface = surface }
  --- Whether every condition of `proto` (an entity or recipe prototype)
  --- holds here. A prototype with none holds everywhere.
  function rule.allows(proto)
    for _, condition in ipairs(proto.surface_conditions or {}) do
      local v = value(condition.property)
      if v == false then return false end
      if condition.min and v < condition.min then return false end
      if condition.max and v > condition.max then return false end
    end
    return true
  end
  return rule
end

return M
