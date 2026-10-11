# recipe_needs and MTS records: two questions the model had to guess at

Status: built and verified on 2026-10-11, uncommitted and unreleased. Part 1 is
the companion's `recipe_needs`, Part 2 is MTS's records tools, Part 3 and
Part 4 are the team chat privacy fix and the answer changes that rode in the
same work. Where the build differs from the plan, the "As built" section of
each part says so, and the code wins over the plan text above it. No version
bump anywhere until the owner asks.

Two unrelated tool sets, planned together because they failed the same way: a
player asked something no tool could answer, and the model either reasoned it
out from memory or hunted through tools that could never hold the answer.

- **Part 1, `recipe_needs`**: a companion engine tool that walks the game's own
  recipes. Everything from here to "Part 2".
- **Part 2, records, milestones and restarts**: tools MTS adds to its own
  `agent_tools_v1` manifest. The bridge does not change.
- **Part 3, team chat privacy**: found while testing Part 2. Private team chat
  could reach a stranger's answer. Fixed in the companion, the service and MTS.
- **Part 4, answers**: bigger answer caps with a refuse-and-rewrite loop, an
  icon repair chain, and a server-wide personality.

# Part 1: recipe_needs

## The question this exists to answer

Question 17 on the AleForge server, 2026-10-10, from Pirion (force team-6):

> I have 48 blue science, 24 red, 24 green. How much iron/copper belts do I need?

From the ledger: 1 round, 0 lookups, **2,420 reasoning tokens**, 113 s on
StreamLake. The first attempt ran into the 60 s client timeout and was thrown
away, the retry took about 52 s, and every question behind it waited. Every
other question that day used 20 to 520 reasoning tokens.

The model had no tool for this. It recalled every recipe from memory, expanded
the tree, multiplied, summed and divided, all in reasoning tokens. That is slow,
and on a modded server it is also wrong, because the model remembers vanilla
ratios.

The fix is a tool that does the walk in the game, from the game's own recipes.
The model's job shrinks to turning the question into item names and amounts.

## Goal and success measure

Ask Pirion's question again on the rig after this ships. Pass when:

- the model calls `recipe_needs` once, with all three targets in that one call
- reasoning tokens for the question stay under 300 and the round under 15 s
- the iron and copper figures match the oracle in "Verification" below

## The tool

One engine tool on `ai-agent-bridge-tools`, in the companion. `force` is
injected as on every tool. Pure read: no `storage` writes, no cache kept
between calls, no `on_tick`. Prototypes are identical on every peer, so the
reply is deterministic.

### Parameters

The probe grammar already has `list<string>` and `list<number>`
(`service/internal/catalog/params.go`), so targets go in as parallel lists. No
service change and no probe change.

| param | type | meaning |
|---|---|---|
| `items` | `list<string>!` | target item or fluid prototype names, e.g. `chemical-science-pack` |
| `per_min` | `list<number>` | wanted output per minute, one per item |
| `machines` | `list<number>` | machines making each target, one per item |
| `machine` | `string` | one machine to use for every recipe it can make; default is the fastest the force can build |
| `belt` | `string` | belt prototype to count belts in; default is the fastest the force can build |
| `stop_at` | `list<string>` | items to treat as inputs and not expand, e.g. `iron-plate` |

Exactly one of `per_min` or `machines`, same length as `items`. Anything else
is an argument error with a message the model can act on.

The `desc` carries the routing rule, so it travels with the tool and needs no
prompt line on an older companion:

> What it takes to make something: machines, every input per minute down to raw
> resources, and belts of each. Every target in one call; the totals are summed
> for you. Never work a ratio out yourself. A count of something the player
> "has" or "runs" is `machines`; a rate is `per_min`. Say which you assumed.

### Reply

```
{
  found = true, force = "team-6",
  mode = "machines",
  belt = { name = "transport-belt", per_sec = 15 },
  productivity = "research",             -- research bonus only; no modules, beacons, machine base effects
  approx = { "byproducts" },             -- why the figures are rough; empty when they are exact
  targets = { { item, per_min, machines, machine } ... },
  rows = { { item, per_min, recipe, machine, machines, belts, fluid? } ... },
  raw = { { item, per_min, belts, fluid? } ... },
  alternatives = { [item] = { "other-recipe", ... } },   -- at most 3 each
  total = 23, shown = 23,
}
```

- `rows` holds every intermediate, including items in `stop_at`, so
  "belts of iron plate" is read straight off the `iron-plate` row whether or not
  the walk went down to ore.
- `raw` is everything with no recipe the force can use: ore, coal, water, crude
  oil. These rows are never cut.
- `belts` is absent on fluid rows.
- Every double goes out through `bounded.round`. Rows go through `bounded.fit`
  with `total` and `shown` beside them.
- Unknown item names: `found = false, unknown = { ... }`, like the other tools'
  unknown-surface reply.

## How it works

### Which recipe makes an item

Candidates come from `prototypes.get_recipe_filtered` with `has-product-item`
(or `has-product-fluid`) and an `elem_filters` of `{ filter = "name", name = item }`.
Keep a candidate only if:

- `force.recipes[name].enabled` is true
- the prototype is neither `hidden` nor `parameter`, both on `LuaPrototypeBase`
  in 2.0.77 and 2.1. Not `is_parameter`: 2.1 removes it.
- its net output of the item is above zero: the product amount minus the same
  item as an ingredient. This handles catalysts like kovarex generically.

Both filter calls return a `LuaCustomTable`. Iterate it with `pairs`; `next()`
does not work on one.

Rank what is left, first rule wins:

1. the recipe whose name is the item's name
2. a recipe whose `main_product` is the item
3. the recipe with the largest net output of the item, then by name

The others go in `alternatives`. Picking a multi-product recipe (oil processing)
adds `byproducts` to `approx`: v1 charges the whole recipe to the product it was
picked for and does not balance byproducts.

An item with no candidate is raw. Measure the picks on a Space Age save before
trusting the rules (see Verification): casting, recycling and oil are the cases
most likely to surprise.

### The walk

Depth-first from each target, summing into one table keyed by item. Per recipe:

- only products of type `item` or `fluid` count. Skip any other type; a
  `research-progress` product exists in the wild and is not in the runtime
  docs' `Product` union
- expected output per craft = `amount` (or the mean of `amount_min` and
  `amount_max`) times `probability`, plus `extra_count_fraction`
- the part productivity skips is `min(expected, ignored_by_productivity)`,
  clamped so it can never exceed the expected output
- output per craft = skipped part + the rest × (1 + bonus). The bonus is
  `force.recipes[name].productivity_bonus`, capped at the prototype's
  `maximum_productivity`. When module or machine bonuses arrive later they add
  to it; bonuses never multiply
