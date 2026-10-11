-- AI Agent Bridge - tests/lua/aab_answer_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Answers at their caps. The service clips every artifact before it sends
-- it (service/internal/agent/artifact.go and budget.go) and the companion
-- clips again when it renders; the two must agree, or the companion cuts a
-- legal answer short. Every cap below is read from the service's own Go
-- source, never copied, so a cap changed on one side and not the other
-- turns this file red.
--
-- Also here: an artifact at the service's whole-answer budget gets through
-- the answer op, and on_answer tells a subscriber whether the answer was
-- private.

local SP = TEST_DIR
local MOD = REPO_DIR .. "companion-mod/"
package.path = MOD .. "?.lua;" .. SP .. "?.lua;" .. package.path

local F = require("fakegame")
local S = F.install()

local passes, fails = 0, 0
local function check(name, ok, detail)
  if ok then passes = passes + 1; print("PASS " .. name)
  else fails = fails + 1; print("FAIL " .. name .. ": " .. tostring(detail)) end
end

local loaded, err = pcall(dofile, MOD .. "control.lua")
check("companion control.lua loads", loaded, err)
if not loaded then os.exit(1) end
S.on_init()

local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies]), S.rcon_replies[#S.rcon_replies]
end

--- One integer constant from a Go file under service/internal/agent, or nil.
local function go_const(file, name)
  local f = io.open(REPO_DIR .. "service/internal/agent/" .. file, "r")
  if not f then return nil end
  local src = f:read("a")
  f:close()
  return tonumber(src:match("%f[%w]" .. name .. "%s*=%s*(%d+)"))
end

local CAPS = {
  MaxSummaryLines   = go_const("artifact.go", "MaxSummaryLines"),
  MaxCellChars      = go_const("artifact.go", "MaxCellChars"),
  MaxListItems      = go_const("artifact.go", "MaxListItems"),
  MaxTableColumns   = go_const("artifact.go", "MaxTableColumns"),
  MaxTableRows      = go_const("artifact.go", "MaxTableRows"),
  MaxComparisonRows = go_const("artifact.go", "MaxComparisonRows"),
  MaxArtLines       = go_const("artifact.go", "MaxArtLines"),
  MaxArtifactBytes  = go_const("budget.go", "MaxArtifactBytes"),
}
for name, value in pairs(CAPS) do
  check("the service's " .. name .. " is readable from its source", type(value) == "number" and value > 0, value)
end
-- The contract both tracks build to: the cell and the whole answer doubled,
-- the counts as they were.
check("the service caps are the contract's: 320 characters a cell, 12000 bytes an answer",
      CAPS.MaxCellChars == 320 and CAPS.MaxArtifactBytes == 12000, F.encode(CAPS))

local ELLIPSIS = "\u{2026}"
local shapes = require("scripts.render_shapes")

local function many(n, make)
  local out = {}
  for i = 1, n do out[i] = make(i) end
  return out
end

-- ── each count cap, one past it ───────────────────────────────────────
-- Mutation-tested: each clip in render_shapes.lua set one below its Go
-- constant turns its own check red.
check("a summary keeps MaxSummaryLines lines",
      #shapes.summary({ lines = many(CAPS.MaxSummaryLines + 1, tostring) }) == CAPS.MaxSummaryLines)
check("a list keeps MaxListItems items",
      #shapes.list({ items = many(CAPS.MaxListItems + 1, tostring) }) == CAPS.MaxListItems)
check("a comparison keeps MaxComparisonRows rows under its heading",
      #shapes.comparison({ columns = { "a", "b" }, rows = many(CAPS.MaxComparisonRows + 1, function(i)
        return { label = "r" .. i, a = "1", b = "2" } end) }) == 1 + CAPS.MaxComparisonRows)
-- An icon takes about two and a half characters in the monospace font, so
-- a column of icons is padded to that, not to one. Mutation-tested: counting
-- an icon as one cell fails it.
do
  local t = shapes.table({ columns = { "Team", "Research", "Pct" },
    rows = { { "a", "[img=item.iron-plate]", "1%" }, { "b", "[img=item.iron-plate] x", "2%" } } })
  check("a cell with one icon is padded as three characters wide",
        t[3] == "[font=aab-mono]a    | [img=item.iron-plate]      |  1%[/font]", t[3])
