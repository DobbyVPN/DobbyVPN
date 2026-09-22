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
        self.assertIn('expected_bridge_output="$(python3 - "$provenance"', self.script)
        self.assertIn("<<'PY' | tee /dev/stderr", self.script)
        self.assertIn('document["artifacts"]["ios"]["sha256"]', self.script)
        self.assertIn('re.fullmatch(r"[0-9a-f]{64}", value)', self.script)
        self.assertIn('[[ ! "$expected_bridge_output" =~ ^[0-9a-f]{64}$ ]]', self.script)
        self.assertNotIn("| tail", self.script)
        self.assertIn('if [[ "$actual_bridge_hash" != "$expected_bridge_hash" ]]', self.script)

    def test_provenance_parser_rejects_bad_hashes(self) -> None:
        self.assertIn("expected exactly 64 lowercase hexadecimal characters", self.script)
        self.assertIn("invalid TrustTunnel static-library provenance JSON", self.script)
        self.assertIn("has no artifacts.ios.sha256", self.script)

    def test_tool_and_binary_hash_reports_are_forwarded_before_parsing(self) -> None:
        self.assertIn('go version -m "$tool" | tee /dev/stderr', self.script)
        self.assertIn("shasum -a 256 \"$bridge\" | tee /dev/stderr", self.script)
        self.assertNotIn("| awk", self.script)


if __name__ == "__main__":
    unittest.main()
