# ADR 0009: Tiers, and bounded walks over raised caps

Status: accepted, 2026-09-12.

## Context
One question cost 21 lookups and was refused outright: `list_forces` returned 24 forces and
the model called `current_research` once per force before hitting the per-question lookup cap;
raising `max_tool_calls` treats the symptom. Reads divide cleanly into two kinds that differ
by orders of magnitude in cost: counters the engine already maintains, and walks over
`find_entities_filtered`, which builds a Lua wrapper per entity.

## Decision
Counter-backed reads always sweep every force, player or surface in one call and are never
singular: a tool widens in place under the name it keeps, as `current_research` did with `all`
in release 1.0.3, and the manifest may not hold two tools answering the same question at
different widths. Entity walks stay per force, bounded by a work budget and a radius (2000
entities, 64 tiles by default), and every reply carries `covered`, `examined`, `budget` and
`exhausted`; only a call with no usable anchor returns the structured refusal
`refused`/`why`/`accepts` instead. Cheap paths go in front of expensive ones, and every
manifest entry declares its tier.

## Consequences
A `refused` result reaches the agent loop as an ordinary tool result and does not count
against the lookup cap; an answer that is itself an ask-back marks the session awaiting a
reply. The logistic-network path for item search (`get_item_count`, `get_supply_counts`) and
the nameplate capacity path (`count_entities_filtered` times `get_max_energy_production`) stay
the two paths that keep a walk's refusal rare rather than routine. Policy in Lua cannot be
argued with the way a prompt instruction can.

