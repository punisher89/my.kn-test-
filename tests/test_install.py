#!/usr/bin/env python3
"""Exercise actual packaged controller/service in a temporary non-system prefix."""
import os
from pathlib import Path
import socket
import subprocess
import tarfile
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


class Installation(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.archive = os.environ.get("PILOT_ENGINE_ARCHIVE")
        if not cls.archive:
            raise RuntimeError("PILOT_ENGINE_ARCHIVE must name the checksum-pinned upstream archive")
        cls.package = ROOT / "dist" / f"keenetic-pilot-{(ROOT / 'VERSION').read_text().strip()}-linux-amd64.tar.gz"
        if not cls.package.is_file():
            raise RuntimeError("Build ARCHES=amd64 first")

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="pilot-it-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        with tarfile.open(self.package) as archive:
            archive.extractall(self.base, filter="data")
        self.package_dir = self.base / "keenetic-pilot"
        self.prefix = self.base / "opt"
        self.env = {**os.environ, "PILOT_ENGINE_ARCHIVE": self.archive}
        self.init = self.prefix / "etc/init.d/S98keenetic-pilot"
        self.binary = self.prefix / "keenetic-pilot/bin/pilot"
        self.config = self.prefix / "etc/keenetic-pilot/config.json"
        self.token_path = self.config.parent / "access-token"
        # Reserve no special system ports and never enable packet interception.
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            self.port = s.getsockname()[1]
        self.install()
        self.addCleanup(self.stop)

    def run_cmd(self, args, *, ok=True, env=None):
        result = subprocess.run([str(a) for a in args], env=env or self.env,
                                capture_output=True, text=True, timeout=60)
        if ok and result.returncode:
            self.fail(f"{args[0]} failed ({result.returncode}): {result.stdout}\n{result.stderr}")
        return result

    def install(self, *, ok=True, env=None):
        return self.run_cmd(["sh", self.package_dir / "scripts/install.sh", "--prefix", self.prefix,
                             "--wan", "lo", "--listen", f"127.0.0.1:{self.port}"], ok=ok, env=env)

    def stop(self):
        if self.init.exists():
            subprocess.run([str(self.init), "stop"], capture_output=True, timeout=40)

    def health(self):
        import json
        return json.loads(self.run_cmd([self.binary, "--config", self.config, "--health"]).stdout)

    def test_install_update_hooks_and_uninstall_preserve_state(self):
        token = self.token_path.read_bytes()
        config = self.config.read_bytes()
        status = self.health()
        self.assertEqual(status["mode"], "router")
        self.assertFalse(status["running"])
        self.assertFalse(status["config"]["enabled"])
        self.assertEqual(self.token_path.stat().st_mode & 0o777, 0o600)
        second = self.run_cmd([self.binary, "--config", self.config], ok=False)
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("уже запущен", second.stderr)
        hook = self.prefix / "etc/ndm/netfilter.d/095-keenetic-pilot.sh"
        for _ in range(10):
            self.run_cmd([hook], env={**self.env, "table": "mangle"})
        lock = self.prefix / "var/run/keenetic-pilot-repair.lock"
        deadline = time.monotonic() + 5
        while lock.exists() and time.monotonic() < deadline:
            time.sleep(0.05)
        self.assertFalse(lock.exists(), "repair hook left a lock behind")
        output = self.install().stdout
        self.assertNotIn(token.decode().strip(), output)
        self.assertEqual(token, self.token_path.read_bytes())
        self.assertEqual(config, self.config.read_bytes())
        self.assertTrue(list(self.prefix.glob("keenetic-pilot.backup.*")))
        self.assertFalse(self.health()["running"])
        self.run_cmd([self.prefix / "keenetic-pilot/scripts/uninstall.sh"],
                     env={**self.env, "PILOT_PREFIX": str(self.prefix)})
        self.assertFalse(self.binary.exists())
        self.assertFalse(hook.exists())
        self.assertEqual(token, self.token_path.read_bytes())

    def test_bad_engine_checksum_leaves_running_installation_untouched(self):
        corrupt = self.base / "corrupt.tar.gz"
        corrupt.write_bytes(b"not the pinned engine")
        binary_before = self.binary.read_bytes()
        token_before = self.token_path.read_bytes()
        result = self.install(ok=False, env={**self.env, "PILOT_ENGINE_ARCHIVE": str(corrupt)})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary_before, self.binary.read_bytes())
        self.assertEqual(token_before, self.token_path.read_bytes())
        self.assertEqual(self.health()["mode"], "router")
        self.assertFalse(list(self.prefix.glob("keenetic-pilot.backup.*")))

    def test_modified_controller_package_is_rejected_before_update(self):
        (self.package_dir / "README.md").write_text("modified after packaging")
        token_before = self.token_path.read_bytes()
        result = self.install(ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(token_before, self.token_path.read_bytes())
        self.assertEqual(self.health()["mode"], "router")
        self.assertFalse(list(self.prefix.glob("keenetic-pilot.backup.*")))

    def test_failure_after_swap_restores_old_application_and_service(self):
        fake_bin = self.base / "fault-injection"
        fake_bin.mkdir()
        sed = fake_bin / "sed"
        sed.write_text("#!/bin/sh\nexit 71\n")
        sed.chmod(0o755)
        binary_before = self.binary.read_bytes()
        init_before = self.init.read_bytes()
        result = self.install(ok=False, env={**self.env, "PATH": str(fake_bin) + ":" + os.environ["PATH"]})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary_before, self.binary.read_bytes())
        self.assertEqual(init_before, self.init.read_bytes())
        self.assertEqual(self.health()["mode"], "router")


if __name__ == "__main__":
    unittest.main(verbosity=2)
