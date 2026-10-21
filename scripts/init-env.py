#!/usr/bin/env python3
"""Create secrets once without printing them or overwriting an existing .env."""
import argparse, os, pathlib, re, secrets
root = pathlib.Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--output', type=pathlib.Path, default=root / '.env')
p.add_argument('--domain', default='store.example.com')
args = p.parse_args()
if not re.fullmatch(r'[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?', args.domain):
    p.error('Use a hostname without a scheme, path or port')
path = args.output
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    f.write(f'STORE_DOMAIN={args.domain}\nADMIN_TOKEN={secrets.token_hex(32)}\nWEBHOOK_SECRET={secrets.token_hex(32)}\nGITHUB_TOKEN=\n')
print('Created private deployment environment; existing files are never overwritten.')
