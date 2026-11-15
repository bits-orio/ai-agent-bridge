# AI Agent Bridge: checks against a real save

`tests/e2e` starts a fresh freeplay map: one force, one surface, no factory.
Most tool bugs only show on a played map with many forces, platforms, other
planets and modded item names. This folder loads a save you give it and
compares what the tools answer with what the engine itself says.

Python 3 standard library only. No model is called and no API key is used.

## What it needs

- A save file. It is copied into the run folder and never changed.
- The mod zips that save was made with, in one folder (your own mods folder
  works; they are only read, through links).
- A full Factorio install of the save's version. Space Age data does not ship
  in the headless download, so a headless-only build cannot load a Space Age
  save. Set `FACTORIO_FULL` or pass `--factorio`.

The server gets its own write-data folder, mod directory and ports (game
`34220`, RCON `27120`) under `tests/realsave/.run/`, which git ignores. It
never reads or writes your own Factorio config, saves or mod list, and it is
stopped when the run ends, on failure too.

## Running it

```sh
python3 tests/realsave/run.py --save ~/saves/my-map.zip
```

A before and after comparison, for a change to the companion:

```sh
python3 tests/realsave/run.py --save my-map.zip --companion HEAD --label before --expect-failures
python3 tests/realsave/run.py --save my-map.zip --label after
python3 tests/realsave/run.py --compare before after
```

`--companion HEAD` loads the companion as it is at that git ref, exported to
the run folder, so the working tree can hold the change. `--only T1,T3` runs
the probes for those finding ids.

The run exits non-zero when a probe fails, when the server log holds a Lua
or engine error, or when the server never came up. `--expect-failures`
records only. `--compare` exits non-zero when a probe that passed in the
first report fails in the second.

## What the probes are

Each probe in `probes.py` discovers what the map has, then checks one claim.
None names a force, surface or item from one particular save. Where it can, a
probe reads the same fact twice: once through a tool, once through the Lua
console, and the two must agree. Examples:

- The `robots` sweep metric against the sum of both robot kinds over every
  logistic network of every force.
- Every machine `recipe_needs` picks for a surface against that machine's
  own surface conditions on that surface.
- Every list reply against the size the service passes to the model.
- A remote `ask` with oversized fields, then `poll`, which must still answer.

A probe with nothing to check on the save prints `SKIP`. The probes that ask
questions change the loaded copy of the map, so they run last.

## Files

- `saverig.py`: reads the save's version and mod list from its header, builds
  the mod directory, starts and stops the server.
- `probes.py`: the checks, each a plain `(game) -> (ok, detail, data)`.
- `run.py`: the only entry point.
