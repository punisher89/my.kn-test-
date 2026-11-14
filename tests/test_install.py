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
