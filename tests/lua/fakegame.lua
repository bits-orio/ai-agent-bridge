-- A stand-in for enough of the Factorio control-stage API to load the AI
-- Agent Bridge companion and drive its protocol. Not a simulator: every value
-- is the smallest thing the mod's own code touches.

local F = {}

-- ── JSON ──────────────────────────────────────────────────────────────
local function is_array(t)
  local n = 0
  for k in pairs(t) do
    if type(k) ~= "number" then return false end
    n = n + 1
  end
  return n == #t
end

local function esc(s)
  return (s:gsub('[%c"\\]', function(c)
    if c == '"' then return '\\"' elseif c == '\\' then return '\\\\'
    elseif c == '\n' then return '\\n' else return string.format('\\u%04x', c:byte()) end
  end))
end

local function encode(v)
  local t = type(v)
  if t == "nil" then return "null" end
  if t == "boolean" then return tostring(v) end
  if t == "number" then
    if v == math.floor(v) and math.abs(v) < 2^53 then return string.format("%d", v) end
    return tostring(v)
  end
  if t == "string" then return '"' .. esc(v) .. '"' end
  if t == "table" then
    if is_array(v) then
      local parts = {}
      for i = 1, #v do parts[i] = encode(v[i]) end
      return "[" .. table.concat(parts, ",") .. "]"
    end
    local keys = {}
    for k in pairs(v) do keys[#keys + 1] = tostring(k) end
    table.sort(keys)
    local parts = {}
    for _, k in ipairs(keys) do
      local value = v[k]
      if value == nil then value = v[tonumber(k)] end
      parts[#parts + 1] = '"' .. esc(k) .. '":' .. encode(value)
    end
    return "{" .. table.concat(parts, ",") .. "}"
  end
  error("cannot encode a " .. t)
end

local decode
local function skip(s, i) return (s:find("[^ \t\r\n]", i)) or #s + 1 end
decode = function(s, i)
  i = skip(s, i)
  local c = s:sub(i, i)
  if c == "{" then
    local out = {}
    i = skip(s, i + 1)
    if s:sub(i, i) == "}" then return out, i + 1 end
    while true do
      local key; key, i = decode(s, i)
      i = skip(s, i)
      i = i + 1 -- ':'
      local value; value, i = decode(s, i)
      out[key] = value
      i = skip(s, i)
      local sep = s:sub(i, i); i = i + 1
      if sep == "}" then return out, i end
    end
  elseif c == "[" then
    local out = {}
    i = skip(s, i + 1)
    if s:sub(i, i) == "]" then return out, i + 1 end
    while true do
      local value; value, i = decode(s, i)
      out[#out + 1] = value
      i = skip(s, i)
      local sep = s:sub(i, i); i = i + 1
      if sep == "]" then return out, i end
    end
  elseif c == '"' then
    local out, j = {}, i + 1
    while true do
      local ch = s:sub(j, j)
      if ch == '"' then return table.concat(out), j + 1 end
      if ch == "\\" then
        local n = s:sub(j + 1, j + 1)
        out[#out + 1] = (n == "n" and "\n") or (n == "t" and "\t") or n
        j = j + 2
      else
        out[#out + 1] = ch; j = j + 1
      end
    end
  elseif s:sub(i, i + 3) == "true" then return true, i + 4
  elseif s:sub(i, i + 4) == "false" then return false, i + 5
  elseif s:sub(i, i + 3) == "null" then return nil, i + 4
  else
    local num = s:match("^-?%d+%.?%d*[eE]?[-+]?%d*", i)
    if not num or num == "" then error("bad json at " .. i .. ": " .. s:sub(i, i + 20)) end
    return tonumber(num), i + #num
  end
end

F.encode = encode
F.decode = function(s) local v = decode(s, 1) return v end

-- ── GUI ───────────────────────────────────────────────────────────────
local function element(spec, parent)
  local el = {}
  el.type = spec.type
  el.name = spec.name
  el.caption = spec.caption
  el.sprite = spec.sprite
  el.column_count = spec.column_count
  el.style = {}
  el.kids = {}
  el.valid = true
  el.parent = parent
  el.add = function(child_spec)
    local child = element(child_spec, el)
    el.kids[#el.kids + 1] = child
    if child_spec.name then el[child_spec.name] = child end
    return child
  end
  el.destroy = function()
    el.valid = false
    if parent and el.name then parent[el.name] = nil end
  end
  return el
end
F.element = element

-- ── The rest of the engine ────────────────────────────────────────────
--- Installs every global the companion touches and returns a handle the test
--- can poke: forces, players, event handlers, written files, rcon replies.
function F.install(opts)
  opts = opts or {}
  local S = {
    tick = opts.tick or 100000,
    files = {},
    rcon_replies = {},
    printed = {},
    handlers = {},
    interfaces = {},
    commands = {},
    next_event_id = 1000,
    settings = {
      ["aab-events-enabled"] = { value = true },
      ["aab-chat-prefix"]    = { value = "" },
      ["aab-answer-audience"] = { value = "server" },
    },
  }

  -- Each quality's share of the same item's flow, so a sum over qualities is
  -- visibly more than the normal-quality figure alone. input 60/min and 2 an
  -- item sample at normal, half of each at uncommon.
  S.quality_share = { normal = 1, uncommon = 0.5 }
  S.qualities_asked = {}

  local stats = {
    input_counts  = { ["iron-plate"] = 1000, ["copper-plate"] = 500 },
    output_counts = { ["iron-plate"] = 400 },
    get_flow_count = function(a)
      assert(type(a) == "table", "get_flow_count wants one table")
      assert(a.name and a.category and a.precision_index, "get_flow_count is missing a field")
      assert(a.category == "input" or a.category == "output" or a.category == "storage", a.category)
      -- FlowStatisticsID for item statistics is an ItemWithQualityID: a bare
      -- prototype name means NORMAL quality alone, an ItemIDAndQualityIDPair
      -- {name, quality} names one quality. Both forms are accepted here and what
      -- the caller asked for is recorded, because a stub that ignores the
      -- quality half hides exactly the bug that made this matter.
      local item, quality = a.name, "normal"
      if type(a.name) == "table" then item, quality = a.name.name, a.name.quality or "normal" end
      assert(type(item) == "string", "get_flow_count wants an item prototype name")
      assert(S.quality_share[quality], "no quality prototype named " .. tostring(quality))
      S.qualities_asked[quality] = (S.qualities_asked[quality] or 0) + 1
      local share = S.quality_share[quality]
      if a.sample_index then
        assert(a.sample_index >= 1 and a.sample_index <= 300, "sample_index " .. a.sample_index)
        assert(a.count == true, "a sample read asks for a count, not a rate")
        return (a.category == "input" and 2 or 1) * share
      end
      return (a.category == "input" and 60 or 20) * share
    end,
  }
  S.stats = stats

  local nauvis = { name = "nauvis", index = 1, valid = true, planet = { name = "nauvis", valid = true } }
  local orbit  = { name = "platform-1", index = 2, valid = true, planet = nil }
  -- LuaGameScript::surfaces is a LuaCustomTable[uint32 or string -> LuaSurface]:
  -- "this sparse table allows you to find surfaces by indexing it with either
  -- their name or index", and pairs() walks each surface once. The indices live
  -- in the table itself and the names on a metatable, so a lookup by either
  -- works while an iteration is not doubled.
  S.surfaces = setmetatable({ nauvis, orbit },
    { __index = { nauvis = nauvis, ["platform-1"] = orbit } })

  -- ── surface reads the breadth tools need ────────────────────────────
  -- LuaSurface::count_entities_filtered, get_total_pollution, pollutant_type.
  -- The filter is checked rather than ignored, because passing a LuaForce where
  -- a name belongs is exactly the mistake a stub that accepts anything hides.
  S.entity_counts = { lab = 12, ["assembling-machine-2"] = 340 }
  S.pollution = { nauvis = 1234.5, ["platform-1"] = 0 }
  for _, surface in pairs(S.surfaces) do
    local this = surface
    this.count_entities_filtered = function(filter)
      assert(type(filter) == "table", "count_entities_filtered wants one table")
      assert(type(filter.name) == "string", "count_entities_filtered wants a name string")
      assert(type(filter.force) == "string", "count_entities_filtered wants a force name")
      S.last_entity_filter = filter
      if this.name ~= "nauvis" then return 0 end
      return S.entity_counts[filter.name] or 0
    end
    this.get_total_pollution = function() return S.pollution[this.name] or 0 end
    -- LuaSurface::find_entities_filtered: name, type, force and limit are
    -- honoured; everything lives on nauvis.
    this.find_entities_filtered = function(filter)
      assert(type(filter) == "table", "find_entities_filtered wants one table")
      assert(type(filter.force) == "string", "find_entities_filtered wants a force name")
      S.last_find_filter = filter
      local out = {}
      if this.name ~= "nauvis" then return out end
      for _, e in ipairs(S.entities) do
        if (filter.name == nil or e.name == filter.name) and (filter.type == nil or e.type == filter.type) then
          out[#out + 1] = e
          if filter.limit and #out >= filter.limit then break end
        end
      end
      return out
    end
  end
  local function entity(name, etype, x, y, recipe)
    return { name = name, type = etype, valid = true, position = { x = x, y = y },
             get_recipe = function() return recipe and { name = recipe } or nil end }
  end
  S.entities = {
    entity("lab", "lab", 10.2, -4.7),
    entity("lab", "lab", 12.9, -4.7),
    entity("assembling-machine-2", "assembling-machine", -20.5, 33.1, "repair-pack"),
    entity("assembling-machine-2", "assembling-machine", -24.5, 33.1, "iron-gear-wheel"),
    entity("stone-furnace", "furnace", 0, 0, "iron-plate"),
  }
  nauvis.pollutant_type = { name = "pollution", valid = true }
  orbit.pollutant_type = nil

  local force
  local bob = {
    index = 1, name = "Bob", valid = true, connected = true, admin = true,
    surface = nauvis, opened = nil, position = { x = 10.4, y = -3.6 },
    print = function(text) S.printed[#S.printed + 1] = { who = "Bob", text = text } end,
  }
  bob.gui = { screen = element({ type = "empty-widget" }, nil) }
  local gone = {
    index = 2, name = "Ann", valid = true, connected = false, admin = false,
    surface = nauvis,
    print = function(text) S.printed[#S.printed + 1] = { who = "Ann", text = text } end,
  }
  gone.gui = { screen = element({ type = "empty-widget" }, nil) }

  -- ── technologies, the queue, rockets, logistic networks ─────────────
  -- LuaTechnology fields the research tools read: name, researched, enabled,
  -- level, research_unit_count, prerequisites, saved_progress. automation is
  -- done, logistics is running, logistics-2 is queued behind it and still
  -- missing one prerequisite.
  local techs = {}
  local function tech(name, fields)
    local t = { name = name, researched = false, enabled = true, level = 1,
                research_unit_count = 20, prerequisites = {}, saved_progress = 0 }
    for key, value in pairs(fields or {}) do t[key] = value end
    techs[name] = t
    return t
  end
  tech("automation", { researched = true })
  -- A level-based technology: one LuaTechnology, any number of queue entries.
  -- level 4 is what every one of those entries reads off it.
  tech("mining-productivity", { level = 4, research_unit_count = 2000, saved_progress = 0.2 })
  tech("logistics", {})
  tech("logistics-2", {
    research_unit_count = 200, saved_progress = 0.5,
    prerequisites = { automation = techs.automation, logistics = techs.logistics },
  })
  S.technologies = techs

  -- LuaForce::research_queue is typed array[TechnologyID], so the entries here
  -- are LuaTechnology-shaped. A test flips one to a plain name to prove the
  -- union is handled.
  S.research_queue = { techs.logistics, techs["logistics-2"] }

  local function network(id, fields)
    local n = { network_id = id, cells = {}, all_logistic_robots = 0,
                available_logistic_robots = 0, all_construction_robots = 0,
                available_construction_robots = 0, contents = {} }
    for key, value in pairs(fields or {}) do n[key] = value end
    -- LuaLogisticNetwork::get_contents returns array[ItemWithQualityCount], a
    -- fresh array each call, which the caller is free to sort in place.
    n.get_contents = function()
      local out = {}
      for i, row in ipairs(n.contents) do
        out[i] = { name = row.name, quality = row.quality or "normal", count = row.count }
      end
      S.contents_reads = (S.contents_reads or 0) + 1
      return out
    end
    return n
  end
  S.networks = {
    network(1, {
      cells = { {}, {}, {} }, all_logistic_robots = 120, available_logistic_robots = 30,
      all_construction_robots = 40, available_construction_robots = 39,
      contents = {
        { name = "iron-plate", count = 9000 }, { name = "copper-plate", count = 4000 },
        { name = "iron-plate", count = 1500, quality = "uncommon" },
        { name = "steel-plate", count = 2000, quality = "uncommon" },
        { name = "coal", count = 1500 }, { name = "stone", count = 1200 },
        { name = "iron-gear-wheel", count = 900 }, { name = "copper-cable", count = 800 },
        { name = "electronic-circuit", count = 700 }, { name = "pipe", count = 600 },
        { name = "belt", count = 500 }, { name = "rail", count = 400 },
        { name = "wood", count = 300 },
      },
    }),
    network(2, { cells = { {} }, all_logistic_robots = 5, contents = { { name = "wood", count = 10 } } }),
  }

  force = {
    name = "player", valid = true, players = { bob, gone }, connected_players = { bob },
    print = function(text) S.printed[#S.printed + 1] = { who = "force:player", text = text } end,
    current_research = techs.logistics, research_progress = 0.25,
    technologies = techs,
    research_queue = S.research_queue,
    rockets_launched = 7,
    items_launched = { satellite = 3, ["space-science-pack"] = 1000, ["cargo-pod"] = 7 },
    logistic_networks = { nauvis = S.networks },
    -- SurfaceIdentification: an index, a name, or the LuaSurface itself.
    get_item_production_statistics = function(id)
      local surface = (type(id) == "table") and id or S.surfaces[id]
      assert(surface and surface.valid, "unknown surface " .. tostring(id))
      S.last_stats_surface = surface.name
      return stats
    end,
  }
  -- LuaForce::get_evolution_factor and its three parts all take an optional
  -- SurfaceIdentification; the tools pass the LuaSurface itself, so assert that.
  local evolution = { get_evolution_factor = 0.42, get_evolution_factor_by_time = 0.2,
                      get_evolution_factor_by_pollution = 0.15,
                      get_evolution_factor_by_killing_spawners = 0.07 }
  for method, value in pairs(evolution) do
    force[method] = function(surface)
      assert(type(surface) == "table" and surface.valid, method .. " wants a surface")
      S.last_evolution_surface = surface.name
      if surface.name ~= "nauvis" then return 0 end
      return value
    end
  end
  bob.force = force
  gone.force = force
  S.force = force
  S.bob = bob
  S.gone = gone

  _G.storage = {}
  S.logs = {}
  _G.log = function(msg) S.log = msg; S.logs[#S.logs + 1] = msg end

  _G.defines = {
    events = setmetatable({}, { __index = function(t, k)
      local id = "event:" .. k; rawset(t, k, id); return id
    end }),
    flow_precision_index = {
      five_seconds = 1, one_minute = 2, ten_minutes = 3, one_hour = 4,
      ten_hours = 5, fifty_hours = 6, two_hundred_fifty_hours = 7,
      one_thousand_hours = 8,
    },
    gui_type = { custom = 1 },
  }

  _G.helpers = {
    table_to_json = encode,
    json_to_table = F.decode,
    write_file = function(path, data, append, for_player)
      assert(for_player == 0, "events must be written by the server only")
      if not append then S.files[path] = "" end
      S.files[path] = (S.files[path] or "") .. data
    end,
  }

  -- The global LuaPrototypes object. Only the entity table is needed: entity_count
  -- checks a name against it before asking the engine to count it.
  -- LuaPrototypes::quality, the LuaCustomTable[string -> LuaQualityPrototype]
  -- every flow read iterates so one item's production is summed over the
  -- qualities a game actually has.
  _G.prototypes = {
    entity = { lab = { name = "lab" }, ["assembling-machine-2"] = { name = "assembling-machine-2" } },
    item = { ["iron-ore"] = { name = "iron-ore" }, ["iron-plate"] = { name = "iron-plate" }, coal = { name = "coal" },
             ["repair-pack"] = { name = "repair-pack" }, ["iron-gear-wheel"] = { name = "iron-gear-wheel" },
             ["military-science-pack"] = { name = "military-science-pack" } },
    fluid = { ["crude-oil"] = { name = "crude-oil" } },
    technology = { ["logistics-2"] = { name = "logistics-2" } },
    recipe = {
      ["repair-pack"] = { name = "repair-pack", products = { { type = "item", name = "repair-pack", amount = 1 } } },
      ["iron-gear-wheel"] = { name = "iron-gear-wheel", products = { { type = "item", name = "iron-gear-wheel", amount = 1 } } },
      ["military-science-pack"] = { name = "military-science-pack", products = { { type = "item", name = "military-science-pack", amount = 2 } } },
    },
    quality = { normal = { name = "normal", level = 0 }, uncommon = { name = "uncommon", level = 1 } },
  }

  _G.rcon = { print = function(s) S.rcon_replies[#S.rcon_replies + 1] = s end }

  _G.commands = {
    add_command = function(name, help, fn)
      if S.commands[name] then error("command " .. name .. " already exists") end
      S.commands[name] = fn
    end,
  }

  _G.remote = {
    interfaces = S.interfaces,
    add_interface = function(name, fns) S.interfaces[name] = fns end,
    call = function(iface, fn, ...)
      local t = S.interfaces[iface]
      if not t then error("no interface " .. iface) end
      if not t[fn] then error("no function " .. iface .. "." .. fn) end
      return t[fn](...)
    end,
  }

  _G.script = {
    active_mods = { ["ai-agent-bridge"] = "0.1.0" },
    generate_event_name = function()
      S.next_event_id = S.next_event_id + 1; return S.next_event_id
    end,
    on_event = function(id, fn) S.handlers[id] = fn end,
    raise_event = function(id, data)
      local fn = S.handlers[id]
      if fn then fn(data) end
      S.raised = S.raised or {}
      S.raised[#S.raised + 1] = { id = id, data = data }
    end,
    on_init = function(fn) S.on_init = fn end,
    on_load = function(fn) S.on_load = fn end,
    on_configuration_changed = function(fn) S.on_config = fn end,
    on_nth_tick = function(_n, _fn) end,
  }

  _G.settings = { global = S.settings }

  -- A second force with nobody on it, for private-scope answers.
  S.team3 = {
    name = "team-3", valid = true, players = {}, connected_players = {},
    print = function(text) S.printed[#S.printed + 1] = { who = "force:team-3", text = text } end,
  }

  _G.game = {
    forces = { player = force, ["team-3"] = S.team3 },
    surfaces = S.surfaces,
    connected_players = { bob },
    players = { bob, gone },
    -- LuaGameScript::get_player takes an index or a name.
    get_player = function(i)
      if i == 1 or i == "Bob" then return bob end
      if i == 2 or i == "Ann" then return gone end
      return nil
    end,
    print = function(text) S.printed[#S.printed + 1] = { who = "*", text = text } end,
  }
  -- tick and ticks_played both live on the handle so a test can move the clock.
  -- LuaGameScript::ticks_played counts from map creation, so it is never behind
  -- tick in a freeplay save; the default keeps the two the same.
  S.ticks_played = opts.ticks_played or S.tick
  setmetatable(_G.game, { __index = function(_, k)
    if k == "tick" then return S.tick end
    if k == "ticks_played" then return S.ticks_played end
  end })

  return S
end

return F