- crafts per minute = needed per minute / output per craft
- machines = crafts per minute × `energy` / (crafting speed × 60)
- each ingredient per minute = crafts per minute × its `amount`

In `machines` mode a target's rate comes first: crafts per minute = machines ×
speed × 60 / `energy`, then the walk runs as above.

An item met again while it is still on the current path is a cycle: treat it as
raw at that point and add `cycle` to `approx`. Hard caps: 15 deep and 80
distinct items. Past either, stop expanding and add `cap` to `approx`.

### Which machine

Read the crafting machines once per call:
`prototypes.get_entity_filtered` with type filters for `assembling-machine`,
`furnace` and `rocket-silo`. A machine can make a recipe when some key of its
`crafting_categories` passes `recipe.has_category(key)`.

Match this way round on purpose. 2.1 removes `LuaRecipePrototype.category` and
`additional_categories` in favour of `categories`; `has_category` and
`crafting_categories` exist in both 2.0.77 and 2.1, so the tool survives the
move.

A machine is one the force can build when some item in its
`items_to_place_this` has an enabled force recipe. Take the fastest by
`get_crafting_speed()` (normal quality), then by name. If the force can build
none, take the fastest anyway and mark the row `unlocked = false`.

`machine`, when given, wins for every recipe whose category it supports.

### Belts

Belts per row = per minute / 60 / (`belt_speed` × 480). RateCalculator uses the
same factor (scripts/gui-util.lua line 139). The default belt is the fastest
`type = "transport-belt"` prototype the force can build. Assume one item per
lane slot: no stacking.

## Files

Small files, one concern each, under `companion-mod/scripts/tools/`:

| file | owns |
|---|---|
| `recipe_needs.lua` | manifest, argument checks, the reply |
| `recipe_pick.lua` | candidates and ranking for one item |
| `recipe_walk.lua` | the walk, totals, cycles, caps |
| `machine_pick.lua` | which machine and which belt, and whether the force can build them |

Add `recipe_needs` to the module list in `engine.lua`. Nothing else in that
file changes.

## Stages

Each stage is one commit that passes `make lua-test` on its own.

1. **Walk, per minute only.** `recipe_pick`, `recipe_walk`, and `recipe_needs`
   with `items`, `per_min` and `stop_at`. Rows and raw, no machines, no belts.
   Fake game gains real recipes for red and green science and their tree.
2. **Machines.** `machine_pick`, the `machines` column, `machines` mode, the
   `machine` param, research productivity.
3. **Belts.** The `belt` param and the `belts` column.
4. **Service and e2e.** Refresh `tests/manifest.json`. Add Pirion's question as
   an e2e scenario. Add a prompt line only if the scenario shows the model
   still routing "how much do I need" to `production_since` (prompt.go line 69
   says "how much ... is a total"; watch for that).
5. **Rig.** Live engine checks and profiling, below.

Docs ride with the stage that makes them true: a row in the
`companion-mod/README.md` tool table, and a `changelog.txt` entry under the next
version.

## Verification

Unit tests in `tests/lua`, with the fake game extended. Oracle, vanilla recipes,
no productivity research, per pack:

| pack | iron plate | copper plate | other |
|---|---|---|---|
| red (`automation-science-pack`) | 2 | 1 | |
| green (`logistic-science-pack`) | 5.5 | 1.5 | |
| blue (`chemical-science-pack`) | 12 | 7.5 | 3 plastic, 0.5 sulfur |

Cases, each mutation-tested (break the guard, watch it go red):

- one target, and three targets summed into shared rows
- `machines` mode gives the same tree as the equivalent `per_min`
- `stop_at` stops expansion but keeps the row
- a disabled recipe is never picked; an item with only a disabled recipe is raw
- a catalyst recipe nets its output; a two-item cycle stops and adds `cycle`
- `productivity_bonus` is applied and capped at `maximum_productivity`
- `ignored_by_productivity` above the expected amount is clamped
- a product of a type other than item or fluid is skipped, not a crash
- a machine matches through `has_category`, with no `category` field in the fake
- unknown item gives `found = false`; mismatched list lengths give an argument error
- a wide tree is cut by `bounded.fit` with `total` above `shown`

On the rig, live 2.0.77 with Space Age:

- the oracle table for red, green and blue on a force with no productivity research
- dump the recipe pick for every science pack and every item in their trees, and
  read it by eye: casting, recycling and oil are where the rules may need a fix
- profile one call for all seven science packs at once with
  `helpers.create_profiler`; budget 5 ms. If it is over, build the product index
  once per call instead of one `get_recipe_filtered` per item
- then Pirion's question through the real model, against the success measure above

On a 2.1 rig, one extra oracle for free: 2.1 adds
`LuaRecipePrototype.get_product_amount(product_index, productivity)`, the
engine's own average amount. Compare it with the tool's output per craft for
every recipe in the game, at a few productivity values. Any mismatch is a bug in
the formula above.

Static checks as always: `luac5.4 -p` on every new file, `make lua-check`,
`go vet ./... && go test ./... && go build ./...` in `service/`.

## As built

The tool shipped as planned in its shape: one engine tool, `recipe_needs`, a
pure read with no storage and no `on_tick`. These are the places the build
differs from the plan above, and the reasons.

### Files

The four planned files became eleven, all under `companion-mod/scripts/tools/`.

| file | owns |
|---|---|
| `recipe_needs.lua` | manifest, argument checks, the flow |
| `recipe_names.lua` | unknown names and `did_you_mean` |
| `recipe_rank.lua` | the candidates for an item, ranked, from one combined engine filter |
| `recipe_pick.lua` | round trips, dead ends, ties, alternatives |
| `recipe_cost.lua` | raw cost of a route, to break ties |
| `raw_sources.lua` | what a surface mines or pumps |
| `machine_pick.lua` | which machine and which belt, and whether the force can build them |
| `recipe_machine.lua` | each pick netted on its machine |
| `recipe_yield.lua` | what one craft gives and takes |
| `recipe_walk.lua` | the walk, totals, cycles, caps |
| `recipe_reply.lua` | the wire shape and the byte budget |

### The `surface` parameter

A new optional `surface` (string): where the things are made, the asker's own
surface as a rule. It decides what counts as mined or pumped. A name or index
the game does not have gives the usual unknown-surface reply. The tool desc
tells the model to send it.

### Pick rules

The plan ranked three ways: the recipe named after the item, a recipe whose
`main_product` is the item, then the largest net output. The build keeps those
and puts three rules around them. In order:

1. The named recipe, then main product, then net output, then name. Net output
   here is at the research bonus alone. The machine is chosen after the pick,
   so its own productivity changes the figures, never the ranking.
