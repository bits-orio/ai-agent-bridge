-- Pure formatters: one artifact table -> an array of chat-line strings, one
-- shape each (ADR 0003, PLAN.md "Artifacts, v1 shapes"). Every cap is
-- enforced by clipping, never by erroring -- rendering is best-effort
-- presentation, not protocol validation.
--
-- Every string value passes through line(), which strips control characters
-- (mainly newlines) before it reaches chat. Tool results and question text
-- are player-typed data the model may echo back into an artifact; without
-- this, a value like "a\nSomeone: fake line" could forge an extra chat line
-- (PLAN.md open question 6, prompt injection). Factorio rich-text tags are
-- left alone, a supported feature of player.print.

local function line(v)
  return tostring(v):gsub("%c+", " ")
end

local function clip(t, n)
  local out = {}
  for i = 1, math.min(n, #t) do out[i] = t[i] end
  return out
end

local M = {}

-- { shape="summary", lines={...} } -- up to three lines.
function M.summary(a)
  local out = {}
  for _, l in ipairs(clip(a.lines or {}, 3)) do
    out[#out + 1] = line(l)
  end
  return out
end

-- { shape="notice", text="...", level="warning"|"confirmation" } -- one line.
function M.notice(a)
  return { line(a.text or "") }
end

-- { shape="list", items={...} } -- up to ten one-line rows.
function M.list(a)
  local out = {}
  for _, item in ipairs(clip(a.items or {}, 10)) do
    out[#out + 1] = "- " .. line(item)
  end
  return out
end

-- { shape="comparison", columns={"A","B"}, rows={{label=,a=,b=},...} } -- up to five rows.
function M.comparison(a)
  local cols = a.columns or {}
  local out = { line((cols[1] or "A") .. "  vs  " .. (cols[2] or "B")) }
  for _, row in ipairs(clip(a.rows or {}, 5)) do
    out[#out + 1] = string.format("- %s: %s vs %s", line(row.label or ""), line(row.a), line(row.b))
  end
  return out
end

-- { shape="table", columns={...}, rows={{...},...} } -- up to five columns, eight rows.
function M.table(a)
  local cols = clip(a.columns or {}, 5)
  local out = { line(table.concat(cols, " | ")) }
  for _, row in ipairs(clip(a.rows or {}, 8)) do
    local cells = {}
    for i = 1, #cols do
      cells[i] = line(row[i])
    end
    out[#out + 1] = table.concat(cells, " | ")
  end
  return out
end

return M
