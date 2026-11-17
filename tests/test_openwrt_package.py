#!/usr/bin/env python3
"""Static package and isolation checks. No root, nft, or router needed."""
import io
from pathlib import Path
import tarfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / "VERSION").read_text().strip()
ARCHES = ("amd64", "arm", "arm64", "mips", "mipsle")


class OpenWrtPackageTests(unittest.TestCase):
    def test_keeps_original_entware_installer(self):
        script = (ROOT / "scripts/install.sh").read_text()
        self.assertIn("kmod_ndms", script)
        self.assertIn("S98keenetic-pilot", script)
        new = (ROOT / "scripts/install-openwrt.sh").read_text()
        self.assertNotIn("kmod_ndms", new)
        self.assertNotIn("S98keenetic-pilot", new)
        self.assertNotIn("095-keenetic-pilot.sh", new)
        self.assertIn("sha256sum -c SHA256SUMS", new)
        self.assertIn("scripts/fetch-engine.sh", new)
        self.assertIn("--mode openwrt", new)
        self.assertIn("--doctor", new)
        self.assertIn("OLD_STOPPED=0", new)
        self.assertIn("SWAPPED=0", new)

    def test_procd_is_isolated(self):
        init = (ROOT / "packaging/keenetic-pilot.openwrt").read_text()
        self.assertIn("/etc/rc.common", init)
        self.assertIn("USE_PROCD=1", init)
        self.assertIn("--mode openwrt", init)
        self.assertIn("procd_set_param command", init)
        self.assertNotIn("/opt/", init)
        self.assertNotIn("iptables", init)
        self.assertNotIn("fw4 reload", init)

    def test_every_package_contains_both_platforms(self):
        for arch in ARCHES:
            path = ROOT / "dist" / f"keenetic-pilot-{VERSION}-linux-{arch}.tar.gz"
            self.assertTrue(path.is_file(), f"Missing {path}")
            with tarfile.open(path) as tar:
                items = set(tar.getnames())
                for name in (
                    "keenetic-pilot/scripts/install.sh",
                    "keenetic-pilot/scripts/install-openwrt.sh",
                    "keenetic-pilot/scripts/uninstall-openwrt.sh",
                    "keenetic-pilot/packaging/S98keenetic-pilot",
                    "keenetic-pilot/packaging/keenetic-pilot.openwrt",
                    "keenetic-pilot/PLATFORM",
                    "keenetic-pilot/SHA256SUMS",
                    "keenetic-pilot/bin/pilot",
                ):
                    self.assertIn(name, items)
                p = tar.extractfile("keenetic-pilot/PLATFORM").read().decode().strip()
                self.assertEqual(p, arch)


if __name__ == "__main__":
    unittest.main()
