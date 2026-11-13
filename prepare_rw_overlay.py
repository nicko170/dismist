#!/usr/bin/env python3
"""Build a minimal AP33 /rw tree for an offline controller trust anchor.

The stock rootfs startup copies default /rw files with rsync
--ignore-existing, so the files in this overlay remain authoritative. This
tool creates a directory tree; mkfs.ubifs must turn it into a UBI image before
U-Boot can write it. No AP is modified by this command.
"""

import argparse
from pathlib import Path
import re
import shutil


ROOTCERTS_PATH = "/rw/etc/mist/ep-control/cacerts-production.pem"
ROOTCERTS_RE = re.compile(r'^(\s*rootcerts\s*=\s*)"([^"]+)"', re.MULTILINE)
URL_RE = re.compile(r'^\s*url\s*=\s*"wss://ep-terminator(?:\$\{FIPS\})?\.mistsys\.net/ws"', re.MULTILINE)


def _cert_count(data: bytes) -> int:
    begin = data.count(b"-----BEGIN CERTIFICATE-----")
    end = data.count(b"-----END CERTIFICATE-----")
    if begin == 0 or begin != end:
        raise ValueError("invalid PEM CA bundle")
    return begin


def build(firmware_root: Path, lab_ca: Path, output: Path) -> None:
    source = firmware_root / "etc/mist/ep-control"
    stock_config = (source / "cloud.ini").read_text()
    stock_ca = (source / "cacerts-production.pem").read_bytes()
    lab_ca_bytes = lab_ca.read_bytes()
    _cert_count(stock_ca)
    _cert_count(lab_ca_bytes)
    if not URL_RE.search(stock_config):
        raise ValueError("unexpected stock cloud endpoint")
    if re.search(r"^\s*insecure\s*=\s*true\b", stock_config, re.MULTILINE):
        raise ValueError("stock config disables TLS verification")

    replacements = 0

    def replace(match: re.Match[str]) -> str:
        nonlocal replacements
        old = match.group(2)
        if old != ROOTCERTS_PATH and not old.startswith("/etc/mist/ep-control/cacerts-production"):
            raise ValueError("unexpected stock root CA path")
        replacements += 1
        return match.group(1) + '"' + ROOTCERTS_PATH + '"'

    local_config = ROOTCERTS_RE.sub(replace, stock_config)
    if replacements == 0:
        raise ValueError("stock cloud config has no rootcerts setting")
    bundle = stock_ca.rstrip() + b"\n" + lab_ca_bytes.strip() + b"\n"
    _cert_count(bundle)
    if output.exists():
        raise FileExistsError(output)
    try:
        output.mkdir(mode=0o700)
        destination = output / "etc/mist/ep-control"
        destination.mkdir(parents=True, mode=0o700)
        config_path = destination / "cloud.ini"
        cert_path = destination / "cacerts-production.pem"
        config_path.write_text(local_config)
        cert_path.write_bytes(bundle)
        config_path.chmod(0o600)
        cert_path.chmod(0o600)
    except BaseException:
        shutil.rmtree(output, ignore_errors=True)
        raise


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--firmware-root", type=Path, required=True)
    parser.add_argument("--controller-ca", "--lab-ca", dest="controller_ca", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    try:
        build(options.firmware_root, options.controller_ca, options.output)
    except (ValueError, OSError) as error:
        parser.exit(2, f"/rw overlay preparation failed: {error}\n")
    print(f"private /rw overlay prepared at {options.output}")


if __name__ == "__main__":
    main()
