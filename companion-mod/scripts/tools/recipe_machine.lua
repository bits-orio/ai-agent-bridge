-- AI Agent Bridge - scripts/tools/recipe_machine.lua
-- Author: bits-orio
-- License: MIT
--
-- What a picked recipe nets on the machine chosen to make it. A crafting
-- machine can carry productivity of its own: effect_receiver.base_effect
-- gives the electromagnetic plant, the foundry and the biochamber +50%
-- (measured on the live 2.0.77 save, and a modded crafter there too), and
-- machine_pick picks those first wherever the force can build them, being
-- the fastest. A walk that ignored it counted their inputs 1.5 times over,
-- and a machine count it was given made two thirds of its real rate.
--
-- So each pick is netted again at the force's research bonus plus the
-- machine's own, the sum capped at the recipe's maximum_productivity
-- (recipe_yield.bonus, which says what was measured). A pick whose machine
-- adds nothing comes back as it was. Which recipe is picked is decided on
-- research alone (recipe_rank.lua): the machine is chosen for the recipe,
-- after it.
--
-- A recipe no crafting machine can make keeps its research figures.

local machine_pick = require("scripts.tools.machine_pick")
local yield        = require("scripts.tools.recipe_yield")

local M = {}

--- `pick` for the item at `key` as it nets on its machine: unchanged when the
--- machine adds no productivity, else a copy with io, net and byproducts at
--- the summed bonus and machine_bonus = true.
local function on_machine(ctx, pick, key)
  local machine = machine_pick.for_recipe(ctx, pick.proto)
  local own = machine and machine.productivity or 0
  if own == 0 then return pick end
  local io = yield.net(pick.proto, yield.bonus(pick.recipe, pick.proto, own))
  local out = {}
  for field, value in pairs(pick) do out[field] = value end
  out.io, out.net, out.byproducts, out.machine_bonus = io, io[key], yield.outputs(io) > 1, true
  return out
end

--- A picker that answers as `picker` (recipe_pick.memo) does, with every pick
--- netted on its machine from `ctx` (machine_pick.context). Each pick is
--- worked out once a call.
function M.picker(picker, ctx)
  local netted = {}
  local wrapped = { prefetch = picker.prefetch }
  function wrapped.pick(kind, name)
    local found, locked, mined = picker.pick(kind, name)
    if not found then return found, locked, mined end
    local key = yield.key(kind, name)
    netted[key] = netted[key] or on_machine(ctx, found, key)
    return netted[key], locked, mined
  end
  return wrapped
end

return M
