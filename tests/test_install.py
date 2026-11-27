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
import re
import json
import http.cookiejar
import urllib.request
import urllib.error
import shutil
import stat
import signal

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
        installed = self.install()
        self.pairing_code = re.search(r"Код первого входа \(15 минут\): ([0-9]{8})", installed.stdout).group(1)
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
        uninstall = [self.prefix / "keenetic-pilot/scripts/uninstall.sh"]
        uninstall_env = {**self.env, "PILOT_PREFIX": str(self.prefix)}
        tunnel_marker = self.config.parent / "pilotwg0.active"
        tunnel_marker.touch()
        blocked = self.run_cmd(uninstall, ok=False, env=uninstall_env)
        self.assertNotEqual(blocked.returncode, 0)
        self.assertIn("Stop the Telegram tunnel", blocked.stderr)
        self.assertTrue(tunnel_marker.is_file())
        self.assertTrue(self.binary.is_file())
        self.assertFalse(self.health()["running"])
        tunnel_marker.unlink()
        self.run_cmd(uninstall, env=uninstall_env)
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

    def test_verified_engine_is_reused_and_only_previous_managed_backup_remains(self):
        fake_bin = self.base / "offline-tools"
        fake_bin.mkdir()
        curl = fake_bin / "curl"
        curl.write_text("#!/bin/sh\nexit 91\n")
        curl.chmod(0o755)
        env = {**self.env, "PATH": str(fake_bin) + ":" + os.environ["PATH"]}
        env.pop("PILOT_ENGINE_ARCHIVE", None)
        older = self.prefix / "keenetic-pilot.backup.100.1"
        older.mkdir()
        (older / ".pilot-managed").write_text("keenetic-pilot\n")
        unowned = self.prefix / "keenetic-pilot.backup.unowned"
        unowned.mkdir()
        linked = self.prefix / "keenetic-pilot.backup.linked"
        linked.symlink_to(older, target_is_directory=True)
        config, token = self.config.read_bytes(), self.token_path.read_bytes()
        result = self.install(env=env)
        self.assertIn("Verified installed engine reused", result.stdout)
        self.assertIn("download skipped", result.stdout)
        self.assertFalse(older.exists())
        self.assertTrue(unowned.is_dir())
        self.assertTrue(linked.is_symlink())
        backups = [p for p in self.prefix.glob("keenetic-pilot.backup.*") if p.is_dir() and not p.is_symlink() and (p / ".pilot-managed").is_file()]
        self.assertEqual(len(backups), 1)
        self.assertEqual(config, self.config.read_bytes())
        self.assertEqual(token, self.token_path.read_bytes())
        self.assertEqual(self.health()["mode"], "router")

    def test_reuse_rejects_tampered_engine_even_with_rewritten_local_manifest(self):
        import hashlib
        engine = self.prefix / "keenetic-pilot/engine"
        with (engine / "nfqws2").open("ab") as output:
            output.write(b"tampered-engine")
        manifest = "".join(hashlib.sha256((engine / name).read_bytes()).hexdigest() + "  " + name + "\n"
                           for name in ["nfqws2", "VERSION", "ORIGIN"])
        (engine / "SHA256SUMS").write_text(manifest)
        fake_bin = self.base / "offline-tools"
        fake_bin.mkdir()
        curl = fake_bin / "curl"
        curl.write_text("#!/bin/sh\nexit 91\n")
        curl.chmod(0o755)
        env = {**self.env, "PATH": str(fake_bin) + ":" + os.environ["PATH"]}
        env.pop("PILOT_ENGINE_ARCHIVE", None)
        before = self.binary.read_bytes()
        result = self.install(ok=False, env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match trusted file pins", result.stdout)
        self.assertIn("curl exit 91", result.stderr)
        self.assertIn("engine-prepare", result.stderr)
        self.assertEqual(before, self.binary.read_bytes())
        self.assertEqual(self.health()["mode"], "router")

    def test_no_start_preserves_older_backup_until_service_is_verified(self):
        older = self.prefix / "keenetic-pilot.backup.100.1"
        older.mkdir()
        (older / ".pilot-managed").write_text("keenetic-pilot\n")
        self.run_cmd(["sh", self.package_dir / "scripts/install.sh", "--prefix", self.prefix, "--no-start"])
        self.assertTrue(older.is_dir())
        self.assertEqual(len(list(self.prefix.glob("keenetic-pilot.backup.*"))), 2)
        self.run_cmd([self.init, "start"])
        self.assertEqual(self.health()["mode"], "router")

    def test_static_comparison_is_reversible_and_preserves_saved_strategies(self):
        settings = json.loads(self.config.read_text())
        settings["adaptive"] = True
        self.config.write_text(json.dumps(settings))
        self.run_cmd([self.init, "restart"])
        original = self.config.read_bytes()
        token = self.token_path.read_bytes()
        helper = ROOT / "scripts/test-static-mode.sh"
        self.run_cmd(["sh", helper, "--prefix", self.prefix])
        snapshot = json.loads(self.config.read_text())
        before = json.loads(original)
        self.assertFalse(snapshot["adaptive"])
        self.assertFalse(snapshot["auto"])
        snapshot["adaptive"], snapshot["auto"] = before["adaptive"], before["auto"]
        self.assertEqual(snapshot, before)
        self.assertEqual(self.token_path.read_bytes(), token)
        self.assertEqual(self.health()["mode"], "router")
        repeat = self.run_cmd(["sh", helper, "--prefix", self.prefix], ok=False)
        self.assertNotEqual(repeat.returncode, 0)
        self.run_cmd(["sh", helper, "--prefix", self.prefix, "--restore"])
        self.assertEqual(self.config.read_bytes(), original)
        self.assertEqual(self.token_path.read_bytes(), token)
        self.assertFalse((self.config.parent / "config.before-static-test.json").exists())
        self.assertEqual(self.health()["mode"], "router")

    def test_static_comparison_rolls_back_when_new_service_fails(self):
        settings = json.loads(self.config.read_text())
        settings["adaptive"] = True
        self.config.write_text(json.dumps(settings))
        original = self.config.read_bytes()
        real_init = self.init.with_name("S98keenetic-pilot-real")
        self.init.rename(real_init)
        self.init.write_text("#!/bin/sh\n# Keenetic Pilot test wrapper\n"
                             f"if [ \"$1\" = start ] && grep -q '\"adaptive\": false' '{self.config}'; then exit 71; fi\n"
                             f"exec '{real_init}' \"$@\"\n")
        self.init.chmod(0o755)
        helper = ROOT / "scripts/test-static-mode.sh"
        result = self.run_cmd(["sh", helper, "--prefix", self.prefix], ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("restoring the configuration", result.stderr)
        self.assertEqual(self.config.read_bytes(), original)
        self.assertEqual(self.health()["mode"], "router")
        self.run_cmd(["sh", helper, "--prefix", self.prefix, "--restore"])
        self.assertEqual(self.config.read_bytes(), original)
        self.assertFalse((self.config.parent / "config.before-static-test.json").exists())
        self.assertEqual(self.health()["mode"], "router")

    def test_service_does_not_require_nohup_and_survives_hangup(self):
        self.run_cmd([self.init,'stop'])
        bindir=self.prefix/'bin'
        bindir.mkdir(exist_ok=True)
        marker=self.base/'nohup-was-called'
        poison=bindir/'nohup'
        poison.write_text('#!/bin/sh\ntouch '+str(marker)+'\nexit 127\n')
        poison.chmod(0o755)
        self.run_cmd([self.init,'start'])
        self.assertFalse(marker.exists(),'service still invokes external nohup')
        pidfile=self.prefix/'var/run/keenetic-pilot.pid'
        pid=int(pidfile.read_text())
        os.kill(pid,signal.SIGHUP)
        time.sleep(0.1)
        self.assertEqual(self.health()['mode'],'router')
        self.assertEqual(int(pidfile.read_text()),pid)
        self.run_cmd([self.init,'restart'])
        self.assertFalse(marker.exists())
        self.assertEqual(self.health()['mode'],'router')

    def test_engine_resources_readable_after_privilege_drop(self):
        app=self.prefix/'keenetic-pilot'
        for directory in [app,app/'engine',app/'engine/lua',app/'lua']:
            self.assertTrue(directory.stat().st_mode & stat.S_IXOTH, f"missing traversal: {directory.name}")
        for lua in (app/'engine/lua').glob('*.gz'):
            self.assertTrue(lua.stat().st_mode & stat.S_IROTH, f"unreadable Lua: {lua.name}")
        self.assertTrue((app/'lua/pilot-auto.lua').stat().st_mode & stat.S_IROTH)
        self.assertEqual(self.config.parent.stat().st_mode & 0o777,0o700)
        self.assertEqual(self.token_path.stat().st_mode & 0o777,0o600)

    def test_root_installer_with_real_nfqws_privilege_drop(self):
        if not shutil.which('sudo') or subprocess.run(['sudo','-n','true'],capture_output=True).returncode:
            self.skipTest('passwordless sudo unavailable; actual root-to-unprivileged transition requires CI or router')
        self.base.chmod(0o711)
        prefix=self.base/'root-install'
        prefix.mkdir(mode=0o755)
        try:
            # --no-start performs the real root dry-run but never starts packet interception.
            result=subprocess.run(['sudo','-n','env','PILOT_ENGINE_ARCHIVE='+self.archive,'sh',str(self.package_dir/'scripts/install.sh'),'--prefix',str(prefix),'--wan','lo','--listen',f'127.0.0.1:{self.port}','--no-start'],capture_output=True,text=True,timeout=60)
            self.assertEqual(result.returncode,0,'root dry-run failed: '+result.stderr)
            config=prefix/'etc/keenetic-pilot/config.json'
            enable="import json,sys; p=sys.argv[1]; c=json.load(open(p)); c['adaptive']=True; open(p,'w').write(json.dumps(c))"
            subprocess.run(['sudo','-n','python3','-c',enable,str(config)],check=True)
            result=subprocess.run(['sudo','-n',str(prefix/'keenetic-pilot/bin/pilot'),'--config',str(config),'--check-engine'],capture_output=True,text=True,timeout=30)
            self.assertEqual(result.returncode,0,'adaptive root dry-run failed: '+result.stderr)
            runtime=prefix/'keenetic-pilot-state'
            # Root can initialize raw sockets in CI; intercept=0 never opens NFQUEUE.
            # This executes Lua after the real nfqws2 UID drop, unlike --dry-run.
            engine=prefix/'keenetic-pilot/engine'
            args=['sudo','-n',str(engine/'nfqws2'),'--intercept=0']
            args += ['--lua-init=@'+str(engine/'lua'/name) for name in ['zapret-lib.lua.gz','zapret-antidpi.lua.gz','zapret-auto.lua.gz']]
            args += ['--lua-init=pilot_runtime=[=['+str(runtime)+']=]', '--lua-init=@'+str(prefix/'keenetic-pilot/lua/pilot-auto.lua')]
            args += ["--lua-init=local f=assert(io.open(pilot_runtime..'/state0.tsv','w'));f:write('test');f:close();assert(io.open(pilot_runtime..'/forbidden','w')==nil)"]
            result=subprocess.run(args,capture_output=True,text=True,timeout=30)
            self.assertEqual(result.returncode,0,'adaptive Lua UID isolation failed: '+result.stderr)

        finally:
            subprocess.run(['sudo','-n','rm','-rf','--',str(prefix)],check=True)

    def test_modified_controller_package_is_rejected_before_update(self):
        (self.package_dir / "README.md").write_text("modified after packaging")
        token_before = self.token_path.read_bytes()
        result = self.install(ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(token_before, self.token_path.read_bytes())
        self.assertEqual(self.health()["mode"], "router")
        self.assertFalse(list(self.prefix.glob("keenetic-pilot.backup.*")))

    def test_real_panel_password_login_update_and_restart(self):
        jar = http.cookiejar.CookieJar()
        browser = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
        base = f"http://127.0.0.1:{self.port}"

        def call(path, data=None):
            request = urllib.request.Request(base + path, None if data is None else json.dumps(data).encode(),
                                             {"Content-Type": "application/json"})
            return json.load(browser.open(request, timeout=5))

        with browser.open(base) as page:
            self.assertIn(b'login-form', page.read())
        self.assertFalse(call("/auth/status")["configured"])
        call("/auth/setup", {"code": self.pairing_code, "password": "integration-panel-password"})
        self.assertTrue(call("/auth/status")["authenticated"])
        self.assertFalse(call("/api/status")["running"])
        self.assertIn("interfaces", call("/api/network"))
        password_before = (self.config.parent / "password.json").read_bytes()
        self.install()
        self.assertEqual(password_before, (self.config.parent / "password.json").read_bytes())
        call("/auth/login", {"password": "integration-panel-password"})
        self.assertEqual(call("/api/status")["mode"], "router")
        call("/auth/logout", {})
        with self.assertRaises(urllib.error.HTTPError) as caught:
            call("/api/status")
        self.assertEqual(caught.exception.code, 401)

    def test_existing_panel_address_can_be_migrated_without_resetting_config(self):
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        before = json.loads(self.config.read_text())
        token = self.token_path.read_bytes()
        self.run_cmd(["sh", self.package_dir / "scripts/install.sh", "--prefix", self.prefix,
                      "--lan-panel", "--listen", f"127.0.0.1:{port}"])
        after = json.loads(self.config.read_text())
        self.assertEqual(after.pop("listen"), f"127.0.0.1:{port}")
        before.pop("listen")
        self.assertEqual(before, after)
        self.assertEqual(token, self.token_path.read_bytes())
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=5) as response:
            self.assertEqual(response.status, 200)

    def test_one_command_entry_downloads_verifies_and_installs_native_package(self):
        prefix = self.base / "one-command"
        (prefix / "bin").mkdir(parents=True)
        fixture = self.base / "download-fixture"
        fixture.mkdir()
        (fixture / "SHA256SUMS").write_bytes((ROOT / ".cache/published-manifest/SHA256SUMS").read_bytes())
        (fixture / self.package.name).write_bytes(self.package.read_bytes())
        curl = prefix / "bin/curl"
        curl.write_text(
            "#!/usr/bin/env python3\nimport sys,shutil\nfrom pathlib import Path\n"
            "args=sys.argv[1:]\nurl=next(v for v in args if v.startswith('https://'))\n"
            "assert url.startswith('https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/packages/v')\n"
            f"source=Path({str(fixture)!r})/url.rsplit('/',1)[-1]\n"
            "shutil.copyfile(source,args[args.index('-o')+1])\n"
        )
        curl.chmod(0o755)
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        env = {**self.env, "PILOT_PREFIX": str(prefix)}
        result = self.run_cmd(["sh", ROOT / ".cache/installer/install.sh", "--wan", "lo",
                               "--listen", f"127.0.0.1:{port}"], env=env)
        init = prefix / "etc/init.d/S98keenetic-pilot"
        self.addCleanup(lambda: subprocess.run([str(init), "stop"], capture_output=True, timeout=40))
        self.assertIn(f"http://127.0.0.1:{port}", result.stdout)
        self.assertRegex(result.stdout, r"Код первого входа \(15 минут\): [0-9]{8}")
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=5) as response:
            self.assertEqual(response.status, 200)

        # A modified remote manifest must fail before touching this installation.
        (fixture / "SHA256SUMS").write_bytes(b"tampered manifest\n")
        rejected = self.run_cmd(["sh", ROOT / ".cache/installer/install.sh"], ok=False, env=env)
        self.assertNotEqual(rejected.returncode, 0)
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=5) as response:
            self.assertEqual(response.status, 200)

    def test_failure_after_swap_restores_old_application_and_service(self):
        older = self.prefix / "keenetic-pilot.backup.100.1"
        older.mkdir()
        (older / ".pilot-managed").write_text("keenetic-pilot\n")
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
        self.assertTrue(older.is_dir(), "failed installation pruned recovery backup")


if __name__ == "__main__":
    unittest.main(verbosity=2)
