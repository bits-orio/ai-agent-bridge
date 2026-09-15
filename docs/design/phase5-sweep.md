# Phase 5: answering "which one has the most" in one call

Status: designed, not built. Stage 1 is scoped into the unreleased 1.0.5.

## The question this exists to answer

> "I would like to make sure we are not overfitting the problem and fixing this
> only for thrusters. This kind of question is going to be asked for multiple
> kinds of things: which team has most of this, most of that, or something like
> that. In almost all of these cases, I would like it to be maybe 1-2 tool calls
> only. Even if there are 40 teams."

Not "make thrusters work". Make the *shape* work, so the next one is not another
release.

## What provoked it, from the ledger

Three questions, all on 2026-09-15, all the same shape and all expensive:

| q | question | cost |
|---|---|---|
| 60 | how many rockets has each team launched | 14 `rockets` calls in one round |
| 64 | how many solar panels on each team's nauvis | 16 lookups, 4 rounds, $0.0146 |
| 80 | which space ship has most thrusters, where is it | 15 lookups, 6 rounds, $0.0215, **no answer** |

Question 80 is the one that matters. It hit the round ceiling and told the player
to ask something narrower. Reading its trace, it failed for three separate
reasons, only one of which anybody guessed:

1. `list_surfaces` at its default limit of 20 returns 20 of 25 rows sorted by
   name, and every `platform-*` row sorts last. **Measured on the live server: no
   platform appears at all.** The model guessed `platform-1` in round 2 before it
   had ever seen one, then spent round 3 re-listing with `limit: 50` to find out
   it had guessed right.
2. Nothing says which force owns a platform, so once it had the names it had to
   try a force-by-surface cross product: 5 platforms times 14 populated forces is
   70 combinations. It managed 15.
3. `find_entities` carries no cost tier, so a counting question was answered with
   fifteen entity walks and zero `entity_count` calls.

## The inventory that makes this structural

Five of seventeen tools sweep. Each was retrofitted by hand in its own release
with its own reply shape:

```
sweeps:  list_forces  list_players  current_research  rockets  entity_count
do not:  pollution  evolution  logistics_summary  item_rate  top_items
         production_since  research_queue  tech_status  game_time
         find_entities  locate_player  list_surfaces
```

The measured costs of the obvious paths, taken against this tree rather than
assumed:

| approach | cached prefix cost |
|---|---|
| twelve new sweep tools | ~7,300 bytes (+58% of a 12,547-byte catalog) |
| twelve `all=true` retrofits | ~4,000 bytes |
| one generic sweep tool | ~600 to 1,500 bytes |

The protocol caps are not the constraint: the manifest reply is 8,255 bytes
against a 32,768 cap, at 25% of capacity. What *is* a constraint is that the
param grammar has no enum type, so a generic tool's vocabulary reaches the model
as prose.

## Four architectures, scored

Judged on three criteria by independent reviewers: does it deliver 1-2 calls for
*any* such question at 40 teams, will the model use it correctly, and what does
it cost to build and keep working.

| approach | scores | avg |
|---|---|---|
| `sweep`, one tool + metric registry | 6, 6, 5 | **5.7** |
| axis tools, three wide-row sweeps | 6, 5, 6 | **5.7** |
| retrofit `all=true` per tool | 3, 6, 6 | 5.0 |
| leaderboard, rank inside Lua | 5, 5, 5 | 5.0 |

A tie at the top, not a landslide. Recording that honestly matters: the winner
won on one argument, and if that argument is wrong the decision should be
revisited rather than treated as settled.

## The decision

**One `sweep` tool, with a metric registry that delegates to the tools that
already exist.**

The deciding argument is that it is the only one whose answerable set grows after
it ships. The retrofit hardcodes twelve metrics and makes a later envelope more
expensive, not less. The leaderboard freezes six metrics and four axes as Lua
enums, and already misses an axis the spec itself names (`player`). The axis
tools make the axis the tool name, so there are three axes forever and no
compound axis, which leaves question 64 unreachable at any call count because a
planet surface carries no owner.

The graft that saves it came from its own judge: as first written, the registry
**reimplemented** thirteen measurements that `environment.lua`, `production.lua`,
`logistics.lua` and `rockets.lua` already take, leaving thirteen pairs of code to
drift apart. Make each metric *declare* `{tool, axes, default_axis, args, value,
cols}` naming an existing manifest function, and have the walker call it. Then
there is one implementation of each measurement, every existing tool becomes
sweepable, and a future tool becomes sweepable by declaring three fields rather
than by anyone writing sweep code.

