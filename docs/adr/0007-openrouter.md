# ADR 0007: Models through OpenRouter

Status: accepted, 2026-09-10.

## Context
The first live day showed the model bill is the product's running cost, and
that most of the levers (caching, thinking off, shorter prompts) still leave
a floor set by the model's own price. Claude Haiku 4.5 sits near a cent for
ten questions; DeepSeek's current models on OpenRouter are listed at a
tenth of that input price. The owner wants to move between them with a
config line, not a code change, and ADR 0005 already says the operator
picks the model.

## Decision
The service talks to OpenRouter's chat completions API through an in-house
client and treats OpenRouter's model ids as the model choice. The direct
Anthropic client stays selectable but frozen. OpenRouter's reported `cost`
is the cost the service logs and serves; the price table is kept only for
the direct backend. Thinking stays off by default on every route.

## Consequences
One key, one config line, any model OpenRouter lists that supports tool
calling, including Claude. Prompt caching keeps working on Anthropic routes
through explicit breakpoints and comes free on the routes that cache by
themselves. The service depends on OpenRouter being up; the `fallbacks`
list covers a model outage, not an OpenRouter outage. Reasoning blocks are
carried opaquely so tool loops keep working on models that require them
back.