2. A round trip is dropped. A side recipe (neither named after the item nor
   with it as main product) is a round trip when one of its inputs is, by that
   input's own best recipe, made from the item. Emptying a barrel is the case:
   2.0 gives every fluid an `empty-<fluid>-barrel` recipe, and walked, it made
   water out of water barrels filled from water. On the live force, blue
   science's iron plate went from 720 to 1378.64 a minute before the rule.
   The item's own recipe is never dropped, so asking for water barrels still
   fills them.
3. A dead end ranks last. A side recipe that takes an input the force cannot
   get (no usable recipe, and not mined or pumped on the surface) loses to
   every candidate whose inputs all resolve. Nuclear fuel reprocessing is the
   case: it nets three uranium-238 a craft to uranium processing's 0.993 and
   charged a burnt fuel cell as raw. The check is one level deep and for side
   recipes only. Named and main-product recipes keep their place.
4. A tie goes to the cheapest route. Candidates level on every rule above are
   ordered by the raw cost of a walk 5 levels deep (`recipe_cost.lua`), a route
   through anything unobtainable ruled out, then by name. Solid fuel is the
   case: by name, heavy oil won at about 80 crude oil a fuel, where light oil
   costs about 22. A pick that a tie decided adds `tie` to `approx` when two or
   more of the tied routes were possible.

The others are still `alternatives`, at most 3 each.

### Raw sources follow the surface

The plan called an item with no usable recipe raw. The build also keeps an
item raw when the surface mines or pumps it, even when a recipe could make it:
a force that researched asteroid crushing still mines its iron ore. Those
recipes stay in `alternatives`, and the row says `raw = "mined"`.

What a surface mines or pumps (`raw_sources.lua`), first match wins:

- A space platform mines and pumps nothing.
- Otherwise the resources named in `LuaSurface.map_gen_settings.autoplace_settings`
  entity, and the fluid tiles named in its tile list. If those name nothing at
  all, the same read of `LuaSurface.planet.prototype.map_gen_settings`.
- If neither says, or no surface was given, every resource and tile in the
  game, and `any_planet` goes into `approx` when a raw row has a recipe that
  could make it.

`treat_missing_as_default` and `autoplace_controls` are not read. Measured on
the live 2.0.77 save (17 surfaces) and on a fresh 2.1.17 map after creating
Vulcanus, Fulgora, Gleba and Aquilo: every resource and fluid tile actually
placed was one the lists name, and nothing unlisted appeared. Nothing links a
resource to its control: `autoplace_specification.control` is nil for every
resource but scrap. What the lists gave:

- Nauvis and each of the 13 MTS copies (`mts-nauvis-N`) list coal, copper-ore,
  crude-oil, iron-ore, stone and uranium-ore, plus water and deepwater.
- The MTS landing pen has no planet and empty lists, so it falls back.
- A platform lists only the empty-space tile and no entity.

The reason it matters: Fulgora's oil ocean gives heavy oil, so a rule that
asked the whole game called heavy oil mined on every planet, and lubricant on
Nauvis lost its crude oil.

### Machine base productivity

The plan left machine base effects out of scope. They are in. A crafting
machine's `effect_receiver.base_effect` productivity adds to the research
bonus: the biochamber, electromagnetic plant, foundry and a modded crafter on
the live save carry +50%. The machine picker chooses them first wherever the
force can build them, being the fastest, so a walk that ignored it counted
their inputs 1.5 times over. `productivity` in the reply is `"research"`, or
`"research+machine"` once a row's machine adds some. Modules, beacons and
quality stay out.

`allowed_effects` is not read. Measured with real machines crafting on 2.0.77
and 2.1.17: the electromagnetic plant and foundry apply +50% to recipes whose
`allowed_effects` forbid productivity (big-electric-pole and
fast-transport-belt made 18 from 12 crafts), and research +30% applies to a
forbidden recipe too (12 crafts made 15). Research +280% on a +50% machine
made 4.0 times: the sum is capped at 300%.

### The yield formula is the engine's

The plan's formula (expected amount, skipped part, then the rest times the
bonus) was wrong in three places. One craft gives of one product

    c * mean over the roll of ( (r + e) + p * max(r + e - g, 0) )

with c the chance, r the amount rolled, e `extra_count_fraction`, g
`ignored_by_productivity` and p the productivity bonus. So:

- the chance gates the extra count too (3 at half a chance with half an extra
  is 1.75, not 2);
- the skip comes off the roll before the chance;
- the skip comes off each roll of a range, not off its mean. An item rolls a
  whole number from `amount_min` to `amount_max`; a fluid any amount between.

2.1 renames the chance. 2.0.77 has `probability`; 2.1 has
`independent_probability` and `shared_probability = {min, max}`, and its docs
define the chance as `independent_probability * (max - min)`. Reading
`probability` alone would count every 2.1 chance as certain.

Checked against the engine on 2.1.17 with
`LuaRecipePrototype::get_product_amount`: 0 mismatches over 1092 shipped
products and 42 synthetic probes at productivity 0, 0.25, 0.5, 1, 3 and 10
(worst error 4.9e-8, float32). A Monte Carlo of real crafting on 2.1.17 and
2.0.77, about 40,000 crafts a probe, agrees. Bonuses add and cap at the
prototype's `maximum_productivity`, as planned.

### Cost of a call

One filter answers for many items: ten names cost about what one does, so the
tool asks for a layer of the tree at once. A per-call memo reads each recipe
once, and the combined filter is "not hidden and makes one of these items"
joined for items and fluids. The engine drops hidden recipes itself, which is
the cheaper order. The memo lives for one call and nothing outlives it.
Measured on the real save (2.0.77, 60 calls a case): every median under 5 ms,
worst median 4.04 ms (seven packs, team-4), worst p95 5.9 ms. Before the work
the seven-pack case took 7.7 ms median and 10.2 ms p95. The combined filter
equals the union of the single filters less hidden: 712 of 712 recipes on
2.0.77, 684 of 684 on 2.1.17.

### The reply

- Fields that come out empty are omitted: `approx`, `raw`, `alternatives`,
  `rows`. Never `{}`.
- The reply fits 4000 bytes, under the service's 4096-byte cut. Rows and their
  alternatives are fitted together: the cut is the largest number of rows that
  fits with each row's alternatives. Setting every row's alternatives aside
  before fitting any row left all twelve science packs on an endgame force
  with 0 of 65 rows shown and 1174 bytes unused. Rows are largest flow first,
  so a cut keeps the plates. Targets and raw rows are never cut.
  Twelve packs on team-4 fit 3982 bytes, 13 of 40 rows.
