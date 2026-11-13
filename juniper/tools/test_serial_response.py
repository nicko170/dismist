"""Synthetic AP33 legacy console challenge vectors; no device data."""

import base64
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock


sys.path.insert(0, str(Path(__file__).resolve().parent))
import serial_response  # noqa: E402


class SerialResponseTests(unittest.TestCase):
    CHALLENGE = (
        "BQXwwMi0xMS0yMi0zMy00NC01NXxyb290fAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
    )
    RESPONSE = (
        "BICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj/ME6+1zZ+l45CtgHHWvPyjwBrrFxFJaGaS+uxai1UnnA=="
    )

    def test_known_hmac_vector(self):
        with mock.patch.object(serial_response.os, "urandom", return_value=bytes(range(32, 64))):
            self.assertEqual(serial_response.respond(self.CHALLENGE), self.RESPONSE)

    def test_other_mac_and_binary_delimiter_nonce(self):
        challenge = (
            "BQXwwMi1hYS1iYi1jYy1kZC1lZXxyb290fHwBAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
        )
        expected = (
            "BICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj+6L6mgqT8iVkLn4r/90U1KiZA9f5rHQKfbNyehWRCZcA=="
        )
        with mock.patch.object(serial_response.os, "urandom", return_value=bytes(range(32, 64))):
            self.assertEqual(serial_response.respond(challenge), expected)

    def test_rejects_malformed_mac_in_legacy_challenge(self):
        for mac in (b"02-11-22-33-44-gg", b"02:11:22:33:44:55", b"02-11-22-33-44"):
            with self.subTest(mac=mac):
                challenge = "B" + base64.b64encode(b"A|" + mac + b"|root|" + bytes(range(32))).decode()
                with self.assertRaisesRegex(ValueError, "MAC"):
                    serial_response.respond(challenge)

    def test_rejects_newer_signed_mode_and_other_users(self):
        for raw in (b"C\x04root", b"A|02-11-22-33-44-55|admin|" + bytes(range(32))):
            with self.subTest(raw=raw[:10]):
                challenge = "B" + base64.b64encode(raw).decode()
                with self.assertRaises(ValueError):
                    serial_response.respond(challenge)

    def test_rejects_bad_framing_and_nonce_length(self):
        for challenge in (
            "not-base64",
            "B%%%",
            "B" + base64.b64encode(b"A|02-11-22-33-44-55|root|short").decode(),
        ):
            with self.subTest(challenge=challenge):
                with self.assertRaises(ValueError):
                    serial_response.respond(challenge)

    def test_cli_reports_invalid_challenge_without_traceback(self):
        result = subprocess.run(
            [sys.executable, str(Path(serial_response.__file__)), "B%%%"],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("invalid", result.stderr)
        self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
