import contextlib
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest

from release_provenance import ProvenanceError, create_manifest, verify_manifest
from version_metadata import main, parse_version


VERSION = "1.5.2"
VERSION_CODE = 1_005_002
SOURCE_SHA = "a" * 40


def manifest_arguments(assets):
    return {
        "tag": f"v{VERSION}",
        "version": VERSION,
        "source_sha": SOURCE_SHA,
        "release_run_id": 17,
        "release_run_number": 9,
        "android_version_code": VERSION_CODE,
        "assets": assets,
    }


class VersionDocumentTests(unittest.TestCase):
    def test_update_document_has_exact_legacy_format(self):
        self.assertEqual(
            parse_version(VERSION).update_document(),
            "versionCode=1005002\nversionName=1.5.2\n",
        )

    def test_cli_writes_update_document(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "version.txt"
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(
                    main(["--version", VERSION, "--update-file", str(output)]),
                    0,
                )
            self.assertEqual(
                output.read_bytes(),
                b"versionCode=1005002\nversionName=1.5.2\n",
            )


class VersionProvenanceTests(unittest.TestCase):
    def test_requested_version_asset_must_match_metadata_even_with_matching_digest(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            version_path = directory / "version.txt"
            version_path.write_text(parse_version(VERSION).update_document(), encoding="utf-8")
            (directory / "package.bin").write_bytes(b"release package")
            create_manifest(directory, **manifest_arguments(["version.txt", "package.bin"])
            )

            wrong_document = b"versionCode=1005003\nversionName=1.5.3\n"
            version_path.write_bytes(wrong_document)

            # Make the manifest digest agree with the altered file, so verification
            # must compare its contents with the asserted version as well.
            manifest_path = directory / "release-provenance.json"
            payload = json.loads(manifest_path.read_text(encoding="utf-8"))
            record = next(asset for asset in payload["assets"] if asset["name"] == "version.txt")
            record["size"] = len(wrong_document)
            record["sha256"] = hashlib.sha256(wrong_document).hexdigest()
            manifest_path.write_text(
                json.dumps(payload, ensure_ascii=True, separators=(",", ":"), sort_keys=True) + "\n",
                encoding="utf-8",
            )

            with self.assertRaisesRegex(ProvenanceError, "version.txt does not match"):
                verify_manifest(directory, **manifest_arguments(["version.txt", "package.bin"]))

    def test_old_manifest_allowlist_without_version_asset_remains_valid(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "package.bin").write_bytes(b"older release package")
            assets = ["package.bin"]
            create_manifest(directory, **manifest_arguments(assets))

            self.assertEqual(
                verify_manifest(directory, **manifest_arguments(assets)),
                directory / "release-provenance.json",
            )

    def test_requested_version_asset_rejects_version_code_not_derived_from_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "version.txt").write_text(
                parse_version(VERSION).update_document(), encoding="utf-8"
            )

            arguments = manifest_arguments(["version.txt"])
            arguments["android_version_code"] += 1
            with self.assertRaisesRegex(ProvenanceError, "does not match the canonical release version"):
                create_manifest(directory, **arguments)


if __name__ == "__main__":
    unittest.main()
