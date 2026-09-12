# Safety and security

What a player can do, what the model can do, what it can cost, and the
guards on each. Reviewed 2026-09-11 against the whole tree; every guard here
has a test.

## Who can do what

| actor | can | cannot |
|---|---|---|
| A player in game | `/ask`, the chat prefix, `/ask new`, `/ask #name`, `/ask sessions` | run `/aab-rpc` (refused with one private line; only RCON and the server console reach it), see another team's private question, forge an answer in the bot's name |
| Another mod on the server | ask through `ai-agent-bridge-v1.ask`, expose tools, a chat scope, force labels | anything a mod could not already do: a mod is server code |
| The service, over RCON | the `aab-rpc` protocol: poll, catalog, tool calls, answers | anything else; the RCON password itself is the server's root, so keep the port on localhost or behind a firewall, as Factorio's own docs say |
| The model | call the tools in the catalog, with arguments it chooses, and submit one answer | invent a tool, call a function a provider did not list, write anything: every tool is a bounded read |

Player-typed text (questions, names, chat) reaches the model as data. The
system prompt says so, and the worst a prompt injection can do is spend the
asker's own quota on odd tool calls.

## Spam and cost, the blanket guards

| guard | where | default | what it stops |
|---|---|---|---|
| ask cooldown per player | companion, `aab-ask-cooldown-seconds` | 5 s | a held-down key: refused privately before the echo, the ring and the service |
| asks per minute, whole server | companion, `aab-asks-per-minute` | 30 | chat flooding by any number of players |
| question text | companion | 400 bytes | a wall of text as a question |
| questions per player per hour | service, `agent.questions_per_player_per_hour` | 20 | one player's cost |
| questions per hour, whole server | service, `agent.questions_per_hour` | 120 | everyone's cost together |
| USD per rolling day | service, `agent.max_cost_per_day` | 5.00 | the bill, whatever the model turns out to cost |
| tokens per question | service, `agent.max_tokens_per_question` | 20,000, a cached input token counting a tenth | one runaway question |
| rounds per question | service, `agent.max_rounds` | 6 | a model that never submits |
| lookups per question | service, `agent.max_tool_calls` | 30; a round over the cap is refused and the model answers from what it has | dozens of surface scans from one question |
| output tokens per turn | service, `agent.max_output_tokens` | 4,096 | a model that writes essays |
| tool result bytes | service, `agent.max_tool_result_bytes` | 4,096 | a result that floods the context |
| answer size | service and companion | 6,000 bytes, 8 rows | chat spam per answer |
| live sessions | service | 200 | a player inventing a new `#name` every question |

A refused question is a private notice to the asker, never a line to the
audience, so a player's spam cannot become the server's. Questions asked by
another mod through the interface are exempt from the companion's rate
limits, mods being server code, and still count against the service's caps.

## The game thread

Every tool runs inside the RCON command on the game thread, so a slow tool
is a stutter every player feels. The tools are bounded reads: engine counts
and finds with a hard limit (2,000 entities for `find_entities`), sorted and
cut lists, one flow read per item and quality. The service logs any tool
call that took over 100 ms so an operator can see which one. Two known
costs to watch: `production_since` on a game with every quality unlocked
makes one engine call per quality and precision window, and `list_players`
sorts every player the force has ever had before cutting.

Polling is one small command a second, the labels and scope probes are one
call each per question, and the catalog is rebuilt every ten minutes.

## Data

The service log and `events.jsonl` hold every question, including ones
asked in a team's private chat, since the operator's service answers them.
`events.jsonl` grows for the life of a save; turn `aab-events-enabled` off
to stop it, or rotate it from the service's host. Prompts leave the server
only for the model provider; the default OpenRouter routing denies providers
that train on prompts.
