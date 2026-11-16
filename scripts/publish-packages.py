#!/usr/bin/env python3
"""Publish immutable packages to a branch of THIS repository, without a worktree.

Git HTTPS access is sufficient; the installer needs neither GitHub API tokens nor
Actions artifact downloads. Existing published versions are never overwritten.
"""
import argparse
import hashlib
import json
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ARCHES = ("amd64", "arm", "arm64", "mips", "mipsle")


def git(*args, data=None, env=None):
    return subprocess.run(["git", *args], cwd=ROOT, input=data, env=env,
                          check=True, capture_output=True).stdout.decode().strip()


def prepare(update_installer=False):
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+-[a-z]+\.[0-9]+", version):
        raise RuntimeError("unexpected release version")
    files = {}
    for arch in ARCHES:
        name = f"keenetic-pilot-{version}-linux-{arch}.tar.gz"
        files[name] = (ROOT / "dist" / name).read_bytes()
    manifest = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in sorted(files.items())).encode()
    files["SHA256SUMS"] = manifest
    script = (ROOT / "install.sh").read_text()
    script = re.sub(r"(?m)^PACKAGE_VERSION=.*$", "PACKAGE_VERSION=" + version, script)
    script = re.sub(r"(?m)^MANIFEST_SHA256=.*$", "MANIFEST_SHA256=" + hashlib.sha256(manifest).hexdigest(), script)
    installer = ROOT / ".cache" / "installer" / "install.sh"
    installer.parent.mkdir(parents=True, exist_ok=True)
    installer.write_text(script)
    if update_installer:
        (ROOT / "install.sh").write_text(script)
    out = ROOT / ".cache" / "published-manifest"
    out.mkdir(parents=True, exist_ok=True)
    (out / "SHA256SUMS").write_bytes(manifest)
    return version, files


def publish(version, files):
    remote = git("ls-remote", "origin", "refs/heads/packages")
    parent = None
    if remote:
        git("fetch", "origin", "refs/heads/packages:refs/remotes/origin/packages")
        parent = git("rev-parse", "refs/remotes/origin/packages")
        existing = subprocess.run(["git", "show", f"{parent}:v{version}/SHA256SUMS"], cwd=ROOT, capture_output=True)
        if existing.returncode == 0:
            if existing.stdout != files["SHA256SUMS"]:
                raise RuntimeError("This version is already published with different bytes. Bump VERSION; do not overwrite it.")
            print("Version is already published with the same checksums.")
            return
    with tempfile.TemporaryDirectory(prefix="pilot-index-") as folder:
        env = {**os.environ, "GIT_INDEX_FILE": str(Path(folder) / "index")}
        git("read-tree", parent or "--empty", env=env)
        for name, data in files.items():
            blob = git("hash-object", "-w", "--stdin", data=data)
            git("update-index", "--add", "--cacheinfo", f"100644,{blob},v{version}/{name}", env=env)
        # Publish a signed latest pointer in the same fast-forward as the archives.
        key = ROOT / "local" / "updates-signing.pem"
        if not key.exists():
            raise RuntimeError("Restore local/updates-signing.pem before publishing an update")
        dep = (ROOT / "dependencies/nfqws2.env").read_text()
        latest = {
            "kind": "pilot-release", "version": version,
            "manifest_sha256": hashlib.sha256(files["SHA256SUMS"]).hexdigest(),
            "published_at": datetime.now(timezone.utc).isoformat(),
            "engine_version": re.search(r"(?m)^ENGINE_VERSION=(.*)$", dep)[1],
            "engine_sha256": re.search(r"(?m)^ENGINE_SHA256=(.*)$", dep)[1],
        }
        doc = ROOT / ".cache" / "latest.json"
        doc.write_text(json.dumps(latest, separators=(",", ":")) + "\n")
        subprocess.run(["python3", str(ROOT / "scripts/sign-updates.py"), str(doc)], check=True)
        for name, data in {"latest.json": doc.read_bytes(), "latest.json.sig": Path(str(doc)+".sig").read_bytes()}.items():
            blob = git("hash-object", "-w", "--stdin", data=data)
            git("update-index", "--add", "--cacheinfo", f"100644,{blob},{name}", env=env)
        tree = git("write-tree", env=env)
        args = ["commit-tree", tree]
        if parent:
            args += ["-p", parent]
        commit = git(*args, data=f"Publish verified Keenetic Pilot {version} packages\n".encode())
        git("push", "origin", f"{commit}:refs/heads/packages")
        print(f"Published {version}: {commit}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--push", action="store_true", help="publish packages in this repository's packages branch")
    parser.add_argument("--update-installer", action="store_true", help="update the tracked entry point after validation")
    args = parser.parse_args()
    version, files = prepare(args.update_installer)
    print(f"Prepared pinned installer for {version}")
    if args.push:
        publish(version, files)
