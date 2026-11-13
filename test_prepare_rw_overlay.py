import tempfile
import unittest
from pathlib import Path

import prepare_rw_overlay


class RWOverlayTests(unittest.TestCase):
    def test_preserves_stock_endpoint_and_enables_rw_ca(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "stock"
            source = root / "etc/mist/ep-control"
            source.mkdir(parents=True)
            source.joinpath("cloud.ini").write_text(
                'ENV = "production"\n[[cloud]]\nurl = "wss://ep-terminator.mistsys.net/ws"\n'
                'insecure = false\nrootcerts = "/etc/mist/ep-control/cacerts-production.pem"\n'
            )
            source.joinpath("cacerts-production.pem").write_text(
                "-----BEGIN CERTIFICATE-----\nSTOCK\n-----END CERTIFICATE-----\n"
            )
            lab = Path(directory) / "lab-ca.pem"
            lab.write_text("-----BEGIN CERTIFICATE-----\nLAB\n-----END CERTIFICATE-----\n")
            output = Path(directory) / "overlay"
            prepare_rw_overlay.build(root, lab, output)
            config = (output / "etc/mist/ep-control/cloud.ini").read_text()
            certs = (output / "etc/mist/ep-control/cacerts-production.pem").read_text()
            self.assertIn('url = "wss://ep-terminator.mistsys.net/ws"', config)
            self.assertIn('rootcerts = "/rw/etc/mist/ep-control/cacerts-production.pem"', config)
            self.assertIn("insecure = false", config)
            self.assertEqual(certs.count("BEGIN CERTIFICATE"), 2)
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)

    def test_rejects_unexpected_stock_config(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "stock"
            source = root / "etc/mist/ep-control"
            source.mkdir(parents=True)
            source.joinpath("cloud.ini").write_text('url = "ws://unexpected.invalid/ws"\n')
            source.joinpath("cacerts-production.pem").write_text("-----BEGIN CERTIFICATE-----\nX\n-----END CERTIFICATE-----\n")
            lab = Path(directory) / "lab-ca.pem"
            lab.write_text("-----BEGIN CERTIFICATE-----\nY\n-----END CERTIFICATE-----\n")
            with self.assertRaisesRegex(ValueError, "endpoint"):
                prepare_rw_overlay.build(root, lab, Path(directory) / "overlay")


if __name__ == "__main__":
    unittest.main()
