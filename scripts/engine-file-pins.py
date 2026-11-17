#!/usr/bin/env python3
"""Derive/check per-file pins from the SHA256-pinned upstream archive."""
import argparse
import hashlib
from pathlib import Path
import re
import tarfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument("archive", type=Path)
parser.add_argument("--write", action="store_true")
args = parser.parse_args()
origin = (ROOT / "dependencies/nfqws2.env").read_bytes()
env = dict(re.findall(r"(?m)^(ENGINE_[A-Z_0-9]+)=(.+)$", origin.decode()))
if hashlib.sha256(args.archive.read_bytes()).hexdigest() != env["ENGINE_SHA256"]:
    raise SystemExit("Engine archive checksum mismatch; refusing to derive file pins")
arches = {"amd64": "linux-x86_64", "arm64": "linux-arm64", "arm": "linux-arm", "mips": "linux-mips", "mipsle": "linux-mipsel"}
with tarfile.open(args.archive) as archive:
    lua = {}
    for member in archive.getmembers():
        prefix = env["ENGINE_ROOT"] + "/lua/"
        if not member.name.startswith(prefix) or member.isdir():
            continue
        name = member.name[len(prefix):]
        if not member.isfile() or not re.fullmatch(r"[a-zA-Z0-9_.-]+", name):
            raise SystemExit("Unexpected upstream Lua archive layout")
        lua["lua/" + name] = archive.extractfile(member).read()
    if not lua:
        raise SystemExit("No upstream Lua files")
    for arch, upstream in arches.items():
        binary = archive.getmember(env["ENGINE_ROOT"] + "/binaries/" + upstream + "/nfqws2")
        if not binary.isfile():
            raise SystemExit("Expected a regular upstream binary")
        files = {**lua, "nfqws2": archive.extractfile(binary).read(), "ORIGIN": origin,
                 "VERSION": (env["ENGINE_VERSION"] + "\n").encode()}
        manifest = "".join(hashlib.sha256(data).hexdigest() + "  " + name + "\n" for name, data in sorted(files.items()))
        path = ROOT / "dependencies/nfqws2-files" / (arch + ".sha256")
        if args.write:
            path.parent.mkdir(exist_ok=True)
            path.write_text(manifest)
        elif not path.exists() or path.read_text() != manifest:
            raise SystemExit("Engine file pins differ from pinned archive: " + arch)
print("Verified engine file pins for all five architectures")
