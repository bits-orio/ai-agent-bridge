#!/usr/bin/env python3
"""Run one Lua test file against the companion mod without Factorio.

Drives liblua5.4 through ctypes (no lua binary or luac needed) and defines two
globals before the file runs: TEST_DIR (this directory, where fakegame.lua
lives) and REPO_DIR (the repository root). usage: luarun.py <test.lua>
"""
import ctypes, os, sys

CANDIDATES = [
    os.environ.get("LUA_SO", ""),
    "/usr/lib/x86_64-linux-gnu/liblua5.4.so.0",
    "/usr/lib/liblua5.4.so.0",
    "/usr/lib64/liblua5.4.so.0",
    "/usr/local/lib/liblua5.4.so",
]
for path in CANDIDATES:
    if path and os.path.exists(path):
        LUA_SO = path
        break
else:
    sys.exit("luarun.py: no liblua5.4 shared library found; set LUA_SO")

lua = ctypes.CDLL(LUA_SO)
lua.luaL_newstate.restype = ctypes.c_void_p
lua.luaL_openlibs.argtypes = [ctypes.c_void_p]
lua.luaL_loadfilex.restype = ctypes.c_int
lua.luaL_loadfilex.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p]
lua.luaL_loadstring.restype = ctypes.c_int
lua.luaL_loadstring.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
lua.lua_pcallk.restype = ctypes.c_int
lua.lua_pcallk.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int,
                           ctypes.c_longlong, ctypes.c_void_p]
lua.lua_tolstring.restype = ctypes.c_char_p
lua.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p]
lua.lua_close.argtypes = [ctypes.c_void_p]

def fail(L, what):
    msg = lua.lua_tolstring(L, -1, None)
    print(what, msg.decode() if msg else "?")
    sys.exit(1)

test_dir = os.path.dirname(os.path.abspath(__file__))
repo_dir = os.path.dirname(os.path.dirname(test_dir))
L = lua.luaL_newstate()
lua.luaL_openlibs(L)
prelude = 'TEST_DIR = %r; REPO_DIR = %r' % (test_dir + "/", repo_dir + "/")
if lua.luaL_loadstring(L, prelude.encode()) != 0 or lua.lua_pcallk(L, 0, 0, 0, 0, None) != 0:
    fail(L, "PRELUDE FAIL:")
if lua.luaL_loadfilex(L, sys.argv[1].encode(), None) != 0:
    fail(L, "LOAD FAIL:")
rc = lua.lua_pcallk(L, 0, -1, 0, 0, None)
sys.stdout.flush()
if rc != 0:
    fail(L, "RUN FAIL:")
print("RUN OK")
