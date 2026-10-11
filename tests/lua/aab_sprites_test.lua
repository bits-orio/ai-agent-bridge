-- AI Agent Bridge - tests/lua/aab_sprites_test.lua
-- Author: bits-orio
-- License: MIT
--
-- Step 4 of the sprite repair chain (companion-mod/scripts/sprites.lua): a
-- name players use for a vanilla item shows that item's icon instead of its
-- plain words. The live model wrote [img=item.red-transport-belt] for the
-- red belt, which the game calls fast-transport-belt; the chain used to end
-- at "red transport belt" with no icon. The nicknames live in
-- scripts/close_names.lua and are vanilla only. The earlier steps and the
-- words fallback are covered by the repair cases in aab_test.lua.
--
-- Mutation-tested against a scratch copy of the mod, one break at a time, and
-- every one turns a check below red:
--   * no nickname step in find(): the live belt case and every icon case
--   * the target taken without asking the game: the missing-target checks
--   * the nickname tried before the real names: the mod-item checks
--   * a looser match (any tail with "belt" in it, a default for a colour the
--     table lacks): the partial-match and unknown-colour checks
--   * the name used as written, not respelled: the capitals, underscores and
--     plural checks
--   * the normalised name itself never tried: the live case and every icon case
--   * the step left out of clickable tags: the [item=..] checks
--   * the classes ignored for the target: the [img=entity..] checks
--   * a non-vanilla entry in the table, or a target changed: the table checks
--   * recipe_names no longer asking close_names.nickname: the did_you_mean
--     check
--   * the renderer no longer calling sprites.repair: the two answer checks
--   * the es or the ies line struck from close_names.spellings: the plural
--     checks, which hold the rule the README states for every plural

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

local sprites     = require("scripts.sprites")
local close_names = require("scripts.close_names")

-- ── a game that has every vanilla item the nicknames can name ─────────
-- The base game and Space Age, 2.0.77 and 2.1.17: each name below is an item
-- prototype there, and the belts are entities too. This list is also the
-- allowlist the table is held to further down.
local BELTS    = { "transport-belt", "fast-transport-belt", "express-transport-belt", "turbo-transport-belt" }
local PACKS    = { "automation-science-pack", "logistic-science-pack", "military-science-pack",
                   "chemical-science-pack", "production-science-pack", "utility-science-pack",
                   "space-science-pack", "metallurgic-science-pack", "electromagnetic-science-pack",
                   "agricultural-science-pack", "cryogenic-science-pack" }
local CIRCUITS = { "electronic-circuit", "advanced-circuit", "processing-unit" }

local VANILLA = {}
for _, list in ipairs({ BELTS, PACKS, CIRCUITS }) do
  for _, name in ipairs(list) do VANILLA[name] = true end
end

local function add(name, ...)
  for _, class in ipairs({ ... }) do prototypes[class][name] = { name = name } end
end
local function stock()
  for name in pairs(VANILLA) do add(name, "item", "recipe") end
  for _, name in ipairs(BELTS) do add(name, "entity") end
end
local function unstock(name)
  for _, class in ipairs({ "item", "recipe", "entity" }) do prototypes[class][name] = nil end
end
stock()

local function repaired(case, input, want)
  local got = sprites.repair(input)
  check("sprite repair, " .. case, got == want, got)
end

-- ── the cases that started this ───────────────────────────────────────
repaired("the live case: a red belt is the fast belt", "A [img=item.red-transport-belt] carries 30/s",
         "A [img=item.fast-transport-belt] carries 30/s")
repaired("a blue science pack is the chemical one", "[img=item.blue-science-pack]/min 45",
         "[img=item.chemical-science-pack]/min 45")
repaired("a name nobody uses still falls back to its words", "[img=item.purple-widget] here", "purple widget here")
repaired("the biter case still falls back to its words", "[img=entity.biter] Most biters", "biter Most biters")

