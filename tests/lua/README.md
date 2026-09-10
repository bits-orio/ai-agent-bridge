# Lua tests without Factorio

`fakegame.lua` is a stub of the control-stage API surface the companion uses
(game, script, remote, settings, helpers, rcon, prototypes, storage). The two
test files load the real `companion-mod/control.lua` on top of it and drive the
`aab-rpc` protocol the way the service does.

    python3 tests/lua/luarun.py tests/lua/aab_test.lua
    python3 tests/lua/luarun.py tests/lua/aab_breadth_test.lua
    make lua-test

`luarun.py` needs liblua5.4 (`apt install liblua5.4-0` on Debian and Ubuntu);
no lua binary is required. `luaparse.py <file>` is a parse-only check for one
file. These suites assert the call shapes and the protocol, not the engine's
answers; the rig harness in `tests/e2e` covers those.