- `approx` words: `byproducts`, `cycle`, `cap`, `tie`, `any_planet`.
- A target the 80-item cap stopped carries `capped = true`, not `raw`. A
  target with no usable recipe but a locked one carries `recipe_locked = true`
  beside `raw = true`.
- A raw row the surface mines or pumps says `raw = "mined"`.
- `did_you_mean` on `found = false`: the name respelled (case, spaces, a
  plural), then the name players give a base-game science pack, circuit or
  belt by its colour or planet (`red-science-pack` gives
  `automation-science-pack`), then names holding every word, then names
  sharing a word. Nicknames and plurals count: `gears` finds `iron-gear-wheel`.
  Five at most, items before fluids.
- The targets are capped at 12 items in one call.
- A bare value where a list was asked for is read as a one-entry list.

### Verification as run

The rig numbers: a full red belt is 1800 iron plate a minute on 48 steel
furnaces. Lubricant on `mts-nauvis-6` charges 240 crude oil and 120 water, and
with no surface it flags `any_planet`. On `platform-1` iron ore comes from
metallic asteroid crushing. Pirion's question through the real model
(DeepSeek V4 Pro on OpenRouter): 13.2 s, 3 rounds, 227 reasoning tokens, two
`recipe_needs` calls. Before: one round, 2420 reasoning tokens, 113 s. The
call count is two, not one as the success measure asked: the model split iron
and copper. The unit suite and the mutations are in TESTING.md. Stage 4 is
done: the fake model has a "science belts" keyword and the e2e scenario
"recipe_needs: science belts, Pirion's three packs by machine count" matches a
direct call.

Known limit: the tool does not balance oil byproducts. A model that did its own
cracking arithmetic on the long oil question got the ratio wrong.

## Out of scope for v1

- Modules and beacons. Machine base productivity is in (see "As built"); the
  reply says whether it counted research only or research plus machine.
- Balancing oil byproducts. Flagged by `approx`.
- Surface conditions on a recipe or machine: one that only works on one planet
  is still offered. What a surface mines or pumps is in (see "As built").
- Quality, and belt stacking.
- Mining drills for raw resources.

The service timeout and the one-question-at-a-time queue made question 17 hurt
far more than it should have. Those are separate fixes, not part of this tool.

## Prior art: RateCalculator

raiguard's RateCalculator (MIT; moved to codeberg.org/raiguard/RateCalculator in
August 2025) measures what a selection of built machines makes and uses. It
answers a different question, but its arithmetic is the same, and its history
records which edge cases bit a mature tool. Rules taken from it, by commit:

| rule | commit |
|---|---|
| skip a `research-progress` product, it crashed the mod | e5453bf |
| productivity bonuses add, they do not multiply | 41b3667 |
| clamp the productivity-skipped amount to the expected amount | 01d1d2e |
| cap at the recipe prototype's `maximum_productivity`, not a fixed +300% | 39b6f34 |
| count `extra_count_fraction` in the expected amount | 2b84437 |
| belts = items per second / (`belt_speed` × 480) | scripts/gui-util.lua line 139 |

The formulas are ours to write; no code is copied, and the companion takes no
dependency on flib or anything else.

## Later

Not in v1. Each is cheap once the walk exists.

- **Read the real machines.** "I have 48 blue science" probably means 48 built
  assemblers. RateCalculator reads `entity.crafting_speed` and
  `entity.productivity_bonus`, which already include modules, beacons and
  quality. A mode that sizes each target from the force's built machines would
  be exact at the top of the tree. RateCalculator only ever reads a player's
  selection box; a surface-wide search needs its cost measured first.
- **Drills on raw rows.** Drills = ore per second / (`mining_speed` /
  `mining_time`) / (1 + `force.mining_drill_productivity_bonus`). Do not copy
  RateCalculator's min/max branch (scripts/calc-util.lua line 584): its operator
  precedence is wrong.
- **Power.** Megawatts per row from `get_max_energy_usage()` × 60 plus the
  electric source's `drain`, times machines.

## Decisions for the owner

Decided and built: the name `recipe_needs`, a bare count is machines, and oil
is approximate and flagged. What remains open:

1. **Release.** Nothing is released and no version is bumped. The changelog
   entry sits under the next companion version. Ship it with the weekly batch
   when you ask for a bump.
2. **Oil.** A byproduct solver is still a much bigger tool. Say whether
   questions about cracking ratios are common enough to want one.
3. **Machines you have.** The "Later" idea of reading real built machines is
   still not built. A bare count of machines is taken as the machine count in
   the recipe, with the fastest machine the force can build.

# Part 2: records, milestones and restarts, as MTS tools

## The questions this exists to answer

From the AleForge log, 2026-10-11, 00:52 to 00:57:

| q | question | what happened |
|---|---|---|
| 34 | How many records or milestones did Team Recordbreaking break? | 5 rounds of `recent_events`, `count_events` and `sweep`, then past the token budget. $0.0177, no answer |
| 35 | Do you think Eagle34 is gonna break any of RuneBoggler's records? | "I don't have a tool that lists RuneBoggler's records" |
| 36 | how many times has eagle restarted | 6 rounds of history reads, then out of rounds |
| 37, 39 | can I still beat all the records? | "no records list is implemented" |
| 38 | stop shooting blanks and answer | compared rockets, kills and research instead, then said there is no records list |

Records and milestones live in MTS `storage`. History never held them, so every
round spent there was wasted. Restarts are counted nowhere.

Question 41, "who has walked most steps", is out of scope: neither the engine
nor MTS tracks it.

## Where it lives

In MTS, next to the two tools it already provides. `scripts/remote_api.lua`
declares `team_clock` and `team_clocks` in its `AGENT_TOOLS` table and serves
them through `agent_tools_v1` on `mts-v1`. Question 38 called one. The new tools
go in the same table.

The bridge does not change: no release, no new word, no new seam. The companion
still never names another mod. MTS knows about the bridge; the bridge never
knows about MTS.

Rules the bridge already sets for any provider:

- `force` is injected on every call. A tool that can answer for every team
  takes `all`, like the companion's own `current_research`.
- A reply over the 8,000-byte call cap is refused whole. Bound rows by bytes,
  not only by count; the companion's `bounded.fit` is the pattern.
- Doubles leave as short strings. MTS already has this as `hours()` in
  `remote_api.lua`.

## What MTS stores today

Read from `master` at 0.6.7.

- `scripts/records.lua` keeps one record per key: `first`, `fastest`, and
  `entries[force_name]`, each `{ team, tick, elapsed, online_elapsed }`.
  `elapsed` runs from the team's clock start and is the official basis for
  first and fastest. `online_elapsed` is the fair-schedule alternative, and it
  is nil on entries from older saves.
