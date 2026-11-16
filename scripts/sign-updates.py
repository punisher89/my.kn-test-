#!/usr/bin/env python3
"""Sign release metadata or the domain registry with a separate trust anchor."""
import argparse,base64,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('file',type=Path);p.add_argument('--init-key',action='store_true');a=p.parse_args()
key=ROOT/'local/updates-signing.pem';pub=ROOT/'internal/pilot/updates-public.key'
if not key.exists():
 if not a.init_key:raise SystemExit('Restore local/updates-signing.pem before signing')
 key.parent.mkdir(exist_ok=True);subprocess.run(['openssl','genpkey','-algorithm','ED25519','-out',str(key)],check=True);key.chmod(0o600)
key.chmod(0o600)
encoded=base64.b64encode(subprocess.check_output(['openssl','pkey','-in',str(key),'-pubout','-outform','DER'])[-32:])+b'\n'
if pub.exists() and pub.read_bytes()!=encoded:raise SystemExit('Trust key differs; refusing implicit rotation')
pub.write_bytes(encoded)
sig=subprocess.check_output(['openssl','pkeyutl','-sign','-rawin','-inkey',str(key),'-in',str(a.file)])
Path(str(a.file)+'.sig').write_bytes(base64.b64encode(sig)+b'\n')
print('Signed',a.file.name,'with the updates trust key; private key stays in ignored local/')
