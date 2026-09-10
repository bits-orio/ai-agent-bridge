import ctypes, sys, os

LUA_SO = "/usr/lib/x86_64-linux-gnu/liblua5.4.so.0"
lua = ctypes.CDLL(LUA_SO)

lua.luaL_newstate.restype = ctypes.c_void_p
lua.luaL_loadfilex.restype = ctypes.c_int
lua.luaL_loadfilex.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p]
lua.lua_tolstring.restype = ctypes.c_char_p
lua.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p]
lua.lua_close.argtypes = [ctypes.c_void_p]

def check(path):
    L = lua.luaL_newstate()
    if not L:
        raise RuntimeError("luaL_newstate failed")
    rc = lua.luaL_loadfilex(L, path.encode(), None)
    msg = None
    if rc != 0:
        msg = lua.lua_tolstring(L, -1, None)
        msg = msg.decode('utf-8', 'replace') if msg else "<no message>"
    lua.lua_close(L)
    return rc, msg

if __name__ == "__main__":
    failures = 0
    for path in sys.argv[1:]:
        rc, msg = check(path)
        if rc == 0:
            print(f"OK   {path}")
        else:
            failures += 1
            print(f"FAIL {path}: rc={rc} {msg}")
    sys.exit(1 if failures else 0)
