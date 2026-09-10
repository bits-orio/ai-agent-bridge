# ADR 0002: Three seams, tools by probe, questions by interface, answers by event

Status: accepted, 2026-09-10.

## Context
Other mods must add tools, submit questions and receive answers while the
companion knows nothing about them. Three candidate designs were judged from
three lenses (Factorio lifecycle, mod-agnosticism, backend fit). The scanned
probe won under all three because the engine already ships a readable registry
of every remote interface and function name.

## Decision
Tools: a provider exposes a zero-argument `agent_tools_v1` function on any
interface it owns; the companion scans `remote.interfaces` for it on every
turn and stores nothing. Questions: the companion owns the frozen interface
`ai-agent-bridge-v1` with `ask` and `get_event_id`. Answers: the companion
raises `on_answer` for any subscriber and prints to the asker.

## Consequences
A provider adds a tool in under forty lines with no dependency in either
direction and no change to its own frozen API. A removed provider vanishes on
the next scan. The Discord bridge submits questions the same way any mod does.
