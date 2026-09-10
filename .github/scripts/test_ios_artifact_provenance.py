#!/usr/bin/env python3
"""Regression tests for the iOS IPA provenance contract."""

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

from ios_artifact_provenance import (
    SCHEMA,
    json_bytes,
    load_provenance,
    make_provenance,
    verify_provenance,
)


SOURCE_SHA = "a" * 40
VERSION = "1.4.7"
BUILD_NUMBER = "2143"


def run_and_surface(
    command: list[str], *, check: bool = True,
) -> subprocess.CompletedProcess[str]:
    """retain both command streams in the complete visible test output."""
    completed = subprocess.run(
        command,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.stdout:
        sys.stdout.write(completed.stdout)
        sys.stdout.flush()
    if completed.stderr:
        sys.stderr.write(completed.stderr)
        sys.stderr.flush()
    if check and completed.returncode:
        raise subprocess.CalledProcessError(
            completed.returncode, command, output=completed.stdout, stderr=completed.stderr
        )
    return completed


class IosArtifactProvenanceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.ipa_dir = self.root / "ipa"
        self.ipa_dir.mkdir()
        self.ipa = self.ipa_dir / "DobbyVPN.ipa"
        self.ipa.write_bytes(b"signed IPA bytes\x00")
        self.sidecar = self.root / "DobbyVPN.ipa.provenance.json"

    def tearDown(self) -> None:
        self.temp.cleanup()

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
            observed = run_and_surface(["fixture"], check=False)
        self.assertEqual(observed.returncode, 7)
        self.assertEqual(stdout.getvalue(), "complete stdout\n")
        self.assertEqual(stderr.getvalue(), "complete stderr\n")

    def create_sidecar(self) -> dict[str, object]:
        provenance = make_provenance(self.ipa_dir, SOURCE_SHA, VERSION, BUILD_NUMBER)
        self.sidecar.write_bytes(json_bytes(provenance))
        return provenance

    def test_create_is_canonical_and_verifies_complete_binding(self) -> None:
        provenance = self.create_sidecar()
        self.assertEqual(provenance["schema"], SCHEMA)
        self.assertEqual(provenance["source_sha"], SOURCE_SHA)
        self.assertEqual(provenance["version"], VERSION)
        self.assertEqual(provenance["build_number"], int(BUILD_NUMBER))
        self.assertEqual(provenance["ipa"]["filename"], "DobbyVPN.ipa")  # type: ignore[index]
        self.assertEqual(self.sidecar.read_bytes(), json_bytes(provenance))
        verify_provenance(self.ipa_dir, self.sidecar, SOURCE_SHA, VERSION, BUILD_NUMBER)

    def test_verification_rejects_each_selected_release_identity_field(self) -> None:
        self.create_sidecar()
        for source_sha, version, build in (
            ("b" * 40, VERSION, BUILD_NUMBER),
            (SOURCE_SHA, "1.4.8", BUILD_NUMBER),
            (SOURCE_SHA, VERSION, "2144"),
        ):
            with self.subTest(source_sha=source_sha, version=version, build=build):
                with self.assertRaises(ValueError):
                    verify_provenance(self.ipa_dir, self.sidecar, source_sha, version, build)

    def test_verification_rejects_changed_ipa_filename_size_and_digest(self) -> None:
        self.create_sidecar()
        self.ipa.write_bytes(b"different signed IPA bytes")
        with self.assertRaises(ValueError):
            verify_provenance(self.ipa_dir, self.sidecar, SOURCE_SHA, VERSION, BUILD_NUMBER)

        self.ipa.rename(self.ipa_dir / "Renamed.ipa")
        with self.assertRaises(ValueError):
            verify_provenance(self.ipa_dir, self.sidecar, SOURCE_SHA, VERSION, BUILD_NUMBER)

    def test_requires_the_named_ipa_and_ignores_unrelated_entries(self) -> None:
        renamed = self.ipa.with_name("Renamed.ipa")
        self.ipa.rename(renamed)
        with self.assertRaises(ValueError):
            make_provenance(self.ipa_dir, SOURCE_SHA, VERSION, BUILD_NUMBER)
        renamed.rename(self.ipa)
        (self.ipa_dir / "second.ipa").write_bytes(b"unrelated")
        self.assertEqual(
            make_provenance(self.ipa_dir, SOURCE_SHA, VERSION, BUILD_NUMBER)["ipa"]["filename"],  # type: ignore[index]
            "DobbyVPN.ipa",
        )

    def test_records_empty_ipa_exactly(self) -> None:
        self.ipa.write_bytes(b"")
        provenance = make_provenance(self.ipa_dir, SOURCE_SHA, VERSION, BUILD_NUMBER)
        self.assertEqual(provenance["ipa"]["size_bytes"], 0)  # type: ignore[index]

    def test_accepts_ordinary_json_formatting_and_rejects_malformed_sidecar(self) -> None:
        provenance = self.create_sidecar()
        self.sidecar.write_text(json.dumps(provenance, indent=2), encoding="utf-8")
        self.assertEqual(load_provenance(self.sidecar), provenance)
        self.sidecar.write_bytes(json_bytes({"schema": SCHEMA}))
        with self.assertRaises(ValueError):
            load_provenance(self.sidecar)

    def test_rejects_noncanonical_identity_values(self) -> None:
        for source_sha, version, build in (
            ("A" * 40, VERSION, BUILD_NUMBER),
            (SOURCE_SHA, "01.4.7", BUILD_NUMBER),
            (SOURCE_SHA, VERSION, "0"),
        ):
            with self.subTest(source_sha=source_sha, version=version, build=build):
                with self.assertRaises(ValueError):
                    make_provenance(self.ipa_dir, source_sha, version, build)

    def test_cli_creates_and_verifies_the_same_contract(self) -> None:
        script = Path(__file__).with_name("ios_artifact_provenance.py")
        command = [
            sys.executable,
            str(script),
            "create",
            "--ipa-dir",
            str(self.ipa_dir),
            "--provenance",
            str(self.sidecar),
            "--source-sha",
            SOURCE_SHA,
            "--version",
            VERSION,
            "--build-number",
            BUILD_NUMBER,
        ]
        run_and_surface(command)
        command[2] = "verify"
        run_and_surface(command)

        command[command.index("--build-number") + 1] = "2144"
        failed = run_and_surface(command, check=False)
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn("does not match", failed.stderr)

if __name__ == "__main__":
    unittest.main()
