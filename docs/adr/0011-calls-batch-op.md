# ADR 0011: One rpc trip per round, the `calls` batch op

Status: accepted, 2026-09-12.

## Context
The service already serializes every tool call in a round through one connection: the RCON
client (service/internal/rcon) holds a single connection behind one mutex, and `runReads`
(service/internal/agent) fans reads out across goroutines that still queue on that same mutex.
Each round trip costs roughly the measured 105 ms RCON floor, so N tool calls in one round cost
about N times that: five calls cost about 525 ms, and the 21-lookup question that motivated ADR
0009 cost about 2.2 seconds of network, none of it model time.

## Decision
A new `calls` op in `companion-mod/scripts/rpc.lua` accepts a whole round's tool calls as one
array and returns an array of results in one round trip, dispatching each call through the
same path `call` already uses. It ships beside `call`, not instead of it: a purely additive
change to the frozen protocol `aab-rpc-v1`, exactly what CONTEXT.md invariant 6 already allows
without a new interface name. `calls` gets its own reply cap, `CAPS.calls` at 65536, sized
above `CAPS.call`'s own rise to 16384, since one `calls` reply now holds what used to be N
separate `call` replies.

## Consequences
A round of N tool calls falls from N times the RCON floor to one round trip regardless of N,
because the mutex is crossed once per round instead of once per call. A companion built before
this ADR simply lacks the op: the service falls back to serialized `call` trips against it,
the same fallback ADR 0008 describes for a companion whose dedicated `briefing` op is missing.
No existing provider, tool or client has to change to keep working, since nothing about `call`
itself moved.
