-- AI Agent Bridge - scripts/tools/recipe_yield.lua
-- Author: bits-orio
-- License: MIT
--
-- What one craft of one recipe gives and takes for one force: the arithmetic
-- recipe_pick ranks by and recipe_walk multiplies. Reads the prototype's own
-- products and ingredients; nothing is kept and nothing is written.
--
-- One craft gives of one product, as the engine counts it,
--
--     c * mean over the roll of ( (r + e) + p * max(r + e - g, 0) )
--
-- with c the chance it is given, r the amount rolled, e its
-- extra_count_fraction, g its ignored_by_productivity and p the productivity
-- bonus. Measured, not assumed: it matched 2.1.17's
-- LuaRecipePrototype::get_product_amount on every product of base, Space Age
-- and quality (1092) and on 42 synthetic probes at six bonuses, and the
-- output of real crafting machines on 2.0.77 and 2.1.17. Three readings of
-- the plain expected amount get it wrong, so the formula says each one:
--   * the chance gates the extra count too: 3 at a half chance with half an
--     extra is 1.75 a craft, not 2.
--   * the skip comes off the roll before the chance: 10 at a half chance with
--     4 skipped is 8 a craft at +100%, not 6.
--   * the skip comes off each roll of a range, not off its mean: 0 to 3 with 2
--     skipped is 1.625 a craft at +50%, not 1.5.
-- An item rolls a whole number from amount_min to amount_max, each equally
-- likely; a fluid any amount between them. Only item and fluid products
-- count: a research-progress product exists in the wild, is missing from the
-- runtime docs' Product union, and has no name to file it under.
--
-- The bonus is the force's research bonus for the recipe plus the base
-- productivity of the machine making it (recipe_machine.lua), capped at the
-- prototype's maximum_productivity; bonuses add, never multiply. Measured
-- with real machines crafting on 2.0.77 and 2.1.17: an electromagnetic
-- plant's +50% and a +30% research bonus each applied to a recipe whose
-- allowed_effects forbid productivity (big-electric-pole, 12 crafts made 18
-- and 15), and +280% research on +50% machine made 4 a craft, the sum capped
-- at 300%. So allowed_effects is not read: the engine does not gate either
-- bonus on it. Modules and beacons are not counted.
--
-- The chance a product is given moved between versions. 2.0.77's Product
-- carries `probability`. 2.1 drops it for `independent_probability` and
-- `shared_probability = {min, max}`, and its prototype docs define the chance
-- as independent_probability * (max - min). Reading `probability` alone would
-- count every 2.1 chance as certain and print a wrong figure as fact.
--
-- API names verified against runtime-api.json 2.0.77 and 2.1.17:
--   LuaRecipePrototype::products (array[Product]), ::ingredients
--   (array[Ingredient]), ::maximum_productivity (double), ::name.
--   LuaRecipe::productivity_bonus (float in 2.0.77, EffectValue, also a
--   float, in 2.1).
--   ItemProduct / FluidProduct: type, name, amount?, amount_min?, amount_max?,
--   extra_count_fraction? (items), ignored_by_productivity?, and probability
--   (2.0.77) or independent_probability + shared_probability (2.1). An item's
--   amounts are whole numbers (ItemCountType), a fluid's are not.
--   Ingredient: type ("item" or "fluid"), name, amount.

local M = {}

local COUNTED = { item = true, fluid = true }

--- Where a walk files one item or fluid. An item and a fluid may share a name,
--- so the kind is part of the key.
function M.key(kind, name)
  return kind .. "/" .. name
end

--- The kind and the name back out of a key.
function M.split(key)
  return key:match("^(%l+)/(.*)$")
end

--- The chance one product is given per craft, either version's shape.
local function chance(product)
  if type(product.probability) == "number" then return product.probability end
  local independent = product.independent_probability
  if type(independent) ~= "number" then return 1 end
  local shared = product.shared_probability
  if type(shared) == "table" and type(shared.min) == "number" and type(shared.max) == "number" then
    return independent * math.max(0, shared.max - shared.min)
  end
  return independent
end

--- The least and the most one roll gives. A max below the min means the min,
--- as the prototype docs say the game reads it.
local function bounds(product)
  if type(product.amount) == "number" then return product.amount, product.amount end
  local low = product.amount_min or 0
  return low, math.max(product.amount_max or low, low)
end

--- The mean over one roll of max(r - threshold, 0): the part of a roll that
--- lies above `threshold`. An item rolls each whole number from low to high
--- alike; a fluid rolls evenly over the span.
local function above(kind, low, high, threshold)
  if high <= low then return math.max(low - threshold, 0) end
  if kind == "fluid" then
    if high <= threshold then return 0 end
    local from = math.max(low, threshold)
    return ((high - threshold) ^ 2 - (from - threshold) ^ 2) / (2 * (high - low))
  end
  local first = math.max(low, math.ceil(threshold))
  if first > high then return 0 end
  return (high - first + 1) * ((first + high) / 2 - threshold) / (high - low + 1)
end

--- What one craft gives of one product at productivity bonus `bonus`, on
--- average; at bonus 0, what it gives before productivity.
function M.output(product, bonus)
  local low, high = bounds(product)
  local extra = product.extra_count_fraction or 0
  local threshold = (product.ignored_by_productivity or 0) - extra
  local boosted = bonus * above(product.type, low, high, threshold)
  return chance(product) * ((low + high) / 2 + extra + boosted)
end

--- The productivity bonus one recipe crafts at: the force's research bonus
--- from its LuaRecipe (nil reads as none) plus `machine`, a machine's base
--- productivity (nil reads as none), capped at the prototype's own maximum
--- and never below zero: the prototype docs' Effect gives productivity a
--- minimum possible sum of 0%, and a script may write any EffectValue into
--- LuaRecipe::productivity_bonus.
function M.bonus(recipe, proto, machine)
  local bonus = (recipe and recipe.productivity_bonus or 0) + (machine or 0)
  return math.max(0, math.min(bonus, proto.maximum_productivity))
end

--- Net amount per craft of everything one recipe touches, keyed by M.key:
--- above zero it comes out, below zero it goes in. Netting is what turns a
--- catalyst like kovarex's 40 uranium-235 into the one it really makes.
function M.net(proto, bonus)
  local io = {}
  for _, product in ipairs(proto.products) do
    if COUNTED[product.type] then
      local key = M.key(product.type, product.name)
      io[key] = (io[key] or 0) + M.output(product, bonus)
    end
  end
  for _, ingredient in ipairs(proto.ingredients) do
    local key = M.key(ingredient.type, ingredient.name)
    io[key] = (io[key] or 0) - ingredient.amount
  end
  return io
end

--- How many different things one craft nets out. More than one means the
--- recipe has byproducts the walk does not balance.
function M.outputs(io)
  local n = 0
  for _, amount in pairs(io) do
    if amount > 0 then n = n + 1 end
  end
  return n
end

return M
