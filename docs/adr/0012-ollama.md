# ADR 0012: Local models through Ollama's native API

Status: accepted, 2026-11-13.

## Context
Operators asked for a model on their own hardware: no key, no bill, no
prompt leaving the network. Ollama is what they run. It serves two APIs, its
own `/api/chat` and an OpenAI-compatible one the OpenRouter client could
nearly speak as it is.

## Decision
A third provider, `ollama`, with its own small client on the native API. The
native API is the only one that takes `options.num_ctx`, and the service
has to set it: Ollama gives a model 4,096 tokens on a card under 24 GiB, the
rules and tools are about 10,000, and a prompt that does not fit is cut from
the front with HTTP 200. The client asks for 16,384 by default and turns a
cut prompt into an error, judged by bytes sent per prompt token reported.

Three things were measured on qwen3:8b and Ollama 0.34.0 and shaped the
client. Ollama drops a call to a tool name it was not given, and a small
model calls a game tool by the function name the rules use, so tools go out
under the function name wherever it is unique and come back under their
own. A malformed call arrives as text, so the client reads a known tool name
followed by a JSON object as the call it was meant to be. Reasoning maps to
`think` as a switch, because levels are per model.

Provider `ollama` has no default model, and its request timeout defaults to
five minutes.

## Consequences
A question costs nothing and reports a cost of zero; the daily cost cap
never trips. Ollama's cached prompt tokens count as cache reads, so the
per-question token budget holds the same number of rounds as on a hosted
route. Answer quality is the local model's: an 8B model answers simple
questions and fails hard ones, and the docs say so. The OpenRouter and
Anthropic clients are untouched.
