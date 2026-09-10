# ADR 0004: History lives in the service

Status: accepted, 2026-09-10.

## Context
Questions like "production since I last died" need event history that the
engine does not keep. Production history itself is free from the engine's flow
statistics up to 1000 hours.

## Decision
The companion appends deaths, alerts, chat, joins, research and rockets to a
server-side `events.jsonl`, the way the Discord bridge does. The service tails
it, locally or over SFTP, into SQLite and answers history questions from there.

## Consequences
The whole history of a save is queryable as tools. The companion's storage
stays small and the game never scans anything for history.
