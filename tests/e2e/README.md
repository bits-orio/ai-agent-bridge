# AI Agent Bridge: end-to-end harness

Black-box checks of the companion mod and the service working together,
against a real (if throwaway) headless Factorio server. Python 3 standard
library only, no dependencies to install.

See `docs/design/phase1-2-spec.md` § Harness for the contract this
implements.

## What it does

`run.py`:

1. Builds `service/aab` through Docker (no Go toolchain assumed on the
   host), unless `SERVICE_BIN` points at an existing binary. If a live
   service runs from `service/aab`, never let the harness overwrite it: build
   your own binary elsewhere and pass it in (see "A machine with a live
   service" below).
2. Starts a headless server (`rig.py`) under `tests/e2e/.run/`, fresh on
   `base/freeplay` every time, with a mod directory linking in
   `companion-mod` and, if it exists yet, `tests/provider-mod/aab-test-provider`.
3. Writes `tests/e2e/.run/aab.e2e.yaml` (model `fake`, the scripted
   deterministic model, and there is no Anthropic API key in this harness
   and never should be; the ledger, `history.sqlite` and `daily-spend.json`
   all land in `tests/e2e/.run/`; `toasts.when_empty: true` so a milestone
   toast is sent with nobody online) and starts the service against it as a subprocess:
   `aab run -config tests/e2e/.run/aab.e2e.yaml`.
4. Runs every scenario in `scenarios.py` against the live pair over RCON,
   the same protocol a real operator's service speaks
   (`companion-mod/README.md`).
5. Prints one `PASS`, `FAIL` or `SKIP` line per scenario with the observed
   value, tears the server/client/service down, and exits non-zero if
   anything failed.

## Running it

```sh
python3 tests/e2e/run.py             # server + service, no player
python3 tests/e2e/run.py --client    # also a standalone client, for the
                                      # scenarios that need a real player
```

Or via the repo `Makefile`: `make e2e` / `make e2e-client`.

`--client` needs a display (there is no headless Factorio client) --
run it under a real desktop session or a virtual one such as Xvfb.

### Env vars

| var | default | meaning |
|---|---|---|
| `FACTORIO_HEADLESS` | `~/factorio-dev/headless-2.1.23/factorio/bin/x64/factorio` | headless server binary |
| `FACTORIO_CLIENT` | `~/factorio-2.1/bin/x64/factorio` | standalone client binary, only used with `--client` |
| `SERVICE_BIN` | (unset) | skip the Docker build, use this `aab` binary instead |
| `E2E_CONTROL_PORT` | `8090` | control API port for the harness service; set it when a live service already holds 8090 |

### A machine with a live service

A live service may be running from `service/aab` and listening on 8090. Then
do not run `make service`, and do not let `run.py` build into `service/aab`.
Build a binary of your own with the local Go toolchain, and move the port:

```sh
export PATH=$HOME/.local/go/bin:$PATH
(cd service && go build -o /tmp/aab-e2e ./cmd/aab)
SERVICE_BIN=/tmp/aab-e2e E2E_CONTROL_PORT=8191 python3 tests/e2e/run.py
```

Rebuild the binary after every change to the service or to the fake model,
since the harness runs whatever `SERVICE_BIN` points at. Run one harness at
a time: it starts a Factorio server of about 2 GB, and a game of your own
(Steam, say) is left alone.

### Ports and state

Game port `34210`, RCON port `27110`, RCON password `rig`. These are fixed
(not configurable via flags) so the harness never collides with a developer's
own long-running dev rig on different ports. Everything the harness writes
lives under `tests/e2e/.run/` (git-ignored): per-instance write-data dirs
(`w-server/`, `w-client/`), logs, the generated mods dir, mod-list and
`mod-settings.dat`, the generated service config, `history.sqlite`, and (only
while a `--keep` run is still up) `pids`. `history.sqlite` is deleted, along
with any `-journal`/`-wal`/`-shm` siblings, at the start of every run before
the service opens it. `history.Open` never truncates an existing file, so
without this the last-death scenario could pass on a row a *previous* run
left behind rather than the one it just created.

`mod-settings.dat` is written before the server starts because a
runtime-global mod setting cannot be set any other way from here: the engine
refuses `settings.global` writes from a console command over RCON ("Settings
can only be changed by the owning player or the mod that made the setting"),
and the console exemption in the Lua docs covers player settings only. The
harness uses it to turn the chat trigger on, `aab-chat-prefix = "!ask "`.

The same file carries the harness's startup settings (`rig.STARTUP_SETTINGS`,
passed to `rig.write_mod_settings(..., startup=...)`). Today that is one:
`aab-selftest-ops = true`. The Phase 0 diagnostic ops (`ping`, `big`, `write`,
`pcall_test`) answer `bad_op` unless that setting is on, and a test rig is
what it is for. The "off" case needs a second server start, so the Lua unit
suite covers it, not this harness. A startup setting cannot change after the
map loads, so add a new one to `STARTUP_SETTINGS` and re-run; do not try to
set it over RCON.

Two more files are run state and are deleted at the start of every run, with
`history.sqlite`: `daily-spend.json` (the rolling day's spend, which would
otherwise carry over from the last run) and `ledger-*.jsonl` (the loop
scenarios read their rows back from it, so it must hold this run only).

`--keep` leaves the server, client and service running after the scenarios
finish, for poking at by hand (state stays live under `tests/e2e/.run/`,
RCON still open on `27110`, control API at `http://127.0.0.1:8090`). It
prints the PIDs it left running and writes them to `tests/e2e/.run/pids`.
Stop them with:

```sh
make e2e-stop
```

which reads that file and signals whichever of those PIDs are still alive;
safe to run even if there's nothing left to stop.

If a previous run was killed hard enough to skip cleanup (a crash, a second
Ctrl+C), Factorio's write-data lock can outlive it. If the next `run.py`
hangs waiting for `Starting RCON`, check
`tests/e2e/.run/server.log` for a lock complaint and remove
`tests/e2e/.run/w-server/.lock` (and `w-client/.lock`) before retrying.

## Files

- `rig.py`: starts/stops the headless server and (optionally) a standalone
  client, builds the mods directory, and a minimal Source-RCON client.
  Reimplements the approach in `~/factorio-dev/rig` (`start-server.sh`,
  `rcon.py`, `server-settings.json`) rather than importing it, so this
  harness has no dependency outside the repo.
- `scenarios.py`: the actual checks, each a plain `(ctx) -> (status, detail)`
  function, plus the `aab-rpc-v1` / `/sc` helpers they share.
- `run.py`: orchestrates the above and is the only entry point; `rig.py` and
  `scenarios.py` are not meant to be run directly (though every function in
  them is import-safe and unit-testable on its own with a fake RCON).

## Scenarios and what they need

| scenario | needs `--client` | needs `tests/provider-mod` | needs |
|---|---|---|---|
| `status` | no | no | companion only |
| `providers` + `manifest` ops: companion's own provider | no | no | the two-step catalog ops (second review-fix contract item 1) |
| ask "what forces are there" | no | no | service `run` loop, `answers` op |
| ask "hello" -> provider greeting | no | yes (skips otherwise) | the probe seam, a second provider mod |
| ask "table of players" | no | no | the `table` artifact shape |
| ask "what is in the research queue" | no | no | the `research_queue` engine tool (breadth addendum) |
| ask "tech status of automation" | no | no | the `tech_status` engine tool (breadth addendum) |
| ask "logistic bots on nauvis" | no | no | the `logistics_summary` engine tool (breadth addendum) |
| ask "how many character on nauvis" | no | no | the `entity_count` engine tool (breadth addendum) |
| ask "evolution on nauvis" | no | no | the `evolution` engine tool (breadth addendum) |
| ask "rockets launched" | no | no | the `rockets` engine tool (breadth addendum) |
| ask "how long have we played" | no | no | the `game_time` engine tool (breadth addendum) |
| ask "pollution on nauvis" | no | no | the `pollution` engine tool (breadth addendum) |
| ask "iron plate production since the start" | no | no | the `production_since` engine tool (breadth addendum), `since_tick` 0 |
| `recipe_needs`: 60 red science a minute, oracle and ask | no | no | the `recipe_needs` engine tool on a fresh freeplay force: with the red science recipe still locked (2.0 unlocks it by a trigger technology) the pack comes back raw with `recipe_locked`; with that one recipe enabled a direct call must give 120 iron plate and 60 copper plate a minute, iron plate on 6.4 stone furnaces, the yellow belt, and red science's assembler marked `unlocked = false`; then "recipe needs" through the service must answer with the tool's own keys. The recipe is put back afterwards |
| `recipe_needs`: science belts, Pirion's three packs by machine count | no | no | Pirion's question through the service: "science belts" makes the fake model call `recipe_needs` once with chemical, automation and logistic science packs as `machines` 48, 24, 24 on `nauvis`, and answer with a table of the iron plate and copper plate rows the tool returned. The player force gets every recipe for the scenario (`LuaForce::enable_all_recipes`); a direct call must carry both plate rows, and the answer's rows must carry its figures. `LuaForce::reset_recipes`, plus anything research had enabled, puts the force back, and the scenario fails if its enabled recipes differ afterwards. Asked by a synthetic player, so the player force's hourly quota is untouched |
| selftest ops: on under `aab-selftest-ops` | no | no | the startup setting seeded into `mod-settings.dat`; reads it back and calls `ping` |
| `recipe_needs`: no rate answers with `assumed` | no | no | a call with neither `per_min` nor `machines` assumes `machines=1` and says so in `assumed` (one stone furnace: 18.75 iron ore a minute, mode `machines`); a rated call carries no `assumed`; `approx` always has `no_modules_or_beacons` |
| `recipe_needs`: iron plate on vulcanus and nauvis | no | no | creates the Vulcanus surface (`LuaPlanet::create_surface`) and enables every recipe; 60 iron plates a minute on `vulcanus` must be foundry and casting with lava among the raw inputs, no crusher, no `metallic-asteroid-crushing`, no iron ore; on `nauvis` it stays on a furnace and mined iron ore. Recipes and the surface are put back afterwards |
| `list_surfaces`: fitted and paged | no | no | adds 60 long-named surfaces; the default reply is at most 3,800 bytes and carries `next_offset`, following it visits every surface once in name order, `limit` cuts where asked, the last page has no `next_offset`. The surfaces are deleted afterwards |
| remote ask: 100 KB fields and odd audience indexes | no | no | an ask with a 100 KB `force` and scope key, and another with a 100 KB audience force, leave `poll` answering and get answered; a private audience listing player indexes -1 and 100000 accepts an answer instead of `bad_artifact` |
| loop: repeated tool call is not run | no | no | the fake model keyword `loop repeat` sends the same call on two rounds; the ledger marks the second `not_run: repeat`, counts one lookup, and the player still gets an answer |
| loop: summary text in the wrong field is salvaged | no | no | `loop salvage` sends a summary with prose in `text` and no `lines`; the player gets that exact text and the ledger round carries `salvaged` |
| loop: empty answer twice ends in 2 rounds | no | no | `loop empty` sends an empty summary every turn; the question ends after 2 rounds (not `max_rounds` 6) with the "could not put that answer into a form the game can show" notice and two `submit_refusals` |
| history: unknown event kind lists the kinds | no | no | `unknown kind` makes the fake model call `count_events{event:"restart"}`; the answer carries the tool's text, `no events of kind "restart"` and the kinds there are |
| answer op: malformed artifact -> `bad_artifact` | no | no | artifact validation ahead of rendering (review-fix contract item 5) |
| answer op: large table artifact accepted | no | no | the transport carrying an artifact past 1000 bytes (review-fix contract item 1); reads the question back through `answers` and checks the recorded shape and title match what was sent (second review-fix contract item 10) |
| chat prefix creates a question | yes | no | `aab-chat-prefix` seeded into `mod-settings.dat` |
| last death via history | yes | no | `events.jsonl` + the history tools |
| per-player quota | no | no | `questions_per_player_per_hour` (asserts both the 1st question, within budget, and the 21st, refused) |
| provider error -> `provider_error` | no | falls back to the companion's own `pcall_test` selftest op | `pcall`-wrapped `remote.call` |
| command: top rockets, no model round | no | no | `top rockets` answers from the companion's `sweep` with no model round: the player force gets `rockets_launched = 3` from the console first (a headless map has no player on any force, and the rockets sweep lists a force with players or a launch; writing the counter raises no event, so no toast), and the first data row must be `1 \| player \| 3`. The ledger row carries `command: top`, 0 rounds, no cost. The count is put back afterwards |
| command: card, technologies researched | no | no | `card` answers from `force_card` with a two-column table; its `technologies researched` row must equal the count of `researched` technologies the console reads for the player force. Ledger: `command: card`, 0 rounds |
| command: help lists top and card | no | no | `help` answers with a list naming `top` and `card`, printed to the asker alone. Ledger: `command: help`, 0 rounds |
| toast: first headline research, none for another | no | no | sets `logistic-science-pack` researched on the player force from the console (`LuaTechnology::researched = true` raises `on_research_finished`, `by_script` true), waits for the companion's `research_finished` line, then a `say` line in the event file and a `toast: said first_tech` line in `service.log`. Then a technology outside the headline list (`automation` or the next unresearched one) must produce no `say`. Both are set back to unresearched afterwards. Needs `toasts.when_empty: true`, which the harness config sets, since nobody is online in a headless run |

Every remote `ask` goes through `ask_via_remote`, which copes with the
server-wide cap of 60 asks per game minute on the `ai-agent-bridge-v1`
interface: when `ask` returns nil it waits for the next game minute (it
polls `game.tick`), retries up to three times, and then fails with a clear
message. A full run stays under the cap by a little, so the wait is rare.

The loop scenarios need the fake model's own keywords, defined in
`service/internal/model/fake/script.go` (`loop repeat`, `loop salvage`,
`loop empty`, `unknown kind`) and covered by `script_test.go`. They ask as
their own synthetic player index each, so no hourly allowance or cooldown is
shared with another scenario.

The command scenarios ask as synthetic player indexes 900021 to 900023:
`top` and `card` cost no model round but still count against the hourly
quota, which the quota scenario spends for the player force.

Everything not marked "companion only" also needs the service's `run`
subcommand and the fake model to exist and be wired up. As of this writing
(alongside `docs/design/phase1-2-spec.md`) several of these are being built
in parallel with this harness. That is expected, not a bug in the harness:
a scenario prints `FAIL` with the observed reply until its dependency lands,
then starts passing with no change needed here.

## What "PASS" actually proves (second review-fix contract item 10)

An `ask` scenario can look like it exercised a tool when it never did: the
fake model's `echo()` answers any unmatched question with a summary line
that starts `You asked: ` and simply repeats the question text back, and a
tool call that errored surfaces the Go client's own error text (always
carrying `aab-rpc:`, and often the code `provider_error`) as if it were the
tool's own result. Either one can satisfy a check that only looks for a
keyword the question text already contains, or only checks an artifact's
shape rather than its content. `real_answer_problem()` in `scenarios.py`
catches both, and every `ask`-style scenario calls it right after
`poll_for_answer` succeeds and before its own assertion runs. Two scenarios
add more on top of that:

- Every breadth-addendum scenario (`_ask_and_expect_field`) asserts a
  quoted JSON key with its colon, for example `"queued":`, never a bare
  word: a bare word can already be a substring of the question it asked
  (`"queue"` is in "what is in the research queue"), or of a Lua error's
  traceback (`"count"` is in `entity_count.lua`), neither of which contains
  a literal `"<key>":` fragment.
- "answer op: large table artifact accepted" reads the question back
  through `answers` afterward and requires the recorded shape and first
  line to be the table it sent, because the service polls concurrently with
  the scenario and can answer the same question first (with the echo
  fallback) between the scenario creating it and directly calling `answer`
  on it; checking only `ok: true` on that direct call cannot tell its own
  answer apart from having raced the service's already-answered
  short-circuit.

`questions_per_player_per_hour` (the quota scenario) is the one exception:
its "quota probe N" questions are deliberately worded to match no keyword,
so its first question is expected to come back as the echo, and it does not
call `real_answer_problem()`.