-- ── every nickname the table knows, spelled the long way and the short ─
-- One entry per table entry: twenty things players say, and what they mean.
local SAID = {
  ["red-science-pack"] = "automation-science-pack",       ["green-science-pack"] = "logistic-science-pack",
  ["grey-science-pack"] = "military-science-pack",        ["gray-science-pack"] = "military-science-pack",
  ["black-science-pack"] = "military-science-pack",       ["blue-science-pack"] = "chemical-science-pack",
  ["purple-science-pack"] = "production-science-pack",    ["yellow-science-pack"] = "utility-science-pack",
  ["white-science-pack"] = "space-science-pack",          ["vulcanus-science-pack"] = "metallurgic-science-pack",
  ["fulgora-science-pack"] = "electromagnetic-science-pack", ["gleba-science-pack"] = "agricultural-science-pack",
  ["aquilo-science-pack"] = "cryogenic-science-pack",
  ["green-circuit"] = "electronic-circuit", ["red-circuit"] = "advanced-circuit", ["blue-circuit"] = "processing-unit",
  ["yellow-transport-belt"] = "transport-belt",    ["red-transport-belt"] = "fast-transport-belt",
  ["blue-transport-belt"] = "express-transport-belt", ["green-transport-belt"] = "turbo-transport-belt",
}
local function short(said) return (said:gsub("%-science%-pack$", "-science"):gsub("%-transport%-belt$", "-belt")) end

local said_count = 0
for said, meant in pairs(SAID) do
  said_count = said_count + 1
  repaired(said .. " is " .. meant, "[img=item." .. said .. "]", "[img=item." .. meant .. "]")
  if short(said) ~= said then
    repaired(short(said) .. " is " .. meant, "[img=item." .. short(said) .. "]", "[img=item." .. meant .. "]")
  end
end
check("the list above has an entry for every nickname the table has", said_count == 20, said_count)

-- ── the class the model wrote, and the other ways to write the tag ────
repaired("an entity tag finds the belt entity", "[img=entity.red-transport-belt]", "[img=entity.fast-transport-belt]")
repaired("an entity tag for a pack finds the item", "[img=entity.blue-science-pack]", "[img=item.chemical-science-pack]")
repaired("a fluid tag for a belt finds the item", "[img=fluid.red-belt]", "[img=item.fast-transport-belt]")
repaired("a bare name finds its class", "[img=red-transport-belt]", "[img=item.fast-transport-belt]")
repaired("a slash path", "[img=item/red-transport-belt]", "[img=item.fast-transport-belt]")
repaired("capitals and spaces", "[img=item.Red Transport Belt]", "[img=item.fast-transport-belt]")
repaired("underscores", "[img=item.red_transport_belt]", "[img=item.fast-transport-belt]")
repaired("a plural", "[img=item.red-transport-belts]", "[img=item.fast-transport-belt]")
repaired("capitals, spaces and a plural", "[img=item.Blue Science Packs]", "[img=item.chemical-science-pack]")
repaired("two nicknames in one line", "[img=item.red-belt] or [img=item.blue-belt]",
         "[img=item.fast-transport-belt] or [img=item.express-transport-belt]")
repaired("a nickname beside a tag that falls back", "[img=entity.biter] hits a [img=item.red-belt]",
         "biter hits a [img=item.fast-transport-belt]")
repaired("the same colour names a different item in each family",
         "[img=item.green-circuit] [img=item.green-science] [img=item.green-belt]",
         "[img=item.electronic-circuit] [img=item.logistic-science-pack] [img=item.turbo-transport-belt]")

-- ── clickable tags go down the same chain ─────────────────────────────
repaired("a clickable item", "[item=red-transport-belt]", "[item=fast-transport-belt]")
repaired("a clickable entity", "[entity=red-belt]", "[entity=fast-transport-belt]")
repaired("a clickable recipe", "[recipe=blue-science]", "[recipe=chemical-science-pack]")
repaired("a clickable nickname keeps its quality", "[item=blue-science-pack,quality=uncommon]",
         "[item=chemical-science-pack,quality=uncommon]")
repaired("and loses one the game lacks", "[item=blue-science-pack,quality=mythic]", "[item=chemical-science-pack]")
repaired("a clickable name nobody uses ends at its plain name", "[item=purple-widget] here", "purple-widget here")

-- ── never a guess on a partial match ──────────────────────────────────
-- The whole name must be a colour and a family. Anything else is words.
for _, case in ipairs({
  { "a colour alone", "red", "red" },
  { "a family alone", "belt", "belt" },
  { "a family and a pack alone", "science-pack", "science pack" },
  { "a colour and extra after the family", "red-belt-mk2", "red belt mk2" },
  { "a number after the family", "red-science-pack-2", "red science pack 2" },
  { "a word in front of the colour", "very-red-belt", "very red belt" },
  { "a word between colour and family", "red-fast-belt", "red fast belt" },
  { "a longer word that starts like a colour", "reddish-belt", "reddish belt" },
  { "two colours", "red-green-belt", "red green belt" },
  { "a colour the table lacks", "purple-belt", "purple belt" },
  { "a colour the table lacks, circuit", "orange-circuit", "orange circuit" },
  { "a family the table lacks", "red-wire", "red wire" },
  { "a doubled hyphen", "red--belt", "red belt" },
}) do
  repaired("no guess: " .. case[1], "[img=item." .. case[2] .. "]", case[3])
