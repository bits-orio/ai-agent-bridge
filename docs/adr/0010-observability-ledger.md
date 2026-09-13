# ADR 0010: A JSONL ledger for cost and latency

Status: accepted, 2026-09-12.

## Context
Every cost and latency decision so far was made from one-off log readings. The asker-line
prompt-cache regression of 2026-09-12 was found by eye, not by a report. The service has no
per-round timing data yet, so the claim that model time dominates the roughly 105 ms RCON floor
is a prediction, not a measurement.

## Decision
The service writes a JSONL ledger, one object per round and one per question. Per round:
question id, round number, model, provider, timing, all six token counts and cost on
`model.Usage`, stop reason, and per tool call its name, clipped arguments, result bytes,
duration, duration minus the RCON floor, and ok or the error. Per question: asker, force,
surface, session key and whether it was fresh, the text, rounds, lookups, shape, cost, wall
clock split into model ms and RCON ms (RCON ms carrying `briefing_ms` as its own summand),
whether it refused and why, which voice was active, `briefing` (on, off or failed),
`briefing_bytes`, `briefing_tokens` (estimated), `zero_lookup` (a question answered with no
tool call), `asked_back` and `awaiting_reply_resolved` (whether the player's reply landed in
the same session).

## Consequences
An `aab stats` subcommand reads the ledger. Tool repeat count within a round names the next
tool to make plural, from evidence instead of guesswork, and cache hit ratio becomes the
regression alarm for prompt-prefix stability. Three new rows: zero-lookup share of questions
and briefing cost against rounds saved, both reported over questions where `briefing` is on,
make Decision 13's free tier and the briefing's own cost argument falsifiable; ask-back rate
against its resolution rate measures whether the anchor design worked. About 3MB a day worst
case; the ledger holds player chat, so `*.jsonl` joins `*.log` in `.gitignore`.

