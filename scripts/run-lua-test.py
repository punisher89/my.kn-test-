#!/usr/bin/env python3
"""Execute Lua unit tests without network/kernel privileges; only used in tests."""
import ctypes
import ctypes.util
from pathlib import Path
import sys

name = ctypes.util.find_library("lua5.4") or ctypes.util.find_library("lua5.3")
if not name:
    raise SystemExit("Tests require liblua5.4-0 (or liblua5.3-0)")
lib = ctypes.CDLL(name)
lib.luaL_newstate.restype = ctypes.c_void_p
lib.luaL_openlibs.argtypes = [ctypes.c_void_p]
lib.luaL_loadstring.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
lib.luaL_loadstring.restype = ctypes.c_int
lib.lua_pcallk.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int,
                          ctypes.c_longlong, ctypes.c_void_p]
lib.lua_pcallk.restype = ctypes.c_int
lib.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.POINTER(ctypes.c_size_t)]
lib.lua_tolstring.restype = ctypes.c_void_p
lib.lua_close.argtypes = [ctypes.c_void_p]
state = lib.luaL_newstate()
if not state:
    raise SystemExit("Cannot create Lua state")
lib.luaL_openlibs(state)
try:
    status = lib.luaL_loadstring(state, Path(sys.argv[1]).read_bytes())
    if status == 0:
        status = lib.lua_pcallk(state, 0, -1, 0, 0, None)
    if status:
        size = ctypes.c_size_t()
        message = lib.lua_tolstring(state, -1, ctypes.byref(size))
        raise SystemExit(ctypes.string_at(message, size.value).decode("utf8", "replace"))
finally:
    lib.lua_close(state)
