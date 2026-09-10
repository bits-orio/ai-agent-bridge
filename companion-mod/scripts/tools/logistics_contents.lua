-- What one logistic network is holding, aggregated the way a player thinks
-- about it: by item name, with the qualities folded in.
--
-- get_contents returns one row per name and quality pair, so a base that makes
-- the same plate at three qualities spends three of the reply's rows saying
-- "iron plate" and reports a distinct_items count nobody asked for. Summing by
-- name first means the eight rows that come back are the eight biggest STACKS,
-- and a name with more than one quality carries a qualities breakdown so the
-- detail is not lost.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10):
--   LuaLogisticNetwork::get_contents() -> array[ItemWithQualityCount], a fresh
--   array the caller may sort in place; ItemWithQualityCount is
--   {name :: string, quality :: string, count :: ItemCountType}.

local M = {}

--- Sums one network's contents by item name. Returns the list of
--- {name, count, qualities, kinds, only} in no particular order, where `only`
--- is the single quality of a name held at just one.
local function by_name(rows)
  local totals, order = {}, {}
  for _, row in ipairs(rows) do
    local item = totals[row.name]
    if not item then
      item = { name = row.name, count = 0, qualities = {}, kinds = 0 }
      totals[row.name] = item
      order[#order + 1] = item
    end
    item.count = item.count + row.count
    local quality = row.quality or "normal"
    if not item.qualities[quality] then
      item.kinds = item.kinds + 1
      item.only = quality
    end
    item.qualities[quality] = (item.qualities[quality] or 0) + row.count
  end
  return order
end

--- The `max` largest item names in one network, largest count first, plus how
--- many distinct names the network holds.
---
--- A name held at one quality gets no breakdown, because on most saves
--- everything is normal and a repeated "normal" on every row is reply bytes
--- spent saying nothing. The one exception is a name held at a single quality
--- that is not normal, which keeps that quality on the row: those 2,000 plates
--- being uncommon is the whole point of the row.
function M.largest(network, max)
  local items = by_name(network.get_contents())
  table.sort(items, function(x, y)
    if x.count ~= y.count then return x.count > y.count end
    return x.name < y.name
  end)

  local top = {}
  for i = 1, math.min(max, #items) do
    local item = items[i]
    local single = (item.kinds == 1) and item.only or nil
    top[i] = {
      name = item.name,
      count = item.count,
      quality = (single ~= "normal") and single or nil,
      qualities = (item.kinds > 1) and item.qualities or nil,
    }
  end
  return top, #items
end

return M
