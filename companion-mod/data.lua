-- AI Agent Bridge - data.lua
-- Author: bits-orio
-- License: MIT
--
-- One font, for the table shape. Columns line up only when every character
-- is the same width, and the game's chat font is not: on the live server
-- every table answer printed with its column boundaries wandering from row
-- to row. The game ships a monospace face already, core's default-mono
-- descriptor (NotoMono, DejaVuSansMono behind it for the rest of Unicode;
-- every locale that overrides fonts defines it, the others inherit it), but
-- no font prototype uses it, so one is declared here at the chat font's own
-- size and scripts/render_shapes.lua sets each table row in it.
data:extend({
  {
    type = "font",
    name = "aab-mono",
    from = "default-mono",
    size = 14,
  },
})