### The objection, and why it does not sink it

`metric` is a soft enum and nothing enforces it. `catalog/params.go` knows five
type words and degrades anything else to a bare string, so the vocabulary reaches
the model only as prose inside a description. A hallucinated metric name is not a
schema violation; it is a valid call that burns a round on the tool built to save
rounds. Two judges named this independently as the worst thing about the design.

This is not theoretical. The same model has already invented `mts-v1__catch_up`
by pattern-matching a prefix, and answered "15 teams (team-1 through team-15)" by
extrapolating a range from partial rows.

Three reasons it holds anyway. An unrecognised metric returns `found=false`
carrying every metric's card, which recovers in one round with a complete answer
rather than a one-line `provider_error`. The real fix is about twenty lines, one
type word in `paramTypes` and one branch in `parseParam`, and it is backward-safe
precisely because the grammar already promises that an unknown type word degrades
to a string on an older service. And no alternative avoids the problem: every
design that takes a metric or axis name by argument has the same hole.

**Adding `enum` to the manifest param grammar is an open owner decision, not a
prerequisite.** It should probably be taken.

## Staging

Stage 1 ships inside the already-staged, unreleased 1.0.5. It needs no service
change, so it cannot break a 1.0.4 companion by construction.

| stage | ships | where |
|---|---|---|
| 1 | platform columns on `list_surfaces`, its default limit raised, `per_surface` on `entity_count`, `rockets` sorted by value, `tier=2` on `find_entities` | companion 1.0.5 |
| 2 | briefing carries platform owner and location; stops filtering platform rows out on `ForcePlayers > 0` | service, feature-detecting |
| 3 | the `sweep` tool and its delegating registry | companion 1.1.0 |
| 4 | service-side ranker in the return path, so a sweep reply is ranked through the same `arith.order` that `rank` uses | service |
| 5 | remaining metrics, `enum` in the grammar, cross-provider metrics | optional |

Stage 1 alone makes question 80 a single call, because `per_surface` on the
existing `entity_count` sweep returns one row per force-and-surface and the
platform columns say who owns each. That is not throwaway work: it is the backend
the platform axis delegates to in Stage 3.

## What the existing five sweeps become

All five stay. None is absorbed, deprecated or removed; they become `sweep`'s
backends.

`list_forces`, `current_research{all}` and `list_players{all}` are **frozen, no
envelope ever**. `briefing.go` pins their exact reply shapes in decoders, and
they are three of the briefing's five per-question trips. The prompt tells the
model that a missing briefing key means "not available this time, never that the
thing itself is absent", so changing any of their shapes silently makes the model
confidently not know things. They also answer "what is", not "which has most".

## What this deliberately does not do

- Not building ADR 0011's `calls` batch op. Accepted and unbuilt, and orthogonal.
- Not fixing `agent.go`'s mid-JSON truncation at 4096 bytes, which cuts a tool
  result rather than refusing it. Real, separate, and it argues for the columnar
  envelope rather than against it.
- Not moving the five briefing decoders onto the envelope.
- Not shipping the ten named tools spec section 7 lists. Most become metrics.
- Not making `sweep` answer single-row questions. `entity_count` keeps "how many
  labs does team-7 have"; `sweep` owns "which team has the most labs". The
  overlap is deliberate.

## Open questions for the owner

1. **Add `enum` to the param grammar?** About twenty lines, backward-safe, and it
   turns the metric vocabulary from prose into something the tool-calling layer
   enforces structurally.
2. **May the companion's sweep call another provider's remote interface?** A
   metric could declare `{iface="mts-v1", fn="team_clocks"}` and become sweepable
   without MTS shipping anything. It also makes the companion depend on a mod it
   does not own.
3. **Spec section 5 says a tool widens in place and never gains a sibling for the
   width it replaces.** `sweep` is a sibling. Either the rule needs an exception
   written into it or this design violates it.
4. **Which row and pass budgets, and will you take one measurement first?**
   `entity_count`'s `MAX_PASSES = 600` was set against a measured 322. Every other
   number in every proposal is reasoned, not timed.
5. **Does `force.get_entity_count(name)` equal the sum of
   `count_entities_filtered{force, name}` over every surface?** This became
   load-bearing at commit `396951a`, which routed the sweep through the O(1)
   counter. Ghosts and surfaces pending deletion are the cases to check. Untested
   against a live server, because no released companion exposes the counter yet.
