# ADR 0003: Typed artifacts rendered by the companion

Status: accepted, 2026-09-10.

## Context
Answers must be small to keep tokens and cost down, look consistent in chat,
and stay safe when player-typed text flows through tool results.

## Decision
The model emits one of five structured shapes (summary, comparison, list,
table, notice) with hard size caps. The companion renders the shape with rich
text and item icons, in chat first and in a popup frame later.

## Consequences
Formatting tokens leave the model entirely. Every answer looks the same.
Injected text inside a result stays inside a cell and cannot change layout.
