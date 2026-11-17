#!/usr/bin/env python3
"""Publish an independent, checksum-pinned SSH release.

This channel NEVER changes packages/latest.json, its Ed25519 signature,
the existing panel updater, or the original install.sh.
"""
import argparse
import hashlib
import json
import os
from datetime import datetime, timezone
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
REPO = "punisher89/my.kn-test-"
CHANNEL = "ssh-releases"
ARCHES = ("amd64", "arm", "arm64", "mips", "mipsle")
SOURCES = ("ChatGPT", "Codex")


def git(*args, data=None, env=None):
    return subprocess.run(
        ["git", *args], cwd=ROOT, input=data, env=env,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
    ).stdout.decode().strip()


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def make_installer(version, source, manifest_hash):
    remote = f"https://raw.githubusercontent.com/{REPO}/refs/heads/{CHANNEL}/v{version}"
    label = f"Keenetic Pilot {version} ({source})"
    # The content-addressed SHA256 manifest is baked in: the remote archive
    # cannot be changed without failing the installer's verification.
    return f"""#!/bin/sh
# {label}
# Independent one-off SSH installation. NOT the signed panel updater.
set -eu
VERSION='{version}'
SOURCE='{source}'
MANIFEST_SHA256='{manifest_hash}'
BASE_URL='{remote}'
PREFIX=/opt
printf '%s\\n' 'Installing {label} over SSH (existing panel updater unchanged)'
[ "$(id -u)" -eq 0 ] || {{ echo "Connect to Entware SSH as root" >&2; exit 1; }}
command -v opkg >/dev/null || {{ echo "Entware opkg is required" >&2; exit 1; }}
for tool in curl sha256sum tar awk mktemp; do
    command -v "$tool" >/dev/null || {{ echo "Missing required executable: $tool" >&2; exit 1; }}
done
case "$(uname -m)" in
    aarch64|arm64) ARCH=arm64;;
    armv7*|armv8l) ARCH=arm;;
    x86_64) ARCH=amd64;;
    mipsel*) ARCH=mipsle;;
    mips*) if opkg print-architecture | grep -q mipsel; then ARCH=mipsle; else ARCH=mips; fi;;
    *) echo "Unsupported router architecture" >&2; exit 1;;
esac
FILE="keenetic-pilot-$VERSION-linux-$ARCH.tar.gz"
STAGE=$(mktemp -d "$PREFIX/.pilot-ssh.XXXXXX")
chmod 0700 "$STAGE"
cleanup() {{ rm -rf "$STAGE"; }}
trap cleanup EXIT HUP INT TERM
download() {{ curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --show-error --location --retry 2 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }}
download "$BASE_URL/SHA256SUMS" "$STAGE/SHA256SUMS"
printf '%s  %s\\n' "$MANIFEST_SHA256" "$STAGE/SHA256SUMS" | sha256sum -c -
awk -v name="$FILE" '$2==name || $2=="./"name {{ print; count++ }} END {{ if (count!=1) exit 1 }}' "$STAGE/SHA256SUMS" > "$STAGE/selected.sha256"
download "$BASE_URL/$FILE" "$STAGE/$FILE"
(cd "$STAGE" && sha256sum -c selected.sha256)
# The SHA-256 verified archive is built by Pilot's cross-platform CI.
tar -xzf "$STAGE/$FILE" -C "$STAGE"
[ -f "$STAGE/keenetic-pilot/scripts/install.sh" ] || {{ echo "Package is missing its installer" >&2; exit 1; }}
# The stock installer checks the package's internal SHA256SUMS, platform,
# pinned nfqws2 engine, existing configuration and backups before swapping.
# Skip opkg changes on an already installed Entware/Pilot router.
sh "$STAGE/keenetic-pilot/scripts/install.sh" --prefix "$PREFIX" --skip-packages
printf '%s\\n' 'Installed {label}. Original signed panel updater is unchanged.'
"""


