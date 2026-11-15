-- AI Agent Bridge - scripts/tools/machine_surface.lua
-- Author: bits-orio
-- License: MIT
--
-- Which crafting machines and recipes a surface rules out, for
-- machine_pick.lua: a machine whose surface_conditions fail on the surface
-- asked about is never offered, and a recipe only such machines can make is
-- not a candidate (scripts/tools/surface_rule.lua reads the conditions). A
-- recipe no machine makes at all keeps today's "no crafting machine can make
-- it" answer rather than vanishing from the plan.
--
-- API names verified against runtime-api.json 2.1.23:
--   LuaRecipePrototype::has_category(category) -> boolean, ::name.

local M = {}

--- The crafters of `all` (machine_pick's entries) whose conditions hold
--- under `rule`, every one when there is no rule.
function M.eligible(rule, all)
  if not rule then return all end
  local out = {}
  for _, one in ipairs(all) do
    if rule.allows(one.proto) then out[#out + 1] = one end
  end
  return out
end

--- Every crafting category some machine has: true when an eligible machine
--- has it here, false when only ruled-out machines do.
local function categories_of(all, eligible)
  local out = {}
  for _, one in ipairs(all) do
    for category in pairs(one.categories) do out[category] = out[category] or false end
  end
  for _, one in ipairs(eligible) do
    for category in pairs(one.categories) do out[category] = true end
  end
  return out
end

--- Whether some machine makes `recipe` here, and whether any does at all.
local function made_here(categories, recipe)
  local any = false
  for category, here in pairs(categories) do
    if recipe.has_category(category) then
      if here then return true, true end
      any = true
    end
  end
  return false, any
end

--- The predicate recipe_rank applies to every candidate on the surface:
--- the recipe's own conditions hold, and a machine that may run here makes
--- it, or no machine anywhere does. Each recipe is decided once a call.
function M.recipe_rule(rule, all, eligible)
  local categories = categories_of(all, eligible)
  local known = {}
  return function(recipe)
    local answer = known[recipe.name]
    if answer == nil then
      answer = rule.allows(recipe)
      if answer then
        local here, any = made_here(categories, recipe)
        answer = here or not any
      end
      known[recipe.name] = answer
    end
    return answer
  end
end

return M