- `storage.tech_records` is keyed by technology name.
  `storage.milestone_records` is keyed `category:item@threshold` for the
  configured trackers (`milestones/config.lua`), and `ext:category@threshold`
  for milestones other mods report through `report_milestone`.
- A record names a slot, `team-4`. Never a person, and never the team's display
  name at the time.
- Releasing a slot strips that team's entries from both record tables and
  recomputes first and fastest from the survivors (`strip_team_from_records` in
  `scripts/team_slots.lua`). So records only ever describe teams still on the
  server. A disbanded team's records are gone, and "first" means "earliest
  surviving team".
- `storage.global_records.first_rocket` is a boolean: who launched it is not
  stored. `first_planet_landings[planet]` holds a force name but no tick, and
  release does not clean it, so it can name a slot's earlier occupant.
- Nothing counts restarts. `storage.left_teams[player_index][force]` marks the
  teams a player left, but release clears it.

## The tools

### `records`

What one team holds, or a table of every team.

| param | type | meaning |
|---|---|---|
| `all` | `boolean` | one row per team instead of the one force; default false |
| `kind` | `enum<tech,milestone,any>` | which record table; default `any` |
| `limit` | `integer` | rows for the one-force reply; default 20 |

One force:

```
{ found = true, force = "team-4", label = "Team zzmaxxing",
  first = 12, fastest = 7, finished = 40, set_fastest_ever = 15,
  rows = { { kind = "milestone", what = "science:automation-science-pack@1000",
             holds = "first" | "fastest" | "both",
             elapsed_hours = "3.4", online_hours = "2.9" } ... },
  total = 19, shown = 19 }
```

- `first` and `fastest` count the keys the team holds now. `finished` counts
  every key it has an entry for.
- `set_fastest_ever` replays each record's surviving entries in tick order and
  counts the times this team beat the fastest time standing when it finished,
  even if it has been beaten since. That is the honest answer to "how many
  records did they break". It covers surviving teams only.
- `rows` are the keys the team holds, newest first.

`all = true` returns `teams = { { force, label, first, fastest, held, finished } }`
for every claimed team, sorted by `held`. `held` counts keys where the team is
first, fastest or both, each key once.

The desc says what a record is, because the model does not know: first and
fastest per technology researched and per production milestone, timed on the
team's own clock since it claimed its slot, never game time. Records belong to
teams, so a player's records are their current team's.

### `record_chase`

For one team: every record it does not hold, and whether it still could.

| param | type | meaning |
|---|---|---|
| `kind` | `enum<tech,milestone,any>` | default `any` |
| `limit` | `integer` | rows; default 20 |

```
{ found = true, force = "team-9", elapsed_hours = "12.1",
  in_reach = 14, out_of_reach = 9, held = 3,
  rows = { { what, holder = "team-4", record_hours = "14.0",
             hours_left = "1.9" } ... },
  total = 14, shown = 14 }
```

A fastest record is in reach when the team has no entry for that key and its
elapsed clock now is under the record's `elapsed`. Equal is out. A key the team
finished more slowly is out, and so is a first held by another team. `rows` are
the in-reach records, the ones closing soonest first.

The tool gives the verdict and the model reports it. That is the same reason the
bridge's `rank_by_rate` exists (`service/internal/arith`): a model left to
compare figures gets the arithmetic right and the verdict wrong.

### A sweep metric

A `sweep` block on `records` uses the bridge's provider-declared metrics
(`docs/design/phase5-sweep.md`, and the contract at the top of
`companion-mod/scripts/sweep/registry.lua`). "Which team holds the most records"
becomes one call:

```
sweep = { axes = { "force" }, rows = "teams", name = "force",
          value = "held", unit = "records", metric = "records_held",
          args = { all = true } }
```

### `player_teams`, and the restart counter

New storage, never cleared on release:
`storage.player_team_history[player_index] = { claims, joins }`, plus
`storage.player_team_history_since`, the tick the table was first created.

- Count a claim beside `remote_api.raise_team_created` in `scripts/team_slots.lua`.
- Count a join beside each `raise_player_joined_team`. A claim's leader fires a
  join too, so joins into someone else's team are joins minus claims.

| param | type | meaning |
|---|---|---|
| `player` | `string!` | player name |

```
{ found = true, player = "Eagle34", force = "team-7",
  claims = 4, joined_others = 1, restarts = 3, counting_since_tick = 4100000 }
```

`restarts` is claims after the first. Joining someone else's team is reported
beside it, not in it. Counting starts when this ships: the live save's past
restarts were never recorded. The desc tells the model to say so, rather than
report a low number as the whole story.

## Files

| file | owns |
|---|---|
| `scripts/agent_tools/records_read.lua` | rows, counts and the `set_fastest_ever` replay over one record table |
| `scripts/agent_tools/records.lua` | the `records` tool and its sweep block |
| `scripts/agent_tools/record_chase.lua` | the `record_chase` tool |
| `scripts/player_team_history.lua` | the claim and join counter, and `player_teams` |

`scripts/remote_api.lua` only wires them into `AGENT_TOOLS` and the interface
table, as it does for `team_clock` today. Its `hours()` helper moves to a shared
place the new files can use.

## Stages

1. `records`, one force and `all`, with the sweep block.
2. `record_chase`.
3. The restart counter and `player_teams`, with its storage migration.
4. The same three on `factorio-2.1`. MTS pairs `master` with 2.0 and
   `factorio-2.1` with 2.1.
5. Replay the 2026-10-11 questions through the bridge on the rig.

## Verification

Headless, with `tests/mock_factorio.lua`. `tests/run.py` only requires
`tests.test_reaper` today; teach it to run a second file,
`tests/test_agent_records.lua`. Cases:

- counts and rows from a fixture with tech and milestone records, including an
  `ext:` key
- `set_fastest_ever` across three teams finishing in a known order
- after `strip_team_from_records`, the recomputed holder is the one reported
- `record_chase` at the boundary: a clock equal to the record is out
- entries with no `online_elapsed` still produce rows
- `player_teams` survives a slot release; a fresh table reports
  `counting_since_tick`
- a reply with many rows stays under 8,000 bytes

Through the bridge, against a save with records:

- the startup line `catalog: N tool(s) from 2 provider(s)` grows by 3
- `sweep` lists `records_held` among its metrics
- question 34 answers in at most 2 rounds
- "can I still beat all the records" answers with in-reach and out-of-reach counts
- "how many times has X restarted" answers in 1 call, and says when counting began
- no answer says "no records list is implemented"

## Out of scope

- **Server firsts.** Who launched the first rocket is not stored, and the
  landing table can name a stale slot. Leave both out until MTS records who
  and when.
- **Records by person.** Records name slots. Storing the members on each entry
  would allow it, going forward only.
