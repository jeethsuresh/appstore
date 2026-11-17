#!/usr/bin/env python3
"""Keep secure timestamps required while recovering from Apple's hostname path failures."""
import ipaddress
import socket
import subprocess
import sys

TIMESTAMP_ERROR = "A timestamp was expected but was not found"


def run_logged(command):
    output = []
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True) as process:
        for line in process.stdout:
            print(line, end="", flush=True)
            output.append(line)
        return subprocess.CompletedProcess(command, process.wait(), "".join(output))


def apple_ipv4():
    return [item[4][0] for item in socket.getaddrinfo("timestamp.apple.com", 80, socket.AF_INET, socket.SOCK_STREAM)]


def package(command, run=run_logged, resolve=apple_ipv4):
    result = run(command)
    if result.returncode == 0 or TIMESTAMP_ERROR not in result.stdout:
        return result.returncode
    # Keep Apple's timestamp authority. macOS verifies its signed timestamp and
    # the unchanged notarization/smoke gates still validate the completed bundle.
    # Resolve fresh addresses; never bake an Apple server address into releases.
    try:
        addresses = []
        for value in resolve():
            address = ipaddress.IPv4Address(value)
            if address in ipaddress.IPv4Network("17.0.0.0/8") and str(address) not in addresses:
                addresses.append(str(address))
    except (OSError, ValueError) as error:
        print(f"Cannot resolve Apple's timestamp IPv4 path: {error}", file=sys.stderr)
        return result.returncode
    for address in addresses[:3]:
        print(f"Retrying secure Apple timestamp through resolved IPv4 address {address}", flush=True)
        result = run([*command, f"-c.mac.timestamp=http://{address}/ts01"])
        if result.returncode == 0 or TIMESTAMP_ERROR not in result.stdout:
            return result.returncode
    return result.returncode


if __name__ == "__main__":
    command = sys.argv[1:]
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        raise SystemExit("Supply the electron-builder package command")
    raise SystemExit(package(command))
