# AI Agent Bridge: end-to-end harness

Black-box checks of the companion mod and the service working together,
against a real (if throwaway) headless Factorio server. Python 3 standard
library only, no dependencies to install.

See `docs/design/phase1-2-spec.md` § Harness for the contract this
implements.

## What it does

`run.py`:

1. Builds `service/aab` through Docker (no Go toolchain assumed on the
   host), unless `SERVICE_BIN` points at an existing binary.
2. Starts a headless server (`rig.py`) under `tests/e2e/.run/`, fresh on
   `base/freeplay` every time, with a mod directory linking in
   `companion-mod` and, if it exists yet, `tests/provider-mod/aab-test-provider`.
3. Writes `tests/e2e/.run/aab.e2e.yaml` (model `fake`, the scripted
   deterministic model, and there is no Anthropic API key in this harness
   and never should be) and starts the service against it as a subprocess:
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
| `FACTORIO_HEADLESS` | `~/factorio-dev/headless-2.0.77/factorio/bin/x64/factorio` | headless server binary |
| `FACTORIO_CLIENT` | `~/factorio/bin/x64/factorio` | standalone client binary, only used with `--client` |
| `SERVICE_BIN` | (unset) | skip the Docker build, use this `aab` binary instead |

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
| ask "what forces are there" | no | no | service `run` loop, `answers` op |
| ask "hello" -> provider greeting | no | yes (skips otherwise) | the probe seam, a second provider mod |
| ask "table of players" | no | no | the `table` artifact shape |
| answer op: malformed artifact -> `bad_artifact` | no | no | artifact validation ahead of rendering (review-fix contract item 5) |
| answer op: large table artifact accepted | no | no | the transport carrying an artifact past 1000 bytes (review-fix contract item 1) |
| chat prefix creates a question | yes | no | `aab-chat-prefix` seeded into `mod-settings.dat` |
| last death via history | yes | no | `events.jsonl` + the history tools |
| per-player quota | no | no | `questions_per_player_per_hour` (asserts both the 1st question, within budget, and the 21st, refused) |
| provider error -> `provider_error` | no | falls back to the companion's own `pcall_test` selftest op | `pcall`-wrapped `remote.call` |

Everything not marked "companion only" also needs the service's `run`
subcommand and the fake model to exist and be wired up. As of this writing
(alongside `docs/design/phase1-2-spec.md`) several of these are being built
in parallel with this harness. That is expected, not a bug in the harness:
a scenario prints `FAIL` with the observed reply until its dependency lands,
then starts passing with no change needed here.