- **Steps walked.** Nothing tracks it.
- **A history seam in the bridge.** A function on `ai-agent-bridge-v1` that
  lets any mod add its own moments to history would make "when did" and "what
  happened while I was away" include records and restarts, still without naming
  a mod. Held for now: the tools above answer every question in the log, and a
  tool with a description is easier for the model to find than an event name it
  has to guess. Revisit if "when did" questions keep failing.

## As built

Built in MTS on `master` and on `factorio-2.1`, uncommitted and unreleased.
The bridge did not change for Part 2. These are the differences from the plan.

### Files

`scripts/agent_tools/` holds `records_read.lua`, `records.lua`,
`record_chase.lua`, plus two helpers the plan did not list: `reply.lua` (the
byte budget, `hours()`, the echo clip) and `team_lookup.lua`. `hours()` moved
into `reply.lua`. `scripts/player_team_history.lua` holds the counter and
`player_teams`. `scripts/team_incarnation.lua` is new, for Part 3.
`scripts/remote_api.lua` only wires them in, as planned.

### `records`

- A new `held_only` parameter. With `all = true`, each row is just `force` and
  `held`. The sweep block asks for it: `args = { all = true, held_only = true }`.
  The bridge's sweep refuses a reply that shows fewer rows than its total,
  because a ranking over the survivors of a cut names whoever survived. Full
  rows for 60 teams fit the budget only while every count has one digit: with
  160 records finished by each team, 58 of the 60 fit. Held-only rows are about
  30 bytes each, 1.8 KB for all 60.
- Rows carry `force`, never a label. The bridge prints a force name as its
  label, so the model only ever reads force names, and a label would cost bytes
  on every row. The one-team reply still names `label`.
- `noncompetitive` marks a team that has played with a handicap.
- A milestone a mod registers with `first_threshold` is first-only. Its key is
  `ext:<category>@<first_threshold>`. It counts for `first` and `held`, has no
  fastest, and adds nothing toward `set_fastest_ever`. In `record_chase` it is
  never in reach.
- `limit` defaults to 20 and is capped at 100.

### `record_chase`

As planned. A staged team whose leader has not pressed Start Playing has no
clock yet and counts as zero.

### The clock tools

- `team_clocks` rows are lean: `force`, `members`, `online_hours`,
  `elapsed_hours`, and `paused` only on a team that is. No label, no raw ticks.
  Most online time first, so a server too big for one reply drops the teams
  that played least, and `shown` can be under `total`.
- `team_clock`'s reply field `claimed_tick` is now `clock_start_tick`. The
  value is the same: the clock's start, which is the Start Playing tick under
  Staged Start.
- Every clock desc now says the clock runs "since the team started playing".
- `docs/MTS_API.md` says agent tool replies are shaped for the model and may
  change. Mods use `get_team_list` and `get_team_info`.

### `player_teams`

- Reply fields: `found`, `player`, `force`, `claims`, `joined_others`,
  `restarts`, `had_team_at_start`, `counting`, `counting_since_tick`,
  `counting_for_hours` and `note`. A part of a name is enough to find a player.
  More than one match gives `candidates`, at most 10.
- `had_team_at_start` marks a player who was on a team when the table was
  created. On a save older than the counter, "claims after the first" would
  call that player's next claim their first, though they are starting over. For
  them every counted claim is a restart.
- `note` is the reply caveat, put where the model reads it with the numbers. A
  desc alone was not enough: asked on a server where counting had begun at that
  load, the model answered "restarted 0 times". The note is set when counting
  has not started, when the player already had a team when counting began, when
  their team's clock started before counting, or when counting began less than
  a day ago. There is no note when counting began at map tick 0. With the note
  in the reply, the same question came back as "0 restarts and 0 team claims
  recorded, but counting only began about 0.01 hours ago".
- Counting is still new-forward only, as planned.

### Byte budget

Every list reply fits 3900 bytes (`reply.BUDGET`), under the service's
4096-byte cut. A reply cut by the service is broken JSON, and the companion
refuses past 8000. Caller text the reply repeats back is clipped to 64 bytes.
On the real save every reply measured under 3900.

### Keys

The chat scope key MTS hands the bridge now carries an incarnation. That is
Part 3.

### Verification as run

Tests: `tests/run.py` runs `test_agent_records`, `test_agent_manifest`,
`test_agent_bytes`, `test_chat_scope`, `test_player_teams` and
`test_team_release` as well as the reaper tests. 701 pass on both lines.
Mutation runs killed every guard but two fast paths.

On the live save: `records` team-13 holds 3 (`set_fastest_ever` 3), team-4
holds 127, team-12 holds 5. `record_chase` team-9 has 26 in reach and 173 out.
The bridge's sweep ranks `records_held` over all 13 teams. Through the model:
"records of Team Recordbreaking" took one call and 10.2 s, "can I beat all the
records" 11.2 s (169 out of reach, 22 in reach), "who has the most records" 10.2 s.
A 2.1.17 server returned every reply at the same size as on 2.0.77.

## Decisions for the owner

Decided and built: a restart is a claim after the first (or every claim for a
player who already had a team when counting began), "broke a record" is
`set_fastest_ever`, and server firsts stay out. What remains open:

1. **MTS release.** Nothing is bumped. The next MTS release notes, on both
   lines, should list: the chat scope key now carries the claim tick,
   first-only milestone records, `team_clock`'s `clock_start_tick`, and the
   clock desc fixes. Pair the release with the bridge's, so the privacy fix
   reaches both (see Part 3, "Deploy order").
2. **Slot release.** Two fixes from the review need release lines too: a team
   no longer disbands when its last online member leaves while another member
   was offline mid-spectate, and freeing a slot moves anyone still on it to the
   landing pen.
3. **Disbanded teams.** Their records stay lost. Say if you want a history
   record kept going forward.


# Part 3: Team chat privacy

Built 2026-10-11, uncommitted and unreleased, in the companion, the service
and MTS. The glossary words are in CONTEXT.md; the operator's view is in
SECURITY.md.

## The leak

Reproduced on the real save at 01:32 on 2026-10-11, before the fix. Pirion
(team-6) said "SECRET-7731 team six hides the nukes north" in MTS's team-only
channel. Trophium (team-9, global) then asked "what was said in chat
recently?" and the answer printed Pirion's line, to everyone. The event file
logged the line as `{"player":"Pirion","force":"team-6","message":...}` with
no scope, so the service had nothing to hide it by.

## The rule

A private history row reaches the model only for a question asked privately
in the same scope key. A question asked in the open reads public rows only,
whoever asks it, a member of the private group included. The store applies the
rule on every read, in SQL, never the prompt.

## What a chat line records

