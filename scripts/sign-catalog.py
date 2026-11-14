#!/usr/bin/env python3
import argparse,base64,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--init-key',action='store_true');a=p.parse_args()
key=ROOT/'local/catalog-signing.pem'
if not key.exists():
    if not a.init_key:raise SystemExit('Signing key missing: restore it securely or use --init-key for a new trust anchor')
    subprocess.run(['openssl','genpkey','-algorithm','ED25519','-out',str(key)],check=True);key.chmod(0o600)
pub=subprocess.check_output(['openssl','pkey','-in',str(key),'-pubout','-outform','DER'])[-32:]
pubpath=ROOT/'internal/pilot/catalog-public.key'
encoded=base64.b64encode(pub)+b'\n'
if pubpath.exists() and pubpath.read_bytes()!=encoded:raise SystemExit('Public key differs: explicit application trust-key rotation required')
pubpath.write_bytes(encoded)
data=ROOT/'catalog/strategies.json'
sig=subprocess.check_output(['openssl','pkeyutl','-sign','-rawin','-inkey',str(key),'-in',str(data)])
(ROOT/'catalog/strategies.json.sig').write_bytes(base64.b64encode(sig)+b'\n')
print('Catalog signed; private key remains in ignored local/ and is never published.')
