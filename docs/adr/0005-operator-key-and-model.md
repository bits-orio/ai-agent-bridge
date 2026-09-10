# ADR 0005: The operator brings the key and picks the model

Status: accepted, 2026-09-10.

## Context
Cost is the operator's concern. A tool call is deterministic and needs no
model; delegation to sub-agents only pays off for multi-part questions.

## Decision
One Anthropic API key and one model choice in the service config. The top
model calls tools directly by default. Sub-agents on a cheaper model are an
option the planner takes, not the default path. Cost per session is reported
on the control API.

## Consequences
The operator sees exactly what a question costs and can move between models
without touching the game. The design has no hidden central service.
