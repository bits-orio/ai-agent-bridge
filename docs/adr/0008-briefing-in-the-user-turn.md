# ADR 0008: The briefing rides in the user turn

Status: accepted, 2026-09-12.

## Context
A question that needs context the model lacks costs a whole extra round: one call to fetch it,
a second to answer with it. The system prompt cannot carry that context instead, because text
that varies by question sits ahead of the cached prefix and forces a full cache miss on every
question, the regression measured on 2026-09-12.

## Decision
Before calling the model, the service places a compact snapshot in the user turn beside the
question, never in the text `systemPrompt()` returns, chosen to make the largest number of
questions answerable with no further lookup. Where the companion has it, this is one op, `{ op
= "briefing", qid = <question id> }`, looked up in the ring `companion-mod/scripts/rpc.lua`
already keeps and replied as `{ ok = true, r = <snapshot> }` under its own CAPS entry, 16384.
Where it does not, the service assembles the same snapshot from five serialized `call` trips,
about 525 ms at the 105 ms RCON floor, a cost the `calls` batch op (ADR 0011) also collapses to
one trip, so whichever lands first wins. The briefing is best effort: on timeout, error, or a
companion with neither op, it is omitted, logged as one line, and the question proceeds as
today rather than failing on the briefing's account, under its own two-second timeout inside
the question's budget, separate from the rpc call's own timeout.

## Consequences
A large class of questions costs zero lookups. `systemPrompt`'s output stays byte stable
question to question, so the cached prefix keeps paying the cached-read rate, and five trips
at 525 ms or one dedicated op both cost far less than the 2 to 6 second round either replaces,
so the briefing is paid for even on questions that never needed it. A briefing that fails
never turns a question into a failed one, only a plainer one.