end
repaired("no guess for a clickable either", "[item=red]", "red")
repaired("no guess for a clickable belt of an unknown colour", "[item=purple-belt]", "purple-belt")

-- ── the real names come first, and the game has the last word ─────────
-- A mod that ships an item called red-science-pack gets that item, never the
-- vanilla one a player would have meant.
add("red-science-pack", "item")
repaired("a real name beats a nickname", "[img=item.red-science-pack]", "[img=item.red-science-pack]")
repaired("a respelled real name beats a nickname", "[img=item.Red Science Pack]", "[img=item.red-science-pack]")
repaired("a real clickable name beats a nickname", "[item=red-science-pack]", "[item=red-science-pack]")
repaired("the nickname still serves the others", "[img=item.green-science-pack]", "[img=item.logistic-science-pack]")
unstock("red-science-pack")

-- The target is asked of the game like any sprite: a game without the turbo
-- belt, or one that renamed the fast belt, gets words and never a raw tag.
unstock("turbo-transport-belt")
repaired("a nickname for an item the game lacks falls back to words", "[img=item.green-belt]", "green belt")
repaired("and so does a clickable one", "[item=green-belt]", "green-belt")
repaired("the other belts are unaffected", "[img=item.blue-belt]", "[img=item.express-transport-belt]")
stock()
repaired("the belt is back", "[img=item.green-belt]", "[img=item.turbo-transport-belt]")

unstock("fast-transport-belt")
repaired("a game that renamed the fast belt reads the live case as words", "A [img=item.red-transport-belt] carries 30/s",
         "A red transport belt carries 30/s")
stock()

-- A prototype of that name but no sprite for it: only is_valid_sprite_path decides an [img].
local drawn = helpers.is_valid_sprite_path
helpers.is_valid_sprite_path = function(path)
  if path:find("fast-transport-belt", 1, true) then return false end
  return drawn(path)
end
repaired("a target with no sprite falls back to words", "[img=item.red-belt]", "red belt")
repaired("though a clickable tag only needs the prototype", "[item=red-belt]", "[item=fast-transport-belt]")
helpers.is_valid_sprite_path = drawn

-- ── a repaired line stays repaired ────────────────────────────────────
for _, text in ipairs({
  "A [img=item.red-transport-belt] carries 30/s", "[img=item.blue-science-pack]/min 45",
  "[item=red-belt]", "[img=item.purple-widget] here", "[img=item.Red Transport Belts]",
}) do
  local once = sprites.repair(text)
  check("repairing twice changes nothing: " .. text, sprites.repair(once) == once, sprites.repair(once))
end

-- ── plurals: s, es and ies ────────────────────────────────────────────
-- close_names.spellings drops a plural three ways, and the repair chain's
-- respelling step, find_entities and recipe_needs all read that one list.
-- The plain s has checks of its own in aab_test.lua; these hold the other
-- two. The examples are real items: Space Age's yumako-mash, the base
-- game's battery.
local function spelled(name, want)
  for _, spelling in ipairs(close_names.spellings(name)) do
    if spelling == want then return true end
  end
  return false
end
check("an es plural drops its es: yumako-mashes is also yumako-mash", spelled("yumako-mashes", "yumako-mash"))
check("an ies plural becomes y: batteries is also battery", spelled("batteries", "battery"))
check("respelled first, then the plural dropped: Yumako Mashes is yumako-mash", spelled("Yumako Mashes", "yumako-mash"))
add("battery", "item", "recipe")
add("yumako-mash", "item", "recipe")
repaired("an ies plural finds its item", "[img=item.batteries]", "[img=item.battery]")
repaired("an es plural finds its item", "[img=item.yumako-mashes]", "[img=item.yumako-mash]")
repaired("a clickable ies plural", "[item=batteries]", "[item=battery]")
repaired("a clickable es plural", "[recipe=yumako-mashes]", "[recipe=yumako-mash]")
unstock("battery")
unstock("yumako-mash")
repaired("with no such item the plural falls back to its words", "[img=item.batteries]", "batteries")

