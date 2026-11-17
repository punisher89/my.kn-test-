#!/usr/bin/env python3
"""Offline checks of the new sidecar SSH release publisher."""
import hashlib
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("publish_ssh", ROOT / "scripts/publish-ssh-release.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)


class SSHInstallerTests(unittest.TestCase):
    def test_release_sources_labeled_and_pinned(self):
        marker = hashlib.sha256(b"test-pilot-SHA256SUMS").hexdigest()
        for source in ("ChatGPT", "Codex"):
            script = mod.make_installer("0.2.0-alpha.18", source, marker)
            self.assertIn("Keenetic Pilot 0.2.0-alpha.18 (" + source + ")", script)
            self.assertIn(marker, script)
            self.assertIn("refs/heads/ssh-releases/v0.2.0-alpha.18", script)
            self.assertIn("sha256sum -c", script)
            self.assertIn("--skip-packages", script)
            self.assertIn('PACKAGE_VERSION', (ROOT / "install.sh").read_text())
            self.assertNotIn("sign-updates.py", script)
            self.assertNotIn("latest.json", script)
            self.assertNotIn("packages/latest.json", script)

    def test_openwrt_installer_is_pinned_and_never_calls_entware(self):
        marker = hashlib.sha256(b"openwrt-test-manifest").hexdigest()
        for source in ("ChatGPT", "Codex"):
            script = mod.make_openwrt_installer("0.2.0-alpha.22", source, marker)
            self.assertIn("install-openwrt.sh", script)
            self.assertIn(marker, script)
            self.assertIn("/etc/openwrt_release", script)
            self.assertIn("sha256sum -c", script)
            self.assertIn("refs/heads/ssh-releases/v0.2.0-alpha.22", script)
            self.assertNotIn("--skip-packages", script)
            self.assertNotIn("S98keenetic-pilot", script)
            self.assertNotIn("PREFIX=/opt", script)

    def test_publisher_metadata_contains_pinned_engine_and_source(self):
        version, files, info = mod.package_files("ChatGPT")
        self.assertEqual(version, (ROOT / "VERSION").read_text().strip())
        self.assertEqual(info["source"], "ChatGPT")
        self.assertEqual(info["channel"], "ssh")
        self.assertTrue(info["engine_version"])
        self.assertEqual(len(info["engine_sha256"]), 64)
        self.assertEqual(files["SOURCE.txt"], b"ChatGPT\n")
        self.assertEqual(info["manifest_sha256"], hashlib.sha256(files["SHA256SUMS"]).hexdigest())
        self.assertIn(f"install-chatgpt.sh", files)
        self.assertIn("install-openwrt-chatgpt.sh", files)
        self.assertTrue(files["install-openwrt-chatgpt.sh"].startswith(b"#!/bin/sh"))
        self.assertEqual(info["installer_sha256"], hashlib.sha256(files["install-chatgpt.sh"]).hexdigest())

    def test_no_implicit_source(self):
        with self.assertRaises(RuntimeError):
            mod.package_files("Unknown")

    def test_release_publisher_does_not_touch_signed_channel(self):
        script = (ROOT / "scripts/publish-ssh-release.py").read_text()
        self.assertIn('CHANNEL = "ssh-releases"', script)
        self.assertNotIn('refs/heads/packages"', script)
        self.assertNotIn('git("push", "origin", "HEAD:refs/heads/packages"', script)


if __name__ == "__main__":
    unittest.main()