def package_files(source):
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+-alpha\.\d+", version):
        raise RuntimeError("Expected numeric alpha VERSION")
    if source not in SOURCES:
        raise RuntimeError("Source must be ChatGPT or Codex")
    archives = {}
    for arch in ARCHES:
        name = f"keenetic-pilot-{version}-linux-{arch}.tar.gz"
        archives[name] = (ROOT / "dist" / name).read_bytes()
    manifest = "".join(
        f"{sha256(raw)}  {name}\n" for name, raw in sorted(archives.items())
    ).encode()
    installer = make_installer(version, source, sha256(manifest)).encode()
    engine = {}
    for line in (ROOT / "dependencies" / "nfqws2.env").read_text().splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            engine[key] = value.strip().strip("'\\\"")
    if not engine.get("ENGINE_VERSION") or not re.fullmatch(r"[a-f0-9]{64}", engine.get("ENGINE_SHA256", "")):
        raise RuntimeError("Missing pinned nfqws2 engine in release metadata")
    info = {
        "kind": "pilot-ssh-release",
        "version": version,
        "source": source,
        "channel": "ssh",
        "engine_version": engine["ENGINE_VERSION"],
        "engine_sha256": engine["ENGINE_SHA256"],
        "git_sha": git("rev-parse", "HEAD"),
        "published_at": datetime.now(timezone.utc).isoformat(),
        "manifest_sha256": sha256(manifest),
        "installer_sha256": sha256(installer),
        "installed_via": "ssh",
        "signed_panel_updater_modified": False,
    }
    files = {
        **archives,
        "SHA256SUMS": manifest,
        f"install-{source.lower()}.sh": installer,
        "release-info.json": (json.dumps(info, indent=2, ensure_ascii=False) + "\n").encode(),
        "SOURCE.txt": (source + "\n").encode(),
    }
    return version, files, info


def publish(version, files):
    files = dict(files)
    info = json.loads(files["release-info.json"])
    # A small per-source discovery pointer is independent of signed packages/latest.json.
    # This lives exclusively in the separate ssh-releases branch.
    files[f"latest-{info['source'].lower()}.json"] = files["release-info.json"]
    remote = git("ls-remote", "origin", f"refs/heads/{CHANNEL}")
    parent = None
    if remote:
        parent = git("rev-parse", "HEAD") if False else None
        git("fetch", "origin", f"refs/heads/{CHANNEL}:refs/remotes/origin/{CHANNEL}")
        parent = git("rev-parse", f"refs/remotes/origin/{CHANNEL}")
        if subprocess.run(
            ["git", "cat-file", "-e", f"{parent}:v{version}/SHA256SUMS"],
            cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        ).returncode == 0:
            raise RuntimeError(f"v{version} already exists in {CHANNEL}; bump VERSION instead of overwriting")
    with tempfile.TemporaryDirectory(prefix="pilot-ssh-index-") as tempdir:
        env = {**os.environ, "GIT_INDEX_FILE": str(Path(tempdir) / "index")}
        git("read-tree", parent or "--empty", env=env)
        for name, data in files.items():
            sha = git("hash-object", "-w", "--stdin", data=data)
            path = name if name.startswith("latest-") and name.endswith(".json") else f"v{version}/{name}"
            git("update-index", "--add", "--cacheinfo",
                f"100644,{sha},{path}", env=env)
        tree = git("write-tree", env=env)
        commit = git(
            *(["commit-tree", tree] + (["-p", parent] if parent else [])),
            data=f"SSH release {version} [{files['SOURCE.txt'].decode().strip()}]\n".encode(),
        )
        git("push", "origin", f"{commit}:refs/heads/{CHANNEL}")
    print("PUBLISHED SSH RELEASE:", version, files["SOURCE.txt"].decode().strip())
    print("Published commit:", commit)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", choices=SOURCES, default="ChatGPT")
    parser.add_argument("--push", action="store_true")
    args = parser.parse_args()
    v, payload, meta = package_files(args.source)
    print(json.dumps(meta, indent=2, ensure_ascii=False))
    if args.push:
        publish(v, payload)