-- ── the renderer prints the icon ──────────────────────────────────────
local function rpc(req)
  S.rcon_replies = {}
  req.v = req.v or 1
  S.commands["aab-rpc"]({ parameter = F.encode(req), player_index = nil, tick = S.tick })
  return F.decode(S.rcon_replies[#S.rcon_replies])
end
local qid = remote.call("ai-agent-bridge-v1", "ask", { text = "belts", force = "player", player_index = 1 })
local answered = rpc({ op = "answer", qid = qid, artifact = { shape = "summary", lines = {
  "A [img=item.red-transport-belt] carries 30/s",
  "[img=item.blue-science-pack]/min 45, [img=item.purple-widget] 7",
} } })
local shown = rpc({ op = "answers", after = qid - 1 }).r[1].lines
check("an answer carrying the live case prints the fast belt's icon",
      answered.ok and shown[1] == "A [img=item.fast-transport-belt] carries 30/s", F.encode(shown))
check("an answer prints a pack's icon, and an unknown name as words",
      shown[2] == "[img=item.chemical-science-pack]/min 45, purple widget 7", F.encode(shown))

-- ── the table: vanilla only, one target each, one copy of the truth ───
-- Vanilla (the base game and Space Age) so the companion names no other mod
-- and guesses nothing about prototypes it has never seen.
local entries, vanilla_only = 0, true
for family, colours in pairs(close_names.NICKNAMES) do
  for colour, target in pairs(colours) do
    entries = entries + 1
    if not VANILLA[target] then
      vanilla_only = false
      print("  not vanilla: " .. family .. "." .. colour .. " = " .. tostring(target))
    end
  end
end
check("every target in the table is a vanilla item", vanilla_only)
check("the table has exactly the twenty entries listed above", entries == 20, entries)

local all_resolve = true
for said, meant in pairs(SAID) do
  if close_names.nickname(said) ~= meant then
    all_resolve = false
    print("  " .. said .. " resolved to " .. tostring(close_names.nickname(said)) .. ", not " .. meant)
  end
end
check("nickname() gives each entry's one target", all_resolve)

local shadows = {}
for name in pairs(VANILLA) do
  if close_names.nickname(name) ~= nil then shadows[#shadows + 1] = name end
end
check("no real vanilla name reads as a nickname", #shadows == 0, table.concat(shadows, ","))
check("nickname() answers nil for what it does not know",
      close_names.nickname("red") == nil and close_names.nickname("purple-belt") == nil
      and close_names.nickname("fast-underground-belt") == nil and close_names.nickname("") == nil)

-- recipe_needs' did_you_mean (scripts/tools/recipe_names.lua) reads the same
-- close_names.nickname, so the two say the same about every nickname.
local recipe_names = require("scripts.tools.recipe_names")
local agree = true
for said, meant in pairs(SAID) do
  local guess = recipe_names.did_you_mean(said)[1]
  if guess ~= meant then
    agree = false
    print("  recipe_names.did_you_mean(" .. said .. ")[1] is " .. tostring(guess) .. ", not " .. meant)
  end
end
check("recipe_needs' did_you_mean says the same about every nickname", agree)

-- ── the table drives the result ───────────────────────────────────────
-- The same break made in-process: strike the entry and the live case is words
-- again, restore it and the icon is back.
local belts = close_names.NICKNAMES.belt
local red = belts.red
belts.red = nil
repaired("with the red belt struck from the table, the live case is words again",
         "A [img=item.red-transport-belt] carries 30/s", "A red transport belt carries 30/s")
repaired("a colour still in the table is unaffected", "[img=item.blue-belt]", "[img=item.express-transport-belt]")
belts.red = red
repaired("and restored, it is the icon again", "A [img=item.red-transport-belt] carries 30/s",
         "A [img=item.fast-transport-belt] carries 30/s")
belts.red = "no-such-belt"
repaired("a table entry the game has no item for never reaches the line", "[img=item.red-belt]", "red belt")
belts.red = red

local real_nickname = close_names.nickname
rawset(close_names, "nickname", function() return nil end)
repaired("with no nicknames at all the live case is words", "A [img=item.red-transport-belt] carries 30/s",
         "A red transport belt carries 30/s")
rawset(close_names, "nickname", real_nickname)
repaired("and with the lookup back, it is the icon", "A [img=item.red-transport-belt] carries 30/s",
         "A [img=item.fast-transport-belt] carries 30/s")

print(("\n%d passed, %d failed"):format(passes, fails))
if fails > 0 then os.exit(1) end