end
check("art keeps MaxArtLines lines",
      #shapes.art({ lines = many(CAPS.MaxArtLines + 1, tostring) }) == CAPS.MaxArtLines)

-- ── art: spaces kept, monospace, nothing decorated ────────────────────
-- A player asked for ASCII art; the list shape bulleted it in the chat
-- font and the service flattened its spaces. Mutation-tested: decorating
-- art lines turns "team-6" into its label, and dropping the font wrap
-- fails the first check.
do
  local drawn = shapes.art({ lines = { "    [o_x]   ", "   /|_|_|\\", "", "  team-6 [img=entity.biter] [font=default-bold]x[/font]" } })
  check("art is set in the monospace font with its leading spaces kept and no bullet",
        drawn[1] == "[font=aab-mono]    [o_x][/font]" and drawn[2] == "[font=aab-mono]   /|_|_|\\[/font]", F.encode(drawn))
  check("a blank art line keeps its row", drawn[3] == "[font=aab-mono] [/font]", F.encode(drawn))
  check("art is never decorated, a bad sprite is repaired and font tags go",
        drawn[4] == "[font=aab-mono]  team-6 biter x[/font]", drawn[4])
end
local wide = { columns = many(CAPS.MaxTableColumns + 1, function(i) return "c" .. i end),
               rows = many(CAPS.MaxTableRows + 1, function(i)
                 return many(CAPS.MaxTableColumns + 1, function(j) return i .. "." .. j end) end) }
local grid = shapes.grid(wide)
check("a table keeps MaxTableColumns columns", #grid.columns == CAPS.MaxTableColumns and #grid.rows[1] == CAPS.MaxTableColumns,
      #grid.columns)
check("a table keeps MaxTableRows rows", #grid.rows == CAPS.MaxTableRows, #grid.rows)
-- The chat text, in the mono font: the header, the rule, and every row.
check("the chat table prints all MaxTableRows rows below its header and rule",
      #shapes.table(wide) == 2 + CAPS.MaxTableRows, #shapes.table(wide))
local mono = prototypes.font["aab-mono"]
prototypes.font["aab-mono"] = nil
check("and so does the plain table when the font is missing", #shapes.table(wide) == 1 + CAPS.MaxTableRows,
      #shapes.table(wide))
prototypes.font["aab-mono"] = mono

-- ── a cell at MaxCellChars is never cut ───────────────────────────────
-- 320 characters of four-byte emoji are 1280 bytes: the most a cell the
-- service sent can weigh, so the companion's net sits exactly there.
local FOUR = "\u{1F600}"
local full_cell = string.rep(FOUR, CAPS.MaxCellChars)
local kept = shapes.summary({ lines = { full_cell } })[1]
check("a cell of MaxCellChars four-byte characters renders whole", kept == full_cell, #kept)
local over = shapes.summary({ lines = { full_cell .. FOUR } })[1]
check("one character more is cut on a character boundary and ends with an ellipsis, within the net",
      #over == CAPS.MaxCellChars * 4 - 1 and over:sub(-3) == ELLIPSIS and over:sub(1, -4) == string.rep(FOUR, CAPS.MaxCellChars - 1),
      #over)
check("a title is a cell like any other", shapes.title_of({ title = full_cell }) == full_cell)

-- ── the whole answer at the service's budget ──────────────────────────
-- The service marshals an artifact to at most MaxArtifactBytes and the rcon
-- client takes 262144 bytes a command; the companion caps no request, so a
-- full-size answer must arrive whole. A table at every cap, cells sized so
-- the artifact lands just under the budget.
local function table_artifact(cell_chars)
  return { shape = "table", title = "Every cap at once",
           columns = many(CAPS.MaxTableColumns, function(i) return "col" .. i end),
           rows = many(CAPS.MaxTableRows, function(r)
             return many(CAPS.MaxTableColumns, function(c)
               return (r .. "." .. c .. " " .. string.rep("x", cell_chars)):sub(1, cell_chars) end) end) }
end
local size = 1
while #F.encode(table_artifact(size + 1)) <= CAPS.MaxArtifactBytes and size < CAPS.MaxCellChars do size = size + 1 end
local biggest = table_artifact(size)
local bytes = #F.encode(biggest)
check("the test artifact sits just under the service's budget (" .. bytes .. " bytes)",
      bytes <= CAPS.MaxArtifactBytes and bytes > CAPS.MaxArtifactBytes - 200, bytes)
local qid = remote.call("ai-agent-bridge-v1", "ask", { text = "everything", force = "player", player_index = 1 })
local request = F.encode({ v = 1, op = "answer", qid = qid, artifact = biggest })
local reply = rpc({ op = "answer", qid = qid, artifact = biggest })
check("an answer at the budget gets through the answer op (" .. #request .. " byte command)", reply.ok, F.encode(reply))
local lines = rpc({ op = "answers", after = qid - 1 }).r[1].lines
check("and renders every row, nothing cut", #lines == 3 + CAPS.MaxTableRows
      and lines[#lines]:find(ELLIPSIS, 1, true) == nil and lines[#lines]:find("8.5 xxx", 1, true) ~= nil, #lines)

-- ── on_answer says whether the answer is private ──────────────────────
-- A subscriber relaying answers elsewhere, a Discord bridge one day, must
-- keep a private answer inside its channel. The payload carries the
-- question's scope and private, beside everything it always carried.
-- Mutation-tested: dropping either field turns its checks red.
local answered
local event_id = remote.call("ai-agent-bridge-v1", "get_event_id", "on_answer")
script.on_event(event_id, function(e) answered = e end)
local open_qid = remote.call("ai-agent-bridge-v1", "ask", { text = "open", force = "player", player_index = 1 })
rpc({ op = "answer", qid = open_qid, artifact = { shape = "notice", text = "for everyone" } })
check("on_answer for a public question says global and not private",
      answered ~= nil and answered.qid == open_qid and answered.scope == "global" and answered.private == false, F.encode(answered))
check("and still carries what it always did", answered ~= nil and answered.question == "open"
      and answered.shape == "notice" and answered.artifact.text == "for everyone" and answered.lines[1] == "for everyone",
      F.encode(answered))
local closed_qid = remote.call("ai-agent-bridge-v1", "ask", { text = "closed", force = "team-3",
  scope = { key = "team-3", private = true, audience = { force = "team-3" } } })
rpc({ op = "answer", qid = closed_qid, artifact = { shape = "notice", text = "for team 3" } })
check("on_answer for a private question carries its scope and private = true",
      answered ~= nil and answered.qid == closed_qid and answered.scope == "team-3" and answered.private == true, F.encode(answered))

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
