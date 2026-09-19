from pathlib import Path
import re
import unittest


class IosBridgeProvenanceContractTests(unittest.TestCase):
    def setUp(self) -> None:
        root = Path(__file__).resolve().parents[2]
        self.script = (root / "go_module/scripts/build_ios_xcframework.sh").read_text(encoding="utf-8")

    def test_device_bridge_hash_comes_from_trusttunnel_provenance(self) -> None:
        self.assertNotRegex(self.script, r"[0-9a-f]{64}")
        self.assertIn('provenance="$module_dir/lib/static-libraries.provenance.json"', self.script)
        self.assertIn('expected_bridge_hash="$(python3 - "$provenance"', self.script)
        self.assertIn('document["artifacts"]["ios"]["sha256"]', self.script)
        self.assertIn('re.fullmatch(r"[0-9a-f]{64}", value)', self.script)
        self.assertIn('if [[ "$actual_bridge_hash" != "$expected_bridge_hash" ]]', self.script)

    def test_provenance_parser_rejects_bad_hashes(self) -> None:
        self.assertIn("expected exactly 64 lowercase hexadecimal characters", self.script)
        self.assertIn("invalid TrustTunnel static-library provenance JSON", self.script)
        self.assertIn("has no artifacts.ios.sha256", self.script)


if __name__ == "__main__":
    unittest.main()
