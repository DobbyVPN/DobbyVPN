#!/usr/bin/env python3
"""Tests for the public deterministic release provenance utility."""
from __future__ import annotations

import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from release_provenance import (
    MANIFEST_NAME,
    TYPED_MANIFEST_NAME,
    ProvenanceError,
    create_manifest,
    create_typed_manifest,
    verify_manifest,
    verify_typed_manifest,
)


SOURCE_SHA = "0123456789abcdef0123456789abcdef01234567"
ASSETS = ["DobbyVPN.apk", "DobbyVPN.zip"]
METADATA = {
    "tag": "v1.4.7",
    "version": "1.4.7",
    "source_sha": SOURCE_SHA,
    "release_run_id": 12345,
    "release_run_number": 678,
    "android_version_code": 1_004_007,
}


def run_and_surface(command: list[str]) -> subprocess.CompletedProcess[str]:
    """retain both streams in the test runner's complete visible output."""
    completed = subprocess.run(
        command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    if completed.stdout:
        sys.stdout.write(completed.stdout)
        sys.stdout.flush()
    if completed.stderr:
        sys.stderr.write(completed.stderr)
        sys.stderr.flush()
    return completed


class ReleaseProvenanceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        for name, content in zip(ASSETS, (b"android", b"archive"), strict=True):
            (self.directory / name).write_bytes(content)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def create(self) -> Path:
        return create_manifest(self.directory, assets=ASSETS, **METADATA)

    def verify(self) -> Path:
        return verify_manifest(self.directory, assets=ASSETS, **METADATA)

    def test_cli_runner_surfaces_complete_stdout_and_stderr(self) -> None:
        stdout, stderr = io.StringIO(), io.StringIO()
        completed = subprocess.CompletedProcess(
            ["fixture"], 7, stdout="complete stdout\n", stderr="complete stderr\n"
        )
        with (
            mock.patch.object(subprocess, "run", return_value=completed),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            observed = run_and_surface(["fixture"])
        self.assertEqual(observed.returncode, 7)
        self.assertEqual(stdout.getvalue(), "complete stdout\n")
        self.assertEqual(stderr.getvalue(), "complete stderr\n")

    def test_create_is_canonical_and_verify_round_trips(self):
        manifest = self.create()
        self.assertEqual(manifest, self.directory / MANIFEST_NAME)
        raw = manifest.read_bytes()
        self.assertTrue(raw.endswith(b"\n"))
        self.assertEqual(
            raw,
            json.dumps(
                json.loads(raw), sort_keys=True, separators=(",", ":"), ensure_ascii=True
            ).encode()
            + b"\n",
        )
        self.assertEqual(self.verify(), manifest)

    def test_cli_requires_all_metadata_and_verifies_it(self):
        script = Path(__file__).with_name("release_provenance.py")
        create = [
            sys.executable,
            str(script),
            "create",
            "--directory",
            str(self.directory),
            "--tag",
            METADATA["tag"],
            "--version",
            METADATA["version"],
            "--source-sha",
            SOURCE_SHA,
            "--release-run-id",
            "12345",
            "--release-run-number",
            "678",
            "--android-version-code",
            "1004007",
            "--asset",
            ASSETS[0],
            "--asset",
            ASSETS[1],
        ]
        self.assertEqual(run_and_surface(create).returncode, 0)
        good = create.copy()
        good[2] = "verify"
        self.assertEqual(run_and_surface(good).returncode, 0)
        bad = good.copy()
        bad[bad.index("--release-run-id") + 1] = "12346"
        result = run_and_surface(bad)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("metadata", result.stderr)

    def test_rejects_invalid_metadata_and_unsorted_or_unsafe_assets(self):
        invalid = dict(METADATA)
        invalid["tag"] = "v01.4.7"
        with self.assertRaises(ProvenanceError):
            create_manifest(self.directory, assets=ASSETS, **invalid)
        invalid = dict(METADATA)
        invalid["version"] = "1.4.8"
        with self.assertRaises(ProvenanceError):
            create_manifest(self.directory, assets=ASSETS, **invalid)
        invalid = dict(METADATA)
        invalid["source_sha"] = SOURCE_SHA.upper()
        with self.assertRaises(ProvenanceError):
            create_manifest(self.directory, assets=ASSETS, **invalid)
        for assets in ([ASSETS[1], ASSETS[0]], [ASSETS[0], ASSETS[0]], ["../secret"]):
            with self.subTest(assets=assets):
                with self.assertRaises(ProvenanceError):
                    create_manifest(self.directory, assets=assets, **METADATA)

    def test_rejects_missing_extra_symlink_and_directory_entries(self):
        (self.directory / ASSETS[1]).unlink()
        with self.assertRaises(ProvenanceError):
            self.create()
        (self.directory / ASSETS[1]).write_bytes(b"archive")
        (self.directory / "unexpected").write_bytes(b"no")
        with self.assertRaises(ProvenanceError):
            self.create()
        (self.directory / "unexpected").unlink()
        (self.directory / "directory").mkdir()
        with self.assertRaises(ProvenanceError):
            self.create()
        (self.directory / "directory").rmdir()
        try:
            (self.directory / "link").symlink_to(self.directory / ASSETS[0])
        except (NotImplementedError, OSError):
            self.skipTest("symlinks unavailable on this platform")
        with self.assertRaises(ProvenanceError):
            self.create()

    def test_rejects_noncanonical_and_malformed_manifest(self):
        manifest = self.create()
        payload = json.loads(manifest.read_text())
        manifest.write_text(json.dumps(payload, indent=2), encoding="utf-8")
        with self.assertRaises(ProvenanceError):
            self.verify()
        manifest.write_text('{"schema":1,"schema":1}', encoding="utf-8")
        with self.assertRaises(ProvenanceError):
            self.verify()
        manifest.write_text("not json\n", encoding="utf-8")
        with self.assertRaises(ProvenanceError):
            self.verify()

    def test_rejects_asset_digest_size_and_allowlist_mismatch(self):
        self.create()
        (self.directory / ASSETS[0]).write_bytes(b"changed")
        with self.assertRaises(ProvenanceError):
            self.verify()
        self.create()
        with self.assertRaises(ProvenanceError):
            verify_manifest(self.directory, assets=[ASSETS[0]], **METADATA)

    def test_rejects_empty_release_asset(self):
        (self.directory / ASSETS[0]).write_bytes(b"")
        with self.assertRaises(ProvenanceError):
            self.create()

    def testVerifierChecksEveryMetadataField(self):
        self.create()
        changed_values = {
            "tag": "v1.4.8",
            "version": "1.4.8",
            "source_sha": "f" * 40,
            "release_run_id": 12346,
            "release_run_number": 679,
            "android_version_code": 1_004_008,
        }
        for key, value in changed_values.items():
            with self.subTest(key=key):
                asserted = dict(METADATA)
                asserted[key] = value
                # Changing tag requires the matching version so this remains
                # syntactically valid and tests metadata equality, not parsing.
                if key == "tag":
                    asserted["version"] = "1.4.8"
                if key == "version":
                    asserted["tag"] = "v1.4.8"
                with self.assertRaises(ProvenanceError):
                    verify_manifest(self.directory, assets=ASSETS, **asserted)

    def test_rejects_canonical_manifest_with_unknown_field(self):
        manifest = self.create()
        payload = json.loads(manifest.read_text())
        payload["private"] = "forbidden"
        manifest.write_bytes(
            json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()
            + b"\n"
        )
        with self.assertRaises(ProvenanceError):
            self.verify()

    def test_manifest_cannot_be_an_asset_or_symlink(self):
        with self.assertRaises(ProvenanceError):
            create_manifest(self.directory, assets=[MANIFEST_NAME], **METADATA)
        manifest = self.create()
        manifest.unlink()
        try:
            manifest.symlink_to(self.directory / ASSETS[0])
        except (NotImplementedError, OSError):
            self.skipTest("symlinks unavailable on this platform")
        with self.assertRaises(ProvenanceError):
            self.verify()


TYPED_ASSETS = [
    {
        "name": "DobbyVPN-v1.4.7-sign.apk",
        "platform": "android",
        "architecture": "arm64-v8a",
        "kind": "apk-signed",
    },
    {
        "name": "dobbyVPN-linux.deb",
        "platform": "linux",
        "architecture": "amd64",
        "kind": "deb",
    },
]


class TypedReleaseProvenanceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        (self.directory / TYPED_ASSETS[0]["name"]).write_bytes(b"signed apk")
        (self.directory / TYPED_ASSETS[1]["name"]).write_bytes(b"linux package")

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def create(self) -> Path:
        return create_typed_manifest(self.directory, assets=TYPED_ASSETS, **METADATA)

    def verify(self) -> Path:
        return verify_typed_manifest(self.directory, assets=TYPED_ASSETS, **METADATA)

    def test_explicit_identity_source_and_bytes_round_trip(self):
        manifest = self.create()
        self.assertEqual(manifest, self.directory / TYPED_MANIFEST_NAME)
        payload = json.loads(manifest.read_text(encoding="utf-8"))
        self.assertEqual(payload["schema"], 2)
        self.assertEqual(
            [
                (item["name"], item["platform"], item["architecture"], item["kind"], item["source_sha"])
                for item in payload["assets"]
            ],
            [
                ("DobbyVPN-v1.4.7-sign.apk", "android", "arm64-v8a", "apk-signed", SOURCE_SHA),
                ("dobbyVPN-linux.deb", "linux", "amd64", "deb", SOURCE_SHA),
            ],
        )
        self.assertEqual(self.verify(), manifest)

    def test_v1_manifest_remains_independent_and_can_include_v2_sidecar(self):
        self.create()
        assets = [item["name"] for item in TYPED_ASSETS] + [TYPED_MANIFEST_NAME]
        assets.sort()
        create_manifest(self.directory, assets=assets, **METADATA)
        self.assertEqual(self.verify(), self.directory / TYPED_MANIFEST_NAME)
        self.assertEqual(
            verify_manifest(self.directory, assets=assets, **METADATA), self.directory / MANIFEST_NAME
        )

    def test_identity_is_not_inferred_from_filename_or_digest(self):
        self.create()
        changed = [dict(item) for item in TYPED_ASSETS]
        changed[0]["platform"] = "linux"
        with self.assertRaises(ProvenanceError):
            verify_typed_manifest(self.directory, assets=changed, **METADATA)
        payload = json.loads((self.directory / TYPED_MANIFEST_NAME).read_text(encoding="utf-8"))
        payload["assets"][0]["source_sha"] = "f" * 40
        (self.directory / TYPED_MANIFEST_NAME).write_bytes(
            json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()
            + b"\n"
        )
        with self.assertRaises(ProvenanceError):
            self.verify()

    def test_rejects_manifest_descriptor_and_unsafe_identity(self):
        with self.assertRaises(ProvenanceError):
            create_typed_manifest(
                self.directory,
                assets=[{**TYPED_ASSETS[0], "name": TYPED_MANIFEST_NAME}],
                **METADATA,
            )
        with self.assertRaises(ProvenanceError):
            create_typed_manifest(
                self.directory,
                assets=[{**TYPED_ASSETS[0], "platform": "../linux"}, TYPED_ASSETS[1]],
                **METADATA,
            )

    def test_cli_requires_explicit_typed_descriptors(self):
        script = Path(__file__).with_name("release_provenance.py")
        command = [
            sys.executable,
            str(script),
            "typed-create",
            "--directory",
            str(self.directory),
            "--tag",
            METADATA["tag"],
            "--version",
            METADATA["version"],
            "--source-sha",
            SOURCE_SHA,
            "--release-run-id",
            "12345",
            "--release-run-number",
            "678",
            "--android-version-code",
            "1004007",
            "--typed-asset",
            "DobbyVPN-v1.4.7-sign.apk|android|arm64-v8a|apk-signed",
            "--typed-asset",
            "dobbyVPN-linux.deb|linux|amd64|deb",
        ]
        self.assertEqual(run_and_surface(command).returncode, 0)
        verify = command.copy()
        verify[2] = "typed-verify"
        self.assertEqual(run_and_surface(verify).returncode, 0)


if __name__ == "__main__":
    unittest.main()
