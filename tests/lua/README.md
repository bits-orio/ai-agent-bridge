# Lua tests without Factorio

`fakegame.lua` is a stub of the control-stage API surface the companion uses
(game, script, remote, settings, helpers, rcon, prototypes, storage). Every
`aab_*test.lua` file loads the real `companion-mod/control.lua` on top of it
and drives the `aab-rpc` protocol the way the service does; `make lua-test`
runs them all.

    python3 tests/lua/luarun.py tests/lua/aab_test.lua
    make lua-test

`aab_answer_test.lua` reads the answer caps out of the service's own Go
source (`service/internal/agent/artifact.go` and `budget.go`) and holds the
renderer to them, so a cap changed on one side only turns it red.

`aab_map_id_test.lua` loads the companion more than once over the same
`storage`, the way loading a save starts a new Lua state, so the session
event and the map id can be held across a load, and the `status` op's
`map_id` held to the same id.

`luarun.py` needs liblua5.4 (`apt install liblua5.4-0` on Debian and Ubuntu);
no lua binary is required. `luaparse.py <file>` is a parse-only check for one
file. These suites assert the call shapes and the protocol, not the engine's
answers; the rig harness in `tests/e2e` covers those.
