#!/usr/bin/env python3
"""Answer a legacy AP33 0.8 A-mode root challenge on the serial console.

The HMAC constants are part of the observed 0.8 console_login firmware, not
an AP secret. Newer 0.14/0.15 C-mode challenges require a signing key and
cannot be answered by this tool.
"""

import argparse
import base64
import binascii
import hashlib
import hmac
import os
import re


PREFIX = b"sr7Krl7tkajVBowS"
SUFFIX = b"ZuSX01QGh8PJq0Na"
MAC = re.compile(rb"[0-9a-fA-F]{2}(?:-[0-9a-fA-F]{2}){5}\Z")


def respond(challenge: str) -> str:
    """Return the B/base64 response for a full legacy root challenge."""
    if not challenge.startswith("B"):
        raise ValueError("invalid challenge prefix (expected B)")
    try:
        raw = base64.b64decode(challenge[1:], validate=True)
    except (binascii.Error, ValueError) as error:
        raise ValueError("invalid challenge base64") from error
    if raw.startswith(b"C"):
        raise ValueError("signed C-mode challenge is unsupported")
    fields = raw.split(b"|", 3)
    if len(fields) != 4 or fields[0] != b"A" or fields[2] != b"root":
        raise ValueError("invalid legacy A-mode root challenge")
    if not MAC.fullmatch(fields[1]):
        raise ValueError("invalid challenge MAC (expected six dashed hex octets)")
    server_nonce = fields[3]
    if len(server_nonce) != 32:
        raise ValueError("invalid challenge nonce (expected 32 bytes)")

    client_nonce = os.urandom(32)
    first = hmac.new(server_nonce, client_nonce, hashlib.sha256).digest()
    auth = hmac.new(first, PREFIX + raw + SUFFIX, hashlib.sha256).digest()
    return "B" + base64.b64encode(client_nonce + auth).decode("ascii")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("challenge", help="complete challenge value, including its leading B")
    args = parser.parse_args()
    try:
        print(respond(args.challenge))
    except ValueError as error:
        parser.exit(2, f"cannot generate response: {error}\n")


if __name__ == "__main__":
    main()
