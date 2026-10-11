-- AI Agent Bridge - tests/lua/fakegame.lua
-- Author: bits-orio
-- License: MIT
--
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
      -- Off for the suite; the rate-limit section turns them on.
      ["aab-ask-cooldown-seconds"] = { value = 0 },
      ["aab-asks-per-minute"]      = { value = 0 },
      ["aab-personality"]          = { value = "" },
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
  }
  -- LuaFlowStatistics::get_input_count / get_output_count take a
  -- FlowStatisticsID, bare name or {name, quality}, and answer the lifetime
  -- count. Only nauvis has ever produced anything here, so the all-surfaces
  -- read can be seen to skip the platform.
  local function lifetime_count(counts)
    return function(id)
      local item, quality = id, "normal"
      if type(id) == "table" then item, quality = id.name, id.quality or "normal" end
      assert(type(item) == "string", "get_*_count wants an item prototype name")
      assert(S.quality_share[quality], "no quality prototype named " .. tostring(quality))
      if S.last_stats_surface ~= "nauvis" then return 0 end
      return (counts[item] or 0) * S.quality_share[quality]
    end
  end
  stats.get_input_count = lifetime_count(stats.input_counts)
  stats.get_output_count = lifetime_count(stats.output_counts)
  do
    local s_ = stats
    s_.get_flow_count = function(a)
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
    end
  end
  S.stats = stats

  local nauvis = { name = "nauvis", index = 1, valid = true, planet = { name = "nauvis", valid = true } }
  -- LuaSurface::map_gen_settings, MapGenSettings: only seed (uint32, "the
  -- random seed used to generate this map") is read, for the map id.
  nauvis.map_gen_settings = { seed = opts.seed or 2894571 }
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
  -- Per-surface overrides, so a sweep that sums a force's count across every
  -- surface can actually be observed doing it. Without these every surface but
  -- nauvis answers zero, and a sum of one real term plus zeroes passes whether
  -- the code sums or simply takes the last value it saw.
  S.entity_counts_by_surface = {}
  S.pollution = { nauvis = 1234.5, ["platform-1"] = 0 }
  for _, surface in pairs(S.surfaces) do
    local this = surface
    this.count_entities_filtered = function(filter)
      assert(type(filter) == "table", "count_entities_filtered wants one table")
      assert(type(filter.name) == "string", "count_entities_filtered wants a name string")
      assert(type(filter.force) == "string", "count_entities_filtered wants a force name")
      S.last_entity_filter = filter
      local per = S.entity_counts_by_surface[this.name]
      if per then return per[filter.name] or 0 end
      if this.name ~= "nauvis" then return 0 end
      return S.entity_counts[filter.name] or 0
    end
    this.get_total_pollution = function() return S.pollution[this.name] or 0 end
    -- LuaSurface::find_entities_filtered: name, type, force, limit, and
    -- position with radius are honoured; everything lives on nauvis.
    -- EntitySearchFilters.position + .radius: "will return all entities
    -- within the radius of the position" (2.0.77), and force is optional.
    --
    -- EntitySearchFilters.name, .type, .ghost_name and .ghost_type each take a
    -- single prototype id OR an array of them (2.0.77 docs, LuaSurface.html).
    -- This fake compared with == until 1.0.4 and so matched nothing whenever a
    -- caller passed the array form, which is what find_entities does for a
    -- recipe or product search: three find_entities checks went red against a
    -- scan the live server had already been measured doing correctly.
    local function matches(want, got)
      if want == nil then return false end
      if type(want) ~= "table" then return want == got end
      for _, one in ipairs(want) do
        if one == got then return true end
      end
      return false
    end
    this.find_entities_filtered = function(filter)
      assert(type(filter) == "table", "find_entities_filtered wants one table")
      if filter.force ~= nil then assert(type(filter.force) == "string", "find_entities_filtered wants a force name") end
      if filter.radius ~= nil then assert(type(filter.position) == "table", "a radius needs a position") end
      S.last_find_filter = filter
      local out = {}
      if this.name ~= "nauvis" then return out end
      for _, e in ipairs(S.entities) do
        local by_real = filter.name == nil and filter.type == nil
        local built = matches(filter.name, e.name) or matches(filter.type, e.type)
        local ghost = matches(filter.ghost_name, e.ghost_name) or matches(filter.ghost_type, e.ghost_type)
        if by_real and (filter.ghost_name or filter.ghost_type) then by_real = false end
        local inside = true
        if filter.radius then
          local dx, dy = e.position.x - filter.position.x, e.position.y - filter.position.y
          inside = dx * dx + dy * dy <= filter.radius * filter.radius
        end
        local owned = filter.force == nil or (e.force ~= nil and e.force.name == filter.force)
        if (by_real or built or ghost) and inside and owned then
          out[#out + 1] = e
          if filter.limit and #out >= filter.limit then break end
        end
      end
      return out
    end
  end
  -- Every fixture entity belongs to the player force: LuaEntity::force is
  -- never nil, and a build is told from scenery by it.
  local function entity(name, etype, x, y, recipe)
    return { name = name, type = etype, valid = true, position = { x = x, y = y },
             force = { name = "player", valid = true },
             get_recipe = function() return recipe and { name = recipe } or nil end }
  end
  local function ghost(name, etype, x, y, recipe)
    return { name = "entity-ghost", type = "entity-ghost", ghost_name = name, ghost_type = etype, valid = true,
             position = { x = x, y = y }, force = { name = "player", valid = true },
             get_recipe = function() return recipe and { name = recipe } or nil end }
  end
  S.entities = {
    ghost("assembling-machine-2", "assembling-machine", -15, -38, "iron-chest"),
    ghost("assembling-machine-2", "assembling-machine", -12, -38, "iron-chest"),
    ghost("stone-furnace", "furnace", 0, -38),
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
    -- LuaPlayer::chat_color, the colour the game prints this player's chat
    -- in; the renderer colours the name the same way.
    chat_color = { r = 1, g = 0.5, b = 0 },
    surface = nauvis, opened = nil, position = { x = 10.4, y = -3.6 },
    -- Remote view: the controller is on nauvis, the character on the platform.
    physical_surface = orbit, physical_position = { x = 2.2, y = 2.8 },
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
    -- LuaForce::get_entity_count(name) -> uint32. The engine keeps this per
    -- force, documented O(1), which is why a sweep over forces needs no pass
    -- budget. Derived here from the same per-surface fixture the surface
    -- counts use, so the two can never disagree and a test cannot pass against
    -- a number nothing else in the fake believes.
    get_entity_count = function(name)
      assert(type(name) == "string", "get_entity_count wants a prototype name")
      local total = 0
      for _, surface in pairs(S.surfaces) do
        total = total + surface.count_entities_filtered{ force = "player", name = name }
      end
      return total
    end,
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
    -- The nine LuaSpacePlatform states (2.0.77 docs). Values only have to be
    -- distinct, since platform_lookup.lua looks them up by identity, not by
    -- number.
    space_platform_state = {
      no_path = 0, no_schedule = 1, on_the_path = 2, paused = 3,
      starter_pack_on_the_way = 4, starter_pack_requested = 5,
      waiting_at_station = 6, waiting_for_departure = 7, waiting_for_starter_pack = 8,
    },
  }

  -- platform-1 carries a real LuaSpacePlatform, stopped at fulgora, owned by
  -- the one force the suite runs as. scheduled_for_deletion is 0 here on
  -- purpose, never nil: the real engine always returns a tick count, "0 if
  -- not scheduled for deletion" (2.0.77), and a fake that left it nil would
  -- hide the bug this exists to catch. In Lua, unlike most languages, 0 is
  -- truthy, so `if not platform.scheduled_for_deletion` reads a healthy
  -- platform's 0 the same as a pending one's tick count and skips both;
  -- platform_lookup.lua's `(x or 0) == 0` is the test that tells them apart,
  -- and this fixture is what a wrong test would get right here for the wrong
  -- reason and wrong on a save with a platform genuinely pending deletion.
  orbit.platform = {
    name = "platform-1", index = 1, valid = true,
    force = force, surface = orbit,
    space_location = { name = "fulgora", valid = true },
    state = defines.space_platform_state.waiting_at_station,
    scheduled_for_deletion = 0,
  }
  -- LuaForce::platforms, dictionary[uint32 -> LuaSpacePlatform], "will
  -- include platforms that are pending deletion" (2.0.77).
  force.platforms = { [1] = orbit.platform }

  _G.helpers = {
    table_to_json = encode,
    json_to_table = F.decode,
    -- LuaHelpers::is_valid_sprite_path(path): "Checks if the given SpritePath
    -- is valid and contains a loaded sprite" (2.0.77). A SpritePath is a
    -- SpritePrototype's own name or "type/name"; the class must be one this
    -- fake knows and the name a prototype it has. planet/<x> is not a sprite
    -- path, measured on the live engine; space-location/<x> is. quality_info
    -- is a sprite prototype the base game's own locale draws bare.
    is_valid_sprite_path = function(path)
      assert(type(path) == "string", "is_valid_sprite_path wants a string")
      if S.sprite_prototypes[path] then return true end
      local class, name = path:match("^([%w%-]+)/(.+)$")
      if not class then return false end
      local P = _G.prototypes
      local tables = { item = P.item, entity = P.entity, fluid = P.fluid, technology = P.technology,
                       recipe = P.recipe, quality = P.quality, tile = P.tile, ["virtual-signal"] = P.virtual_signal,
                       ["space-location"] = { nauvis = true }, utility = { warning_icon = true } }
      local t = tables[class]
      return t ~= nil and t[name] ~= nil
    end,
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
      ["iron-chest"] = { name = "iron-chest", products = { { type = "item", name = "iron-chest", amount = 1 } } },
      ["repair-pack"] = { name = "repair-pack", products = { { type = "item", name = "repair-pack", amount = 1 } } },
      ["iron-gear-wheel"] = { name = "iron-gear-wheel", products = { { type = "item", name = "iron-gear-wheel", amount = 1 } } },
      ["military-science-pack"] = { name = "military-science-pack", products = { { type = "item", name = "military-science-pack", amount = 2 } } },
    },
    quality = { normal = { name = "normal", level = 0 }, uncommon = { name = "uncommon", level = 1 } },
    -- LuaPrototypes::tile and ::virtual_signal, for the clickable [tile=..]
    -- and [virtual-signal=..] tags and their sprite paths. F.install_recipes
    -- swaps tile for strict tiles that carry the fluid a pump draws.
    tile = { water = { name = "water" }, ["grass-1"] = { name = "grass-1" } },
    virtual_signal = { ["signal-A"] = { name = "signal-A" } },
    -- LuaPrototypes::font: the mod's own data.lua declares aab-mono, and the
    -- table renderer checks it is there before setting rows in it.
    font = { ["aab-mono"] = { name = "aab-mono" } },
  }

  S.sprite_prototypes = { quality_info = true }

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

  -- LuaSettings::global. A mod changes one of its own settings by writing a
  -- whole ModSetting table, { value = ... }, of the setting's own type; the
  -- engine refuses anything else, so this does too. S.settings is the store
  -- tests read and poke directly.
  S.setting_writes = {}
  _G.settings = { global = setmetatable({}, {
    __index = S.settings,
    __newindex = function(_, name, setting)
      local was = S.settings[name]
      assert(was ~= nil, "no mod setting named " .. tostring(name))
      assert(type(setting) == "table" and setting.value ~= nil, "a setting is written as { value = ... }")
      assert(type(setting.value) == type(was.value), "setting " .. name .. " holds a " .. type(was.value))
      S.settings[name] = { value = setting.value }
      S.setting_writes[#S.setting_writes + 1] = { name = name, value = setting.value }
    end,
  }) }

  -- A second force with nobody on it, for private-scope answers.
  S.team3 = {
    name = "team-3", valid = true, players = {}, connected_players = {},
    print = function(text) S.printed[#S.printed + 1] = { who = "force:team-3", text = text } end,
    -- Every LuaForce carries these two; leaving them off made a force sweep
    -- unable to see a second force at all, so no test could observe the order
    -- rows arrive in.
    rockets_launched = 0,
    items_launched = {},
    get_entity_count = function() return 0 end,
    platforms = {},
    -- The per-force reads every LuaForce carries, with team-3's own numbers,
    -- so a sweep that walks two forces can be seen ORDERING them. Without
    -- these, five of the six delegating metrics crashed on a nil method the
    -- moment team-3 had a player, so every ordering claim had only ever been
    -- checked against a single row, which any order satisfies.
    logistic_networks = {},
    get_item_production_statistics = function(id)
      local surface = (type(id) == "table") and id or S.surfaces[id]
      assert(surface and surface.valid, "unknown surface " .. tostring(id))
      S.last_stats_surface = surface.name
      return S.team3_stats
    end,
  }
  -- team-3's item statistics: a fixed multiple of player's, on nauvis only,
  -- through the same lifetime_count and get_flow_count shapes as player's so
  -- the two fakes cannot drift apart.
  S.team3_scale = 0
  S.team3_stats = {
    input_counts = setmetatable({}, { __index = function(_, k) return (stats.input_counts[k] or 0) * S.team3_scale end }),
    output_counts = setmetatable({}, { __index = function(_, k) return (stats.output_counts[k] or 0) * S.team3_scale end }),
  }
  S.team3_stats.get_input_count = function(id) return stats.get_input_count(id) * S.team3_scale end
  S.team3_stats.get_output_count = function(id) return stats.get_output_count(id) * S.team3_scale end
  S.team3_stats.get_flow_count = function(a) return stats.get_flow_count(a) * S.team3_scale end
  S.team3_evolution = 0
  for _, method in ipairs({ "get_evolution_factor", "get_evolution_factor_by_time",
                           "get_evolution_factor_by_pollution", "get_evolution_factor_by_killing_spawners" }) do
    S.team3[method] = function(surface)
      assert(type(surface) == "table" and surface.valid, method .. " wants a surface")
      return S.team3_evolution
    end
  end

  local function engine_force(name)
    return {
      name = name, valid = true, players = {}, connected_players = {},
      rockets_launched = 0, items_launched = {}, platforms = {},
      get_entity_count = function() return 0 end,
      logistic_networks = {},
    }
  end
  S.enemy, S.neutral = engine_force("enemy"), engine_force("neutral")

  _G.game = {
    -- Every game carries the engine's own enemy and neutral forces. They
    -- have no players and never will, and a sweep or a force list that
    -- forgets that counts them as empty team slots.
    forces = { player = force, ["team-3"] = S.team3, enemy = S.enemy, neutral = S.neutral },
    surfaces = S.surfaces,
    connected_players = { bob },
    players = { bob, gone },
    -- LuaGameScript::get_player takes an index or a name.
    get_player = function(i)
      if i == 1 or i == "Bob" then return bob end
      if i == 2 or i == "Ann" then return gone end
      return nil
    end,
    -- LuaGameScript::get_surface takes an index or a name, nil when no
    -- surface matches.
    get_surface = function(id) return S.surfaces[id] end,
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

  -- ── kills, built and fluid production: Stage 5 Unit A ────────────────
  -- LuaForce::get_kill_count_statistics(surface) and
  -- ::get_entity_build_count_statistics(surface) both require a surface
  -- (verified: /home/shobhitg/factorio/doc-html/runtime-api.json, 2.0.77;
  -- unlike get_evolution_factor, `surface` is not optional on either), so a
  -- fake that let it default to nil would hide a bug the real engine would
  -- refuse outright. team-3 carries its own, larger counts, a real number
  -- rather than the zero the item-stats fixture above leaves every other
  -- force at, so an all=true sweep across kills, built, fluid_rate and
  -- trains all have an actual leader to sort ahead of player, the same
  -- reason S.team3.rockets_launched got set to 99 further up this file.
  local function stat_counts(input_counts, output_counts)
    return { input_counts = input_counts, output_counts = output_counts }
  end
  S.kill_counts = {
    player = {
      nauvis = stat_counts({ ["small-biter"] = 40, ["medium-biter"] = 10 }, { character = 3 }),
      ["platform-1"] = stat_counts({ ["small-biter"] = 5 }, {}),
    },
    ["team-3"] = { nauvis = stat_counts({ ["small-biter"] = 99 }, {}) },
  }
  S.build_counts = {
    player = {
      nauvis = stat_counts({ ["assembling-machine-2"] = 6, lab = 2 }, { ["stone-furnace"] = 1 }),
      ["platform-1"] = stat_counts({ ["solar-panel"] = 3 }, {}),
    },
    ["team-3"] = { nauvis = stat_counts({ lab = 200 }, {}) },
  }
  local function attach_count_stats(target, force_name)
    target.get_kill_count_statistics = function(surface)
      assert(type(surface) == "table" and surface.valid, "get_kill_count_statistics wants a surface")
      local by_surface = S.kill_counts[force_name] or {}
      return by_surface[surface.name] or stat_counts({}, {})
    end
    target.get_entity_build_count_statistics = function(surface)
      assert(type(surface) == "table" and surface.valid, "get_entity_build_count_statistics wants a surface")
      local by_surface = S.build_counts[force_name] or {}
      return by_surface[surface.name] or stat_counts({}, {})
    end
  end
  attach_count_stats(force, "player")
  attach_count_stats(S.team3, "team-3")

  -- LuaForce::get_fluid_production_statistics(surface) reads a
  -- LuaFlowStatistics the same shape the item one does, but FluidID
  -- (concepts.FlowStatisticsID: "Used with fluid production statistics")
  -- carries no quality form at all, unlike ItemWithQualityID, so this fake's
  -- own get_flow_count asserts a bare string name rather than accepting the
  -- {name=,quality=} table the item-side fake above takes: a caller that
  -- copied flow.item_flow's quality loop onto a fluid is exactly the bug
  -- this assertion exists to catch.
  -- Keyed by force, surface, FLUID and precision, and every read goes
  -- through all four. The first version keyed on force and surface alone and
  -- answered the same number for any fluid name and any window, which let
  -- sweep{metric:fluid_rate, subject:"nonexistent-fluid-xyz"} come back as a
  -- confident 100 per minute and let a hardcoded precision pass every test.
  -- A read for a fluid or a window the fixture does not hold is 0, which is
  -- what the engine answers for a fluid a force has never touched.
  local P = defines.flow_precision_index
  S.fluid_flow = {
    player = {
      nauvis = { ["crude-oil"] = { [P.one_minute] = { input = 90, output = 30 }, [P.one_hour] = { input = 60, output = 20 } } },
      ["platform-1"] = { ["crude-oil"] = { [P.one_minute] = { input = 10, output = 5 } } },
    },
    ["team-3"] = {
      nauvis = { ["crude-oil"] = { [P.one_minute] = { input = 500, output = 100 } } },
    },
  }
  S.fluid_flow_calls = 0
  local function attach_fluid_stats(target, force_name)
    -- One LuaFlowStatistics object per surface asked for, bound to that
    -- surface at fetch time, the way the engine's is: a handle fetched for
    -- nauvis keeps reading nauvis however many other surfaces are fetched
    -- afterwards.
    target.get_fluid_production_statistics = function(id)
      local surface = (type(id) == "table") and id or S.surfaces[id]
      assert(surface and surface.valid, "unknown surface " .. tostring(id))
      local surface_name = surface.name
      return {
        get_flow_count = function(a)
          assert(type(a) == "table", "get_flow_count wants one table")
          assert(a.name and a.category and a.precision_index, "get_flow_count is missing a field")
          assert(a.category == "input" or a.category == "output" or a.category == "storage", a.category)
          assert(type(a.name) == "string",
            "get_flow_count for a fluid wants a bare prototype name: fluids have no quality")
          assert(a.sample_index == nil, "fluid_rate never samples")
          S.fluid_flow_calls = S.fluid_flow_calls + 1
          local by_fluid = (S.fluid_flow[force_name] or {})[surface_name] or {}
          local by_precision = by_fluid[a.name] or {}
          local at = by_precision[a.precision_index] or { input = 0, output = 0 }
          return a.category == "input" and at.input or at.output
        end,
      }
    end
  end
  attach_fluid_stats(force, "player")
  attach_fluid_stats(S.team3, "team-3")

  -- LuaTrainManager::get_trains(TrainFilter) -> array[LuaTrain]; `filter` is
  -- required (2.0.77 docs: `optional: false`), unlike most search filters in
  -- this fake, so a caller that forgot to pass one at all is exactly the bug
  -- this assertion exists to catch. Returned trains carry only the fields
  -- LuaTrain actually has that scripts/tools/trains.lua reads: id, valid,
  -- speed, manual_mode; force and surface live in this fixture's own
  -- bookkeeping table, never on the object itself, since real LuaTrain
  -- carries neither directly (checked against its full attribute list).
  local function fake_train(id, surface_name, force_name, speed, manual)
    return {
      entry = { id = id, valid = true, speed = speed, manual_mode = manual },
      surface_name = surface_name, force_name = force_name,
    }
  end
  S.trains = {
    fake_train(1, "nauvis", "player", 3.2, false),
    fake_train(2, "nauvis", "player", 0, true),
    fake_train(3, "nauvis", "player", 0, false),
    fake_train(4, "platform-1", "player", 0, false),
    fake_train(5, "nauvis", "team-3", 12.5, false),
    fake_train(6, "nauvis", "team-3", 8, false),
    fake_train(7, "nauvis", "team-3", 0, true),
    fake_train(8, "platform-1", "team-3", 3, false),
    fake_train(9, "platform-1", "team-3", 0, false),
  }
  _G.game.train_manager = {
    get_trains = function(filter)
      assert(type(filter) == "table", "get_trains wants one table")
      local out = {}
      for _, t in ipairs(S.trains) do
        local ok = true
        if filter.force ~= nil and t.force_name ~= filter.force then ok = false end
        if filter.surface ~= nil then
          local want = filter.surface
          if type(want) == "table" then want = want.name end
          if t.surface_name ~= want then ok = false end
        end
        if filter.is_moving ~= nil and (t.entry.speed ~= 0) ~= filter.is_moving then ok = false end
        if filter.is_manual ~= nil and t.entry.manual_mode ~= filter.is_manual then ok = false end
        if ok then out[#out + 1] = t.entry end
      end
      return out
    end,
  }

  return S
end

-- ══ recipe_needs: recipes, crafting machines, belts (opt-in) ═══════════
-- One self-contained block for scripts/tools/recipe_needs.lua and its
-- helpers. Opt-in: a suite calls F.install_recipes(S) after F.install(), and
-- no other suite sees any of it. That matters, because find_entities sorts
-- its name suggestions, and a science pack added to every suite's items
-- would move "military-science-pack" off the top of a list aab_test.lua reads.
--
-- Ground truth, not memory: the vanilla recipes below are transcribed from
-- headless 2.0.77 data/base/prototypes/recipe.lua, the machines from
-- entity/entities.lua and the belts from entity/transport-belts.lua. Where a
-- data field is left out the prototype default applies: category "crafting",
-- energy_required 0.5, maximum_productivity 3.
--
-- Engine behaviour the fake holds the tool to:
--   * Every prototype and LuaRecipe is a strict object. Reading a key it does
--     not carry raises, as a LuaObject does, and so does iterating one or
--     encoding one. category, additional_categories and is_parameter raise
--     with a 2.1 message: 2.1.17 removes all three, so code that reads them
--     dies on the next game version. LuaRecipePrototype::enabled is left off
--     on purpose: it is the prototype default, never what a force has.
--   * get_recipe_filtered and get_entity_filtered return a LuaCustomTable:
--     pairs works, next() sees nothing, as on the real engine.
--   * Both implement exactly the filters the tool sends and assert on any
--     other, so a filter the engine reads differently cannot pass silently.
--   * get_crafting_speed and has_category assert their argument, so a colon
--     call, which passes the object as the first argument, fails here as it
--     would misbehave there.
-- No centrifuge is defined: the uranium recipes have no machine, which is the
-- "no crafting machine can make it" path.
do
  local REMOVED_IN_2_1 = { category = true, additional_categories = true, is_parameter = true }

  --- A read-only object that raises on any key it does not carry, the way a
  --- LuaObject does. `optional` names keys that may read as nil.
  local function strict(class, fields, optional, writable)
    optional = optional or {}
    return setmetatable({}, {
      __index = function(_, key)
        if REMOVED_IN_2_1[key] then
          error(class .. "::" .. key .. " does not exist in Factorio 2.1 (removed after 2.0.77)", 2)
        end
        local value = fields[key]
        if value == nil and not optional[key] then
          error(class .. " doesn't contain key " .. tostring(key), 2)
        end
        return value
      end,
      __newindex = function(_, key, value)
        if not (writable and writable[key]) then error(class .. "::" .. tostring(key) .. " is read only", 2) end
        fields[key] = value
      end,
      __pairs = function() error("cannot iterate a " .. class .. ": it is a LuaObject, not a table", 2) end,
    })
  end

  --- LuaCustomTable: indexable and walkable with pairs, but next() on it sees
  --- nothing, because the entries are not stored in the table itself.
  local function custom_table(entries)
    return setmetatable({}, {
      __index = function(_, key) return entries[key] end,
      __pairs = function() return next, entries, nil end,
      __len = function()
        local n = 0
        for _ in pairs(entries) do n = n + 1 end
        return n
      end,
      __newindex = function() error("LuaCustomTable is read only", 2) end,
    })
  end

  local function product(kind, name, amount, extra)
    local p = { type = kind, name = name, amount = amount, probability = 1 }
    for key, value in pairs(extra or {}) do p[key] = value end
    return p
  end
  local function item(name, amount, extra) return product("item", name, amount, extra) end
  local function fluid(name, amount, extra) return product("fluid", name, amount, extra) end
  -- An Ingredient carries no probability; strip the one product() adds.
  local function input(p) p.probability = nil return p end

  local VANILLA = {
    { name = "iron-plate", category = "smelting", energy = 3.2,
      ingredients = { input(item("iron-ore", 1)) }, products = { item("iron-plate", 1) } },
    { name = "copper-plate", category = "smelting", energy = 3.2,
      ingredients = { input(item("copper-ore", 1)) }, products = { item("copper-plate", 1) } },
    { name = "steel-plate", category = "smelting", energy = 16,
      ingredients = { input(item("iron-plate", 5)) }, products = { item("steel-plate", 1) } },
    { name = "stone-brick", category = "smelting", energy = 3.2,
      ingredients = { input(item("stone", 2)) }, products = { item("stone-brick", 1) } },
    { name = "iron-gear-wheel",
      ingredients = { input(item("iron-plate", 2)) }, products = { item("iron-gear-wheel", 1) } },
    { name = "copper-cable",
      ingredients = { input(item("copper-plate", 1)) }, products = { item("copper-cable", 2) } },
    { name = "electronic-circuit",
      ingredients = { input(item("iron-plate", 1)), input(item("copper-cable", 3)) },
      products = { item("electronic-circuit", 1) } },
    { name = "advanced-circuit", energy = 6,
      ingredients = { input(item("electronic-circuit", 2)), input(item("plastic-bar", 2)), input(item("copper-cable", 4)) },
      products = { item("advanced-circuit", 1) } },
    { name = "pipe", ingredients = { input(item("iron-plate", 1)) }, products = { item("pipe", 1) } },
    { name = "engine-unit", category = "advanced-crafting", energy = 10,
      ingredients = { input(item("steel-plate", 1)), input(item("iron-gear-wheel", 1)), input(item("pipe", 2)) },
      products = { item("engine-unit", 1) } },
    { name = "inserter",
      ingredients = { input(item("electronic-circuit", 1)), input(item("iron-gear-wheel", 1)), input(item("iron-plate", 1)) },
      products = { item("inserter", 1) } },
    { name = "transport-belt",
      ingredients = { input(item("iron-plate", 1)), input(item("iron-gear-wheel", 1)) },
      products = { item("transport-belt", 2) } },
    { name = "automation-science-pack", energy = 5,
      ingredients = { input(item("copper-plate", 1)), input(item("iron-gear-wheel", 1)) },
      products = { item("automation-science-pack", 1) } },
    { name = "logistic-science-pack", energy = 6,
      ingredients = { input(item("inserter", 1)), input(item("transport-belt", 1)) },
      products = { item("logistic-science-pack", 1) } },
    { name = "chemical-science-pack", energy = 24,
      ingredients = { input(item("engine-unit", 2)), input(item("advanced-circuit", 3)), input(item("sulfur", 1)) },
      products = { item("chemical-science-pack", 2) } },
    { name = "plastic-bar", category = "chemistry", energy = 1,
      ingredients = { input(fluid("petroleum-gas", 20)), input(item("coal", 1)) }, products = { item("plastic-bar", 2) } },
    { name = "sulfur", category = "chemistry", energy = 1,
      ingredients = { input(fluid("water", 30)), input(fluid("petroleum-gas", 30)) }, products = { item("sulfur", 2) } },
    -- main_product = "" in the data: no main product at runtime.
    { name = "basic-oil-processing", category = "oil-processing", energy = 5, main_product = "",
      ingredients = { input(fluid("crude-oil", 100)) }, products = { fluid("petroleum-gas", 45) } },
    -- The multi-product recipe: picked for petroleum gas by net output, so it
    -- is what puts "byproducts" in approx.
    { name = "advanced-oil-processing", category = "oil-processing", energy = 5,
      ingredients = { input(fluid("water", 50)), input(fluid("crude-oil", 100)) },
      products = { fluid("heavy-oil", 25), fluid("light-oil", 45), fluid("petroleum-gas", 55) } },
    { name = "light-oil-cracking", category = "chemistry", energy = 2, main_product = "",
      ingredients = { input(fluid("water", 30)), input(fluid("light-oil", 30)) }, products = { fluid("petroleum-gas", 20) } },
    { name = "heavy-oil-cracking", category = "chemistry", energy = 2, main_product = "",
      ingredients = { input(fluid("water", 30)), input(fluid("heavy-oil", 40)) }, products = { fluid("light-oil", 30) } },
    -- A chance on every product, and the catalyst: kovarex nets 1 uranium-235
    -- out of 41 made, the other 40 being the ones it took in.
    { name = "uranium-processing", category = "centrifuging", energy = 12,
      ingredients = { input(item("uranium-ore", 10)) },
      products = { item("uranium-235", 1, { probability = 0.007 }), item("uranium-238", 1, { probability = 0.993 }) } },
    { name = "kovarex-enrichment-process", category = "centrifuging", energy = 60, main_product = "",
      ingredients = { input(item("uranium-235", 40, { ignored_by_stats = 40 })),
                      input(item("uranium-238", 5, { ignored_by_stats = 2 })) },
      products = { item("uranium-235", 41, { ignored_by_stats = 40, ignored_by_productivity = 40 }),
                   item("uranium-238", 2, { ignored_by_stats = 2, ignored_by_productivity = 2 }) } },
    -- What places the machines and belts below.
    { name = "assembling-machine-1",
      ingredients = { input(item("electronic-circuit", 3)), input(item("iron-gear-wheel", 5)), input(item("iron-plate", 9)) },
      products = { item("assembling-machine-1", 1) } },
    { name = "assembling-machine-2",
      ingredients = { input(item("steel-plate", 2)), input(item("electronic-circuit", 3)),
                      input(item("iron-gear-wheel", 5)), input(item("assembling-machine-1", 1)) },
      products = { item("assembling-machine-2", 1) } },
    { name = "assembling-machine-3",
      ingredients = { input(item("speed-module", 4)), input(item("assembling-machine-2", 2)) },
      products = { item("assembling-machine-3", 1) } },
    { name = "stone-furnace", ingredients = { input(item("stone", 5)) }, products = { item("stone-furnace", 1) } },
    { name = "steel-furnace", energy = 3,
      ingredients = { input(item("steel-plate", 6)), input(item("stone-brick", 10)) }, products = { item("steel-furnace", 1) } },
    { name = "electric-furnace", energy = 5,
      ingredients = { input(item("steel-plate", 10)), input(item("advanced-circuit", 5)), input(item("stone-brick", 10)) },
      products = { item("electric-furnace", 1) } },
    { name = "chemical-plant", energy = 5,
      ingredients = { input(item("steel-plate", 5)), input(item("iron-gear-wheel", 5)),
                      input(item("electronic-circuit", 5)), input(item("pipe", 5)) },
      products = { item("chemical-plant", 1) } },
    { name = "oil-refinery", energy = 8,
      ingredients = { input(item("steel-plate", 15)), input(item("iron-gear-wheel", 10)), input(item("stone-brick", 10)),
                      input(item("electronic-circuit", 10)), input(item("pipe", 10)) },
      products = { item("oil-refinery", 1) } },
    { name = "fast-transport-belt",
      ingredients = { input(item("iron-gear-wheel", 5)), input(item("transport-belt", 1)) },
      products = { item("fast-transport-belt", 1) } },
    { name = "express-transport-belt", category = "crafting-with-fluid",
      ingredients = { input(item("iron-gear-wheel", 10)), input(item("fast-transport-belt", 1)), input(fluid("lubricant", 20)) },
      products = { item("express-transport-belt", 1) } },
    -- Not vanilla. A research-progress product exists in the wild (it crashed
    -- RateCalculator, e5453bf), is not in the runtime docs' Product union, and
    -- has no name.
    { name = "test-research-progress",
      ingredients = { input(item("iron-plate", 1)) },
      products = { item("test-progress-item", 1), { type = "research-progress", research_item = "automation-science-pack", amount = 1 } } },
    { name = "barrel", energy = 1, ingredients = { input(item("steel-plate", 1)) }, products = { item("barrel", 1) } },
  }
  -- Barrels, as data/base/data-updates.lua makes them for every fluid with a
  -- barrel: a fill recipe named <fluid>-barrel and an empty-<fluid>-barrel
  -- recipe, 50 units each way, 0.2 s, crafting-with-fluid, neither hidden nor
  -- a parameter, both unlocked by fluid handling, no main_product. Emptying
  -- nets 50 of the fluid, so without its round-trip rule the tool would make
  -- water out of barrels filled from water.
  for _, fluid_name in ipairs({ "water", "crude-oil", "petroleum-gas" }) do
    local full = fluid_name .. "-barrel"
    VANILLA[#VANILLA + 1] = { name = full, category = "crafting-with-fluid", energy = 0.2,
      ingredients = { input(fluid(fluid_name, 50, { ignored_by_stats = 50 })), input(item("barrel", 1, { ignored_by_stats = 1 })) },
      products = { item(full, 1, { ignored_by_stats = 1 }) } }
    VANILLA[#VANILLA + 1] = { name = "empty-" .. full, category = "crafting-with-fluid", energy = 0.2,
      ingredients = { input(item(full, 1, { ignored_by_stats = 1 })) },
      products = { fluid(fluid_name, 50, { ignored_by_stats = 50 }), item("barrel", 1, { ignored_by_stats = 1 }) } }
  end

  -- Recipes a fresh freeplay force starts with, among the ones above: every
  -- recipe in data/base/prototypes/recipe.lua without enabled = false. A
  -- fresh force has no assembler and no science pack recipe; it does have the
  -- stone furnace and the yellow belt.
  local FREEPLAY_START = { "iron-plate", "copper-plate", "stone-brick", "iron-gear-wheel", "transport-belt", "stone-furnace" }

  -- Crafting machines and belts: name, type, speed, categories.
  local MACHINES = {
    { "assembling-machine-1", "assembling-machine", 0.5, { "crafting", "basic-crafting", "advanced-crafting" } },
    { "assembling-machine-2", "assembling-machine", 0.75, { "basic-crafting", "crafting", "advanced-crafting", "crafting-with-fluid" } },
    { "assembling-machine-3", "assembling-machine", 1.25, { "basic-crafting", "crafting", "advanced-crafting", "crafting-with-fluid" } },
    { "stone-furnace", "furnace", 1, { "smelting" } },
    { "steel-furnace", "furnace", 2, { "smelting" } },
    { "electric-furnace", "furnace", 2, { "smelting" } },
    { "chemical-plant", "assembling-machine", 1, { "chemistry" } },
    { "oil-refinery", "assembling-machine", 1, { "oil-processing" } },
    { "rocket-silo", "rocket-silo", 1, { "rocket-building" } },
    -- Not vanilla, which has no hidden crafting machine; mods do. Faster than
    -- anything real, so a tool that offered either would name it.
    { "test-hidden-assembler", "assembling-machine", 10, { "crafting" }, { hidden = true } },
    { "test-parameter-assembler", "assembling-machine", 9, { "crafting" }, { parameter = true } },
  }
  local BELTS = {
    { "transport-belt", 0.03125 }, { "fast-transport-belt", 0.0625 }, { "express-transport-belt", 0.09375 },
  }

  local function main_product_of(spec)
    if spec.main_product == "" then return nil end
    for _, p in ipairs(spec.products) do
      if spec.main_product ~= nil and p.name == spec.main_product then return p end
    end
    if spec.main_product == nil and #spec.products == 1 then return spec.products[1] end
    return nil
  end

  function F.install_recipes(S)
    local P = _G.prototypes
    local specs, protos, entities = {}, {}, {}
    local refresh
    S.recipe_state = { player = {}, ["team-3"] = {}, enemy = {}, neutral = {} }
    S.recipe_filter_calls, S.entity_filter_calls = 0, 0

    local function register(p)
      if p.type == "item" and not P.item[p.name] then P.item[p.name] = { name = p.name } end
      if p.type == "fluid" and not P.fluid[p.name] then P.fluid[p.name] = { name = p.name } end
    end

    --- Adds one recipe prototype, registering every item and fluid it names.
    --- spec: name, category?, energy?, ingredients, products, main_product?,
    --- hidden?, parameter?, maximum_productivity?, allowed_effects?,
    --- enabled? (force name -> boolean; player defaults to true, every
    --- other force to false).
    function S.add_recipe(spec)
      for _, p in ipairs(spec.products) do if p.name then register(p) end end
      for _, p in ipairs(spec.ingredients) do register(p) end
      local category = spec.category or "crafting"
      specs[spec.name] = spec
      protos[spec.name] = strict("LuaRecipePrototype", {
        name = spec.name, type = "recipe", valid = true, object_name = "LuaRecipePrototype",
        products = spec.products, ingredients = spec.ingredients,
        energy = spec.energy or 0.5, main_product = main_product_of(spec),
        hidden = spec.hidden == true, parameter = spec.parameter == true,
        maximum_productivity = spec.maximum_productivity or 3,
        -- LuaRecipePrototype::allowed_effects, dictionary[string -> boolean],
        -- optional: only a suite that asks for one gets it.
        allowed_effects = spec.allowed_effects,
        has_category = function(wanted)
          assert(type(wanted) == "string", "has_category wants a category name (called with a colon?)")
          return wanted == category
        end,
      }, { main_product = true, allowed_effects = true })
      P.recipe[spec.name] = protos[spec.name]
      for force_name, state in pairs(S.recipe_state) do
        local on = (spec.enabled or {})[force_name]
        if on == nil then on = force_name == "player" end
        state[spec.name] = { enabled = on, productivity_bonus = 0 }
      end
      if refresh then refresh() end
    end

    for _, spec in ipairs(VANILLA) do S.add_recipe(spec) end
    for _, name in ipairs(FREEPLAY_START) do S.recipe_state["team-3"][name].enabled = true end
    for _, name in ipairs({ "iron-ore", "copper-ore", "stone", "coal", "uranium-ore", "speed-module" }) do
      register({ type = "item", name = name })
    end

    local function place(name, extra)
      local fields = { name = name, valid = true, hidden = false, parameter = false,
                       items_to_place_this = { { name = name, count = 1 } } }
      for key, value in pairs(extra) do fields[key] = value end
      entities[name] = strict("LuaEntityPrototype", fields)
      P.entity[name] = entities[name]
      P.item[name] = { name = name, place_result = entities[name] }
    end
    --- One crafting machine, {name, type, speed, categories, flags?}, flags
    --- hidden, parameter and productivity. Every crafting machine has an
    --- EffectReceiver (measured: all of the live save's), a plain table whose
    --- base_effect carries productivity only on a machine that has some: the
    --- electromagnetic plant, foundry and biochamber's 0.5.
    function S.add_crafter(m)
      local categories = {}
      for _, c in ipairs(m[4]) do categories[c] = true end
      local speed, flags = m[3], m[5] or {}
      place(m[1], { type = m[2], crafting_categories = categories,
                    hidden = flags.hidden == true, parameter = flags.parameter == true,
                    effect_receiver = { base_effect = { productivity = flags.productivity },
                                        uses_module_effects = true, uses_beacon_effects = true, uses_surface_effects = true },
                    get_crafting_speed = function(quality)
        assert(quality == nil or quality == "normal", "get_crafting_speed takes a quality, not the prototype (called with a colon?)")
        return speed
      end })
    end
    for _, m in ipairs(MACHINES) do S.add_crafter(m) end
    for _, b in ipairs(BELTS) do place(b[1], { type = "transport-belt", belt_speed = b[2] }) end

    --- A resource entity, as data/base/prototypes/entity/resources.lua
    --- declares one: a LuaEntityPrototype of type "resource" whose
    --- MineableProperties, a plain table, name what a drill or pumpjack gets.
    --- extra overrides MineableProperties fields, minable for one.
    function S.add_resource(name, products, extra)
      for _, p in ipairs(products) do if p.name then register(p) end end
      local mining = { minable = true, mining_time = 1, products = products }
      for key, value in pairs(extra or {}) do mining[key] = value end
      entities[name] = strict("LuaEntityPrototype", {
        name = name, type = "resource", valid = true, hidden = false, parameter = false,
        mineable_properties = mining,
      })
      P.entity[name] = entities[name]
    end
    for _, ore in ipairs({ "iron-ore", "copper-ore", "coal", "stone", "uranium-ore" }) do
      S.add_resource(ore, { item(ore, 1) })
    end
    -- data: crude-oil's minable result is the fluid, amount_min = amount_max = 10.
    S.add_resource("crude-oil", { fluid("crude-oil", nil, { amount_min = 10, amount_max = 10 }) })

    -- LuaPrototypes::tile as the engine has it: a LuaCustomTable of strict
    -- LuaTilePrototypes, `fluid` the one an offshore pump draws there or nil.
    -- The tiles F.install made stay, now as strict ones with no fluid.
    local tiles = {}
    function S.add_tile(name, fluid_name)
      if fluid_name then register({ type = "fluid", name = fluid_name }) end
      tiles[name] = strict("LuaTilePrototype", { name = name, valid = true, fluid = fluid_name and P.fluid[fluid_name] or nil },
        { fluid = true })
    end
    for name in pairs(P.tile) do S.add_tile(name, nil) end
    S.add_tile("water", "water")
    S.add_tile("deepwater", "water")
    P.tile = custom_table(tiles)

    -- What a surface's map generator places, as recipe_needs reads it:
    -- MapGenSettings::autoplace_settings, a plain table, entity and tile
    -- each { treat_missing_as_default, settings = { [prototype name] =
    -- AutoplaceControl } }. Nauvis lists what the live 2.0.77 save's does
    -- (data/base/prototypes/planet/planet-map-gen.lua), shortened to names
    -- the fake has: resources, a fish, a rock, a land tile and the waters.
    -- Its LuaPlanet gains the prototype, with the same lists, and platform-1
    -- is a platform already. Every planet on the live save, and on a fresh
    -- 2.1.17 map, sets treat_missing_as_default and places nothing it leaves
    -- out, so the fake sets it too.
    local function autoplace(lists)
      local out = {}
      for kind, names in pairs(lists) do
        local settings = {}
        for _, name in ipairs(names) do settings[name] = { frequency = 1, size = 1, richness = 1 } end
        out[kind] = { treat_missing_as_default = true, settings = settings }
      end
      return out
    end
    local NAUVIS_PLACES = { entity = { "iron-ore", "copper-ore", "stone", "coal", "crude-oil", "uranium-ore", "fish", "big-rock" },
                            tile = { "grass-1", "water", "deepwater" } }
    local nauvis = S.surfaces.nauvis
    nauvis.map_gen_settings.autoplace_settings = autoplace(NAUVIS_PLACES)
    nauvis.planet.prototype = { name = "nauvis", map_gen_settings = { autoplace_settings = autoplace(NAUVIS_PLACES) } }

    --- A surface to ask recipe_needs about. spec: name; platform (true for a
    --- space platform); places, what its own map generator lists ({entity,
    --- tile}, nothing when left out); planet, what its planet prototype
    --- lists (no planet when left out).
    function S.add_surface(spec)
      local surface = { name = spec.name, index = #S.surfaces + 1, valid = true,
                        map_gen_settings = { autoplace_settings = autoplace(spec.places or {}) } }
      if spec.platform then surface.platform = { name = spec.name, valid = true } end
      if spec.planet then
        surface.planet = { name = spec.name, valid = true,
                           prototype = { name = spec.name, map_gen_settings = { autoplace_settings = autoplace(spec.planet) } } }
      end
      rawset(S.surfaces, surface.index, surface)
      getmetatable(S.surfaces).__index[spec.name] = surface
      return surface
    end

    --- One force's LuaCustomTable of LuaRecipe: enabled and
    --- productivity_bonus are writable, as on the engine.
    local function force_recipes(force_name)
      local entries = {}
      for name in pairs(specs) do
        local state = S.recipe_state[force_name][name]
        entries[name] = strict("LuaRecipe", setmetatable({ name = name, valid = true, prototype = protos[name] },
          { __index = state, __newindex = state }), nil, { enabled = true, productivity_bonus = true })
      end
      return custom_table(entries)
    end
    -- Rebuilt whenever a recipe is added, so a recipe a suite adds later is in
    -- every force's table the way a loaded prototype always is.
    refresh = function()
      S.force.recipes = force_recipes("player")
      S.team3.recipes = force_recipes("team-3")
      S.enemy.recipes = force_recipes("enemy")
      S.neutral.recipes = force_recipes("neutral")
    end
    refresh()

    local function names_of(filter_value)
      if type(filter_value) == "string" then return { [filter_value] = true } end
      local set = {}
      for _, one in ipairs(filter_value) do set[one] = true end
      return set
    end

    -- RecipePrototypeFilter, a list of them read the way the engine reads it
    -- (runtime docs, 2.0.77 and 2.1.17): each filter tests one recipe,
    -- `invert` flips that test, and `mode` joins it to the filter before,
    -- "or" when left out, "and" binding tighter than "or". Measured on a
    -- live 2.0.77 Space Age game of 712 recipes: X or Y and Z gave X plus
    -- (Y and Z), never (X or Y) and Z; the first filter's mode joins nothing;
    -- an empty list matched all 712; and (not hidden and makes these items)
    -- or (not hidden and makes these fluids) equalled the single filters'
    -- union less hidden recipes over 712 samples. Only the filters the tool
    -- sends are implemented; any other is refused.
    local PRODUCT_KIND = { ["has-product-item"] = "item", ["has-product-fluid"] = "fluid" }
    local MODES = { ["or"] = true, ["and"] = true }

    --- The names one has-product filter asks for. Every one must be a real
    --- prototype: stricter than the engine, which quietly matches nothing for
    --- a name it does not have (2.0.77), because a tool that sends one has a
    --- bug and here it shows.
    local function product_names(f, kind)
      local elem = f.elem_filters
      assert(type(elem) == "table" and #elem == 1 and elem[1].filter == "name",
        "the fake implements elem_filters = {{filter = \"name\", name = ...}} only")
      local wanted = names_of(elem[1].name)
      for name in pairs(wanted) do
        assert(P[kind][name], "no " .. kind .. " prototype named " .. name)
      end
      return wanted
    end

    --- The bare test one filter makes of a recipe spec, before invert.
    local function recipe_condition(f)
      if f.filter == "hidden" then return function(spec) return spec.hidden == true end end
      local kind = PRODUCT_KIND[f.filter]
      assert(kind, "the fake does not implement recipe filter " .. tostring(f.filter))
      local wanted = product_names(f, kind)
      return function(spec)
        for _, p in ipairs(spec.products) do
          if p.type == kind and wanted[p.name] then return true end
        end
        return false
      end
    end

    local function recipe_test(f)
      assert(type(f) == "table", "each recipe filter is a table")
      assert(f.mode == nil or MODES[f.mode], "a filter's mode is \"or\" or \"and\", not " .. tostring(f.mode))
      assert(f.invert == nil or type(f.invert) == "boolean", "a filter's invert is a boolean")
      local condition, invert = recipe_condition(f), f.invert == true
      return function(spec) return condition(spec) ~= invert end
    end

    --- The filters as groups joined by "or", the tests in each joined by
    --- "and". A filter opens a new group unless its mode is "and".
    local function recipe_groups(filters)
      local groups = {}
      for i, f in ipairs(filters) do
        if i == 1 or f.mode ~= "and" then groups[#groups + 1] = {} end
        local group = groups[#groups]
        group[#group + 1] = recipe_test(f)
      end
      return groups
    end

    local function passes_all(group, spec)
      for _, test in ipairs(group) do
        if not test(spec) then return false end
      end
      return true
    end

    local function passes(groups, spec)
      if #groups == 0 then return true end
      for _, group in ipairs(groups) do
        if passes_all(group, spec) then return true end
      end
      return false
    end

    P.get_recipe_filtered = function(filters)
      assert(type(filters) == "table", "get_recipe_filtered wants an array of filters")
      local groups = recipe_groups(filters)
      S.recipe_filter_calls = S.recipe_filter_calls + 1
      local out = {}
      for name, spec in pairs(specs) do
        if passes(groups, spec) then out[name] = protos[name] end
      end
      return custom_table(out)
    end

    P.get_entity_filtered = function(filters)
      assert(type(filters) == "table" and #filters == 1, "the fake implements one entity filter at a time")
      local f = filters[1]
      assert(f.filter == "type", "the fake does not implement entity filter " .. tostring(f.filter))
      assert(f.invert == nil and f.mode == nil, "the fake does not implement invert or mode")
      local types = names_of(f.type)
      S.entity_filter_calls = S.entity_filter_calls + 1
      local out = {}
      for name, entity in pairs(entities) do
        if types[entity.type] then out[name] = entity end
      end
      return custom_table(out)
    end

    --- Sets one force's state for one recipe, e.g. {enabled = true} or
    --- {productivity_bonus = 0.5}, the way the engine's own writes would.
    function S.set_recipe(force_name, recipe_name, fields)
      for key, value in pairs(fields) do S.recipe_state[force_name][recipe_name][key] = value end
    end

    return S
  end
end

return F
