#!/usr/bin/env python3
"""Create secrets once without printing them or overwriting an existing .env."""
import os, pathlib, secrets
root = pathlib.Path(__file__).resolve().parents[1]
path = root / '.env'
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    f.write(f'STORE_DOMAIN=store.example.com\nADMIN_TOKEN={secrets.token_hex(32)}\nWEBHOOK_SECRET={secrets.token_hex(32)}\nGITHUB_TOKEN=\n')
print('Created private .env. Set STORE_DOMAIN before deployment.')
