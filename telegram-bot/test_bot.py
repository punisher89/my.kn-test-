import unittest
from pathlib import Path
import tempfile
from bot import Bot, private_allowed, release, valid_release

def doc():
    return dict(kind="pilot-ssh-release", channel="ssh", source="ChatGPT",
                installed_via="ssh", signed_panel_updater_modified=False,
                version="0.2.0-beta.6", git_sha="a"*40,
                manifest_sha256="b"*64, installer_sha256="c"*64, engine_sha256="d"*64)

class Tests(unittest.TestCase):
    def test_pointer(self):
        d=doc()
        self.assertEqual(release(lambda u:d),d)
        with self.assertRaises(ValueError):
            release(lambda u:d if "latest" in u else dict(d,version="0.2.0-beta.5"))
        self.assertFalse(valid_release(dict(d,source="Codex")))
    def test_private(self):
        self.assertTrue(private_allowed({"chat":{"id":1,"type":"private"}},{"id":1},{1}))
        self.assertFalse(private_allowed({"chat":{"id":-1,"type":"group"}},{"id":1},{1}))
        self.assertFalse(private_allowed({"chat":{"id":2,"type":"private"}},{"id":2},{1}))
    def test_announce_dedup(self):
        with tempfile.TemporaryDirectory() as t:
            state=Path(t)/"state.json"
            b=Bot("fake",{1},-1,"",state)
            seen=[]
            b.send=lambda *a:seen.append(a)
            b.fetch_release=doc
            b.announce()
            b.announce()
            b=Bot("fake",{1},-1,"",state)
            b.send=lambda *a:seen.append(a)
            b.fetch_release=doc
            b.announce()
            self.assertEqual(len(seen),1)
    def test_group_control_rejected(self):
        with tempfile.TemporaryDirectory() as t:
            b=Bot("fake",{1},-1,"",Path(t)/"state",control_token="fake")
            b.send=lambda *a: self.fail("unauthorized message")
            b.handle({"message":{"chat":{"id":-1,"type":"group"},"from":{"id":1},"text":"/start"}})
            b.handle({"message":{"chat":{"id":2,"type":"private"},"from":{"id":2},"text":"/start"}})
    def test_confirm_once(self):
        with tempfile.TemporaryDirectory() as t:
            b=Bot("fake",{1},-1,"",Path(t)/"state",control_token="fake")
            b.send=lambda *a,**kw:None
            b.telegram=lambda *a:None
            calls=[]
            b.pilot=lambda *a:(calls.append(a) or {"status":"accepted"})
            def cb(action):
                b.rates.clear()
                b.handle({"callback_query":{"id":"1","from":{"id":1},"message":{"chat":{"id":1,"type":"private"}},"data":action}})
            cb("ask:off")
            key=b.pending[1][0]
            cb("confirm:bad")
            self.assertEqual(calls,[])
            cb("confirm:"+key)
            cb("confirm:"+key)
            self.assertEqual(len(calls),1)

if __name__=="__main__":
    unittest.main()