The companion writes `scope` (the key) and `private` (a boolean) beside the
player, force and message of every chat line, and beside every question.

- A scope provider (`chat_scope_v1`) decides for the line when one answers.
  Nil, or a provider error, means "do not know".
- With no provider and two or more forces with players, online or not, the
  engine's rule applies: a plain line reached the speaker's force alone, so it
  is private, key = the force name, audience = that force. While every player
  shares one force, it is public.
- A question asked with the chat prefix takes its chat line's scope. `/ask`
  and remote asks keep the old rule: global unless a provider says otherwise.
  Before the fix the prefixed line was logged private but the question was
  global, so its text sat public in history and its answer printed to all.
- After a provider failure only a private answer from another provider
  stands. A global one gives way to the force rule, because the failing
  provider may have been the one that knew the line was private. A provider's
  private flag reads as private unless it is literally false or absent. An
  `/ask` question still goes global past a failed provider: it reads public
  rows only, but its echo and answer print to the whole server.
- A line the engine rule decides, on a server where a provider is installed,
  is private to the speaker's force and prints there, but is stored under the
  global key, which no question reads private rows under. Nobody reads it back.
  The force may have held other groups before, and a force name is not an
  audience. Its key is the force name only on a server with no provider at all.

## The service visibility predicate

Every events row carries `scope`, `private` and `epoch` columns
(`service/internal/history/visibility.go`).

- Only the current epoch is read, and within it a private row is visible only
  to a question that is private and carries the same key.
- A question in the open, a question from a companion that sends no scope, and
  a read that carries no scope see public rows only.
- The reads covered: `recent_chat`, `catch_up`, `recent_events`, `last_event`,
  `count_events` and the briefing's chat. A count covers only the rows the
  question may read, so a private channel's volume stays private too.
- `recent_chat`, `catch_up` and the briefing's `ch` rows carry each line's
  speaker force and its private flag. A private line still reaches only a
  question asked in its own channel, so the flag reveals nothing new. Without
  the force, the model attributed Pirion to "other teams" for his own team's
  question.
- An answer row is as private as its question, found by `qid` in the same
  epoch. With no `qid`, or no question there, it is unreadable.

## Fail closed

- Only a literal `false` is public. Any other value in the `private` field
  reads as private.
- A private row with no usable key matches no question's scope, so nobody reads
  it back. The empty key is refused by `chatscope.Scope.PrivateKey`, so an
  unreadable row can never match.
- A provider key is opaque and compared whole, byte for byte, and is at most 64
  bytes. A longer key is refused, never cut, and its answer is private under
  the global key whatever it said, so two keys that share their first 64 bytes
  never become one audience. Labels (64 bytes) and tags (200 bytes) are cut on
  a character boundary; a tag is cut without tearing a rich-text tag.
- A save that cannot be placed in an epoch loses its old rows to the model
  rather than leaking them.

## Map epochs

One history file outlives its map. Every map names its forces the same way,
so an earlier map's private chat would read as this map's under the same key.
An epoch is the run of rows one map logged (`service/internal/history/epoch.go`).

- The companion makes a **map id** once per map and keeps it in
  `storage.aab.map_id`: `<seed>-<tick>-<random>`. Every save and load of one
  map carries the same id. It is made in `on_init`, or at the first event write
  for a save from before 1.0.6.
- The first line of every game session in the event file is a `session` line,
  data `{map_id}`, written in the same write that truncates the file.
  Another map starts a new epoch. The same map keeps its epoch through any
  reload, except a drop into the map's first five minutes (18,000 ticks): two
  maps made from one fixed seed carry the same id, and a map that young is new.
- With no session line, the tick decides: a drop into the first five minutes
  or a drop of more than thirty minutes (108,000 ticks) is a new map. A smaller
  drop is an autosave rollback of the same map.
- `status` carries `map_id`. Every 30 polls the service reads it, and when the
  file has not named that map (the session line was written while the service
  was down, or events are off), it records a session row of its own with data
  source `status`. The status is judged against history as it stood before the
  call went out.
- A count below the highest question id the epoch logged starts a new epoch.
  The status always carries the companion's `last_id`, so the status rows
  apply this today; a session line would too once the companion writes
  `last_id` into it (not yet). This separates same-seed maps, and also starts an epoch on a rollback to a
  save from before some logged questions. Fail closed, accepted.
- The `status` op writes storage once: it makes the map id on a save that has
  none yet. Every peer runs the command at the same tick and makes the same id,
  and a lost reply costs nothing because the id is kept.

## The outage reset

A call that gets no reply, or a reply over a new RCON connection, counts as an
outage, including a save that blocks RCON past the 30-second I/O timeout. A
restart is the only way a headless server loads a save, so on the next reply
the service starts again before it answers anything: cursor back to the first
question, held answers dropped, every session ended. An answer, and the notice
for a refused artifact, only ever goes over the RCON connection its question
was polled on. The game loses nothing, because only `answer` writes storage.
A game that went back with no outage in between is caught by the question
count: the highest question id issued going down logs "the game went back".
The residual case is two maps from the same seed where neither logged a
higher question id (SECURITY.md).

## Provider key contract, and incarnation

A key must name one audience and never a later one. A provider that reuses
names adds a generation. MTS recycles slots: once team-4 is released the next
team to claim a slot can be team-4 again, so MTS's key is
`<force>@<claim tick>`, for example `team-4@81234`.

- A team already holding its slot when a save first loads this MTS version
  reads `team-N@legacy-g<generation>-t<update tick>`, unique to that save and
  update. Never one shared constant. The slot's generation is bumped on every
  release, so an entry a downgrade left behind is never inherited.
- A player on a free slot's force gets the global scope. Global stays `global`
  for the global channel and `!` shouts.
- Under Staged Start the key uses the claim tick, not the Start Playing tick.
- Rows the bridge filed under the old bare key (`team-4`) match no key MTS
  sends after the update, so nobody reads them back, not even the same team.
  That is a one-time loss of context, on the safe side. Bare-tick entries from
  the earlier dev build are not trusted and are re-keyed on the next update.
- The key spans one team's whole life in a slot. Members who join later share
  it and can be answered from that team's earlier private chat. A member who
  leaves stops matching it.
- Two narrow gaps remain. A claim, a release and a new claim of the same slot
  inside one tick would give two teams one key. Loading a save from before the
  update with the new MTS also gives that save's team `legacy`, so a rollback
  across the update inside one epoch could make two teams share `team-N@legacy`.

## Legacy rows and the migration

A chat line logged before lines carried a scope states none. Factorio hands a
plain line to the speaker's force alone, so the migration (and ingest) makes it
private to the force it was logged with. A line with no force is the server
console's and is public. A question that states no scope predates scope
providers and is global. Every other event is a fact about the game and public.

