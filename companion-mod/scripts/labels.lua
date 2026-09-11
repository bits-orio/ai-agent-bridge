-- Force labels: what players call a force (docs/design/phase3-spec.md,
-- "Force labels"). A team mod knows that "team-1" is Team Ace; the companion
-- does not, and the model must never say team-1 to a player who knows it as
-- Team Ace. Any remote interface exposing a zero-argument `force_labels_v1`
-- returning { [force_name] = label } is a labels provider, found by scan
-- like every other probe and never stored.

local PROBE_FN = "force_labels_v1"
local LABEL_LIMIT = 64

local M = {}

--- Sorted names of every interface exposing the probe.
function M.provider_names()
  local names = {}
  for iface_name, functions in pairs(remote.interfaces) do
    if functions[PROBE_FN] then names[#names + 1] = iface_name end
  end
  table.sort(names)
  return names
end

--- A label as plain text: rich text tags stripped, control characters and
--- runs of spaces collapsed, clipped.
local function plain(label)
  if type(label) ~= "string" then return nil end
  local text = label:gsub("%[[^%[%]]*%]", ""):gsub("%c+", " "):gsub("%s+", " ")
  text = text:match("^%s*(.-)%s*$")
  if text == "" then return nil end
  if #text > LABEL_LIMIT then text = text:sub(1, LABEL_LIMIT) end
  return text
end

--- { [force_name] = label } merged over every provider; the first provider
--- by interface name wins a force two of them label. Forces the game does
--- not have are dropped.
function M.map()
  local out = {}
  for _, iface_name in ipairs(M.provider_names()) do
    local answered, labels = pcall(remote.call, iface_name, PROBE_FN)
    if answered and type(labels) == "table" then
      for force_name, label in pairs(labels) do
        local text = plain(label)
        if type(force_name) == "string" and text and out[force_name] == nil and game.forces[force_name] then
          out[force_name] = text
        end
      end
    elseif not answered then
      log("[ai-agent-bridge] force labels provider " .. iface_name .. " failed: " .. tostring(labels))
    end
  end
  return out
end

--- The same as an array of { name, label } sorted by name: the `labels` op.
function M.all()
  local rows = {}
  for force_name, label in pairs(M.map()) do
    rows[#rows + 1] = { name = force_name, label = label }
  end
  table.sort(rows, function(x, y) return x.name < y.name end)
  return rows
end

return M
