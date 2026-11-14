#!/usr/bin/env python3
"""Run controller tests on desktop Lua and the router's LuaJIT / Lua 5.1 API."""
import ctypes
import ctypes.util
import os
from pathlib import Path
import sys

libraries = [
    ("Lua 5.3/5.4", ctypes.util.find_library("lua5.4") or ctypes.util.find_library("lua5.3")),
    ("LuaJIT / Lua 5.1", os.environ.get("PILOT_LUAJIT_LIBRARY") or ctypes.util.find_library("luajit-5.1")),
]
if any(not name for _, name in libraries):
    raise SystemExit("Tests require liblua5.4-0 (or liblua5.3-0) AND libluajit-5.1-2")

program = Path(sys.argv[1]).read_bytes()
runtime = Path(sys.argv[2])
# Each VM must start with the same persisted state, including migration fixtures.
original = {name: (runtime / name).read_bytes()
            for name in ("state0.tsv", "state1.tsv", "autohosts.txt")}
for label, name in libraries:
    for filename, data in original.items():
        (runtime / filename).write_bytes(data)
    lib = ctypes.CDLL(name)
    lib.luaL_newstate.restype = ctypes.c_void_p
    lib.luaL_openlibs.argtypes = [ctypes.c_void_p]
    lib.luaL_loadstring.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
    lib.luaL_loadstring.restype = ctypes.c_int
    lib.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.POINTER(ctypes.c_size_t)]
    lib.lua_tolstring.restype = ctypes.c_void_p
    lib.lua_close.argtypes = [ctypes.c_void_p]
    modern = hasattr(lib, "lua_pcallk")
    if modern:
        lib.lua_pcallk.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int,
                                  ctypes.c_longlong, ctypes.c_void_p]
        lib.lua_pcallk.restype = ctypes.c_int
    else:
        lib.lua_pcall.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int]
        lib.lua_pcall.restype = ctypes.c_int
    state = lib.luaL_newstate()
    if not state:
        raise SystemExit(f"{label}: cannot create Lua state")
    lib.luaL_openlibs(state)
    try:
        # Some LuaJIT packages expose optional 5.2 extensions. Enforce the
        # baseline 5.1 API seen on the actual Keenetic build as well.
        prefix = b"assert(_VERSION=='Lua 5.1' and jit);table.unpack=nil\n" if not modern else b""
        status = lib.luaL_loadstring(state, prefix + program)
        if status == 0:
            status = lib.lua_pcallk(state, 0, -1, 0, 0, None) if modern else lib.lua_pcall(state, 0, -1, 0)
        if status:
            size = ctypes.c_size_t()
            message = lib.lua_tolstring(state, -1, ctypes.byref(size))
            raise SystemExit(f"{label}: " + ctypes.string_at(message, size.value).decode("utf8", "replace"))
    finally:
        lib.lua_close(state)