One rule only the migration applies: an epoch whose rows name exactly one force
and no row that says it was private was heard by every player there, so its
legacy chat is public. Ingest never does this, because it cannot know. The
migration also places every old row in an epoch, and an answer row inherits
from its question in the same epoch.

## Deploy order

Update the service first, or both together. An older service applies no
visibility at all and stores the `session` line as an ordinary event row with
no player or force. A new service with an older companion keeps legacy chat
private to its force and falls back to the tick rule for epochs. The MTS key
change does not depend on order: the new keys never match old rows. Update the
service with companion 1.0.6.

## Fixed alongside

- Asker mode no longer broadcasts an answer whose asker has left. It prints to
  nobody, with a log line, and the question is still marked answered. A mod's
  question with no player still prints to the server. A private question always
  prints to its own audience whatever the setting says.
- The `answer` op refuses an artifact that would print nothing, as
  `bad_artifact` with m = `<shape> would print nothing: its title and every
  line, item or cell are blank`. Colour and font tags, empty tags like `[img=]`,
  spaces and control characters do not count.
- Sessions are keyed by a struct of scope and name, so a key that itself holds
  `#ops` never shares another key's `#ops` session.

## Verified

Privacy matrix on the rig (MTS team-6 talking team-only): Trophium (team-9,
global) saw only the public line. LolyP0p (team-6, private, key
`team-6@legacy`) saw Pirion's line. zzh8829 (team-4, private) and simihu
(spectator, global) did not. Question rows are private under their asker's key,
answers inherit, the server console line is public, every row is epoch 1. A new
map under a running service: the service logged "the game went back", question
1 on the new map answered in 1.1 s, "chat" returned none of the old map's
lines, and history held epoch 1 (old map) and epoch 2 (new map). Through
DeepSeek V4 Pro: Trophium asking what team-6 said got "Team Robot Abuse has
said nothing in recent public chat". Kyle372 (team-6, private) got Pirion's
line marked private channel. The numbers and mutations are in TESTING.md.

# Part 4: Answers

Built 2026-10-11, uncommitted and unreleased, service and companion.

## Caps doubled, with refuse and rewrite

Questions 37 and 38 on the live server ran past the 160-character cell cap in
ordinary sentences and were cut mid-sentence ("Name a couple and"). The counts
never needed raising, the cell cap did.

| cap | was | now |
|---|---|---|
| characters per line or cell | 160 | 320 |
| bytes in one answer, service | 6,000 | 12,000 |
| bytes per line, companion safety net | 640 | 1,280 |

Counts are unchanged: 3 summary lines, 10 list items, 5 table columns, 8 table
rows, 5 comparison rows. Runes are counted, not bytes, so a cut never splits a
character. The companion's 1,280 bytes is the same 320 characters at four bytes
a character.

An answer over a cap no longer goes out cut without a word. The service sends
it back to the model with a list of what is over, in words the model can act
on ("Line 1 is 376 characters, the cap is 320: rewrite it shorter..."), while
the model still has a round, at most twice. At most six problems are named at
a time. Only when no round is left, or the model answered in prose too long
for a summary, is the answer cut, at a word boundary with an ellipsis, and the
ledger records `clipped`. A cut never leaves a dangling `[` or an unclosed
`[color]` or `[font]` tag. The prompt tells the model the caps.

Rig: three 320-character lines arrive whole with no ellipsis, an 8-row table
prints 8 rows, a ten-item list of 320-character items (3.2 KB) renders whole,
and three lines of 320 CJK characters (960 bytes each) render whole. A blank
artifact is refused as `bad_artifact`. A long oil answer through DeepSeek took
one over-cap refusal ("Line 1 is 376 characters, the cap is 320"), was
rewritten, and arrived whole with `clipped` false.

## Icon repair chain

Factorio prints a tag it cannot draw raw, brackets and all. The model wrote
`[img=entity.biter]` for a kind of enemy with no prototype by that name, and
`[img=item.red-transport-belt]` for the belt the game calls
`fast-transport-belt`. Dropping the tag left "A  carries 30/s". Each tag now
goes down one chain (`companion-mod/scripts/sprites.lua`), the first step that
finds something the game has wins:

1. A planet path is read as the space location it is.
2. The same name in another class: `item.crude-oil` becomes `fluid.crude-oil`,
   `entity.iron-plate` becomes `item.iron-plate`.
3. The name respelled (`scripts/close_names.lua`): lowercase, hyphens for
   spaces and underscores, a plural dropped, then step 2 again.
4. The name players give a base-game item: the red belt is
   `fast-transport-belt`, a blue science pack is `chemical-science-pack`. The
   whole name has to be a nickname.
5. The plain words: `entity.biter` becomes "biter", `purple-widget` becomes
   "purple widget".

Clickable tags (`[item=x]` and kin) go down the same chain and end at their
plain name. A name the game really has always beats a nickname. A sprite
prototype's bare name is kept, as are `tile.water`, virtual-signal and quality
tags. Every step asks the game, so the chain works on a modded server.

## Personality

One server-wide voice. The setting is the runtime-global string `aab-personality`
(on the Map tab of the mod settings). The service config
`agent.personality` is the fallback. `/aab-personality` shows the voice to
anyone, and an admin sets it (`/aab-personality <text>`), turns it off (`off`)
or hands it back to the operator's config (`reset`). It is admin-only to change
because a voice is server-wide. A value is at most 500 bytes.

The companion stamps the setting onto every question it hands over, and one
that says anything wins over the config. The service
(`service/internal/agent/voice.go`) resolves it:

- blank means the config decides. `off`, `false`, `no`, `none`, `disabled`,
  `0` and `n`, in any case, mean no voice. A preset name (`factorio`) is that
  preset. Anything else is free text.
- Free text is sanitised to one short line with no rich-text tags and is always
  followed by the same guard sentence the preset ends with: "Flavour never
  changes a number, a unit, an item name or a table cell." A voice changes how
  an answer sounds, never what it says.
- The voice is the last part of the system prompt, so every question in the
  same voice reads the whole prompt from the cache and a change of voice costs
  one cache write.
- The service logs once when the map's setting starts or stops deciding the
  voice. The ledger records `voice` per question as `off`, a preset's name or
  `custom`.

An RCON `settings.global` write is refused by the engine, so the command
writes the mod's own runtime-global setting itself. On the rig: the setting changed
over RCON, the service logged "voice: custom, from the map's personality
setting, which overrides agent.personality", the ledger said `custom`, and
reset worked. A pirate voice answered "Yer lab rats are workin' on [tank] right
now, just 1% in, arr." with the numbers intact.
