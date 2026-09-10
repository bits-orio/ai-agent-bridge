-- Where a technology's progress fraction lives, which is two places: the
-- running technology's progress is on the force, and everything else keeps the
-- fraction it had when it was last worked on, on the technology itself. Shared
-- by research_queue and tech_status so the two can never disagree about it.
--
-- API names verified against the official 2.0.77 docs (fetched 2026-09-10):
--   LuaForce::research_progress   RW double, "Progress of current research, as
--                                 a number in range [0, 1]."
--   LuaTechnology::saved_progress RW double, "Saved technology progress
--                                 fraction as a value in range [0, 1)."

local M = {}

--- The progress fraction of one technology, or nil when there is no technology.
function M.of(force, tech, current_name)
  if not tech then return nil end
  if current_name and tech.name == current_name then return force.research_progress end
  return tech.saved_progress
end

return M
