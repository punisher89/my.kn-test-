import pathlib, subprocess, tempfile, unittest

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "scripts/vps-telegram-wg.sh"
CONFIG = """[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.77.66.2/32
MTU = 1380
[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
Endpoint = vpn.example.org:51820
AllowedIPs = 91.108.56.0/22,91.108.4.0/22,91.108.8.0/22,91.108.16.0/22,91.108.12.0/22,149.154.160.0/20,91.105.192.0/23,91.108.20.0/22,185.76.151.0/24
PersistentKeepalive = 25
"""

class Config(unittest.TestCase):
    def validate(self, value):
        with tempfile.TemporaryDirectory() as d:
            p=pathlib.Path(d)/"pilotwg0.conf"
            p.write_text(value)
            return subprocess.run(["sh",str(SCRIPT),"validate",str(p)],capture_output=True).returncode
    def test_valid(self):
        self.assertEqual(self.validate(CONFIG),0)
    def test_rejected(self):
        for value in (CONFIG.replace("MTU = 1380","PostUp = echo bad"),
                      CONFIG.replace("91.108.56.0/22","0.0.0.0/0"),
                      CONFIG.replace("PersistentKeepalive = 25","DNS = 1.1.1.1"),
                      CONFIG+"[Peer]\n",CONFIG.replace("Endpoint = vpn.example.org:51820","Endpoint = -x:22")):
            self.assertNotEqual(self.validate(value),0)

if __name__=="__main__":
    unittest.main()
