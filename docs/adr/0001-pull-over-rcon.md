# ADR 0001: Pull over RCON, nothing runs until a question arrives

Status: accepted, 2026-09-10.

## Context
The closest prior art, flma, exports live state to disk on every peer every
300 ticks and reads it with a local CLI. That costs every player on the server
whether or not anyone asks anything, and it only works where the reader shares
a disk with the game.

## Decision
The companion registers no periodic handler. Every read happens inside the
console command the service invokes over RCON. The service polls a pure-read
operation for pending questions at a short interval.

## Consequences
Works against hosted servers with no shell. The on_load and registry problems
disappear because nothing is discovered or stored at load time. Cost is paid
only when a question is asked, and the service can measure it per call.
