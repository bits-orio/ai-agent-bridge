-- Shared window and surface resolution for every tool that reads
-- LuaFlowStatistics, plus the sample arithmetic production_since needs.
--
-- Verified against the official LuaFlowStatistics docs (fetched 2026-09-10),
-- not from memory:
--   get_flow_count{name, category, precision_index, sample_index?, count?}
--   "Each precision level contains 300 samples of data so at a precision of
--    1 minute, each sample contains data averaged across 60s / 300 = 0.2s
--    = 12 ticks."
--   sample_index "must be between 1 and 300 where 1 is the most recent sample
--    and 300 is the oldest."
--   count "If true, the count of items/fluids/entities is returned instead of
--    the per-time-frame value."
--   "All return values are normalized to be per-tick for electric networks and
--    per-minute for all other types."

local SAMPLES_PER_WINDOW = 300
local TICKS_PER_SECOND = 60

-- Every precision the engine offers, in seconds. Built into WINDOWS below by
-- looking each name up in defines, so a Factorio build that lacks one of them
-- simply does not offer it rather than registering a window whose precision is
-- nil and failing later inside get_flow_count.
local SECONDS = {
  five_seconds            = 5,
  one_minute              = 60,
  ten_minutes             = 600,
  one_hour                = 3600,
  ten_hours               = 36000,
  fifty_hours             = 180000,
  two_hundred_fifty_hours = 900000,
  one_thousand_hours      = 3600000,
}

local WINDOWS = {}
local ORDER = {}
for name, seconds in pairs(SECONDS) do
  local precision = defines.flow_precision_index[name]
  if precision then
    WINDOWS[name] = { precision = precision, ticks = seconds * TICKS_PER_SECOND }
    ORDER[#ORDER + 1] = name
  end
end
table.sort(ORDER, function(x, y) return SECONDS[x] < SECONDS[y] end)

local M = {}

M.SAMPLES_PER_WINDOW = SAMPLES_PER_WINDOW

-- "five_seconds, one_minute, ..." for tool manifests, so a description can
-- never drift out of step with what require_window actually accepts.
M.window_names = table.concat(ORDER, ", ")

--- Resolves a window name to {precision, ticks}, or error()s with the full
--- list of names, which scripts/probe.lua turns into a provider_error reply.
function M.require_window(name)
  local window = WINDOWS[name]
  if not window then
    error("unknown window: " .. tostring(name) .. " (want one of " .. M.window_names .. ")")
  end
  return window
end

--- Checks a surface name exists and returns it unchanged.
function M.require_surface(name)
  if type(name) ~= "string" or not game.surfaces[name] then
    error("unknown surface: " .. tostring(name))
  end
  return name
end

--- The smallest window whose span covers elapsed_ticks, as name, window. Falls
--- back to the longest window the engine offers when nothing covers it, so a
--- very old tick still answers with the best history that exists.
function M.window_covering(elapsed_ticks)
  for _, name in ipairs(ORDER) do
    if WINDOWS[name].ticks >= elapsed_ticks then
      return name, WINDOWS[name]
    end
  end
  local longest = ORDER[#ORDER]
  return longest, WINDOWS[longest]
end

return M
