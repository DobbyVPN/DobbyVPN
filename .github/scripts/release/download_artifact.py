#!/usr/bin/env python3
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

from artifact_attempts import ArtifactAttemptError, select_artifacts


def _forward(stream, payload, failure=None):
    if not payload:
        return
    try:
        binary = getattr(stream, "buffer", None)
        if binary is not None:
            binary.write(payload)
            binary.flush()
        else:
            stream.write(payload.decode("utf-8", errors="backslashreplace"))
            stream.flush()
    except Exception as error:
        if failure is None:
            raise
        failure.add_note(
            f"gh output forwarding failed: {type(error).__name__}: {error}"
        )


def _normalize_digest(value, label):
    if not isinstance(value, str):
        raise SystemExit(f"{label} is not a valid SHA-256 digest")
    digest = value.removeprefix("sha256:")
    if not re.fullmatch(r"[0-9a-fA-F]{64}", digest):
        raise SystemExit(f"{label} is not a valid SHA-256 digest")
    return digest.lower()


def _workflow_attempt(run_id, repository):
    try:
        result = subprocess.run(
            ["gh", "api", f"repos/{repository}/actions/runs/{run_id}"],
            check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
    except subprocess.CalledProcessError as error:
        _forward(sys.stdout, error.stdout or error.output or b"", failure=error)
        _forward(sys.stderr, error.stderr or b"", failure=error)
        raise
    _forward(sys.stdout, result.stdout or b"")
    _forward(sys.stderr, result.stderr or b"")
    run = json.loads(result.stdout)
    if (
        not isinstance(run, dict) or type(run.get("id")) is not int
        or str(run["id"]) != str(run_id)
        or type(run.get("run_attempt")) is not int or run["run_attempt"] < 1
    ):
        raise SystemExit(f"workflow run {run_id!r} has invalid attempt metadata")
    return run["run_attempt"]


def _artifact_record(artifacts, name, current_attempt):
    explicit = re.fullmatch(r".+-attempt-([0-9]+)", name)
    if explicit:
        attempt_text = explicit.group(1)
        attempt = int(attempt_text)
        if attempt < 1 or str(attempt) != attempt_text or attempt > current_attempt:
            raise SystemExit(f"explicit artifact {name!r} has an invalid or future attempt")
        matches = [item for item in artifacts if item.get("name") == name and item.get("expired") is False]
        if len(matches) != 1:
            raise SystemExit(f"expected one unexpired artifact named {name!r}; found {len(matches)}")
        return matches[0]
    try:
        return select_artifacts(artifacts, current_attempt, required_names=(name,))[name]
    except ArtifactAttemptError as error:
        raise SystemExit(str(error)) from error


def download_artifact(run_id, name, destination, repository, expected_digest=None):
    current_attempt = _workflow_attempt(run_id, repository)
    endpoint = f"repos/{repository}/actions/runs/{run_id}/artifacts?per_page=100"
    command = [
        "gh", "api", "--method", "GET", "--paginate", "--slurp", endpoint,
    ]
    try:
        metadata = subprocess.run(
            command,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except subprocess.CalledProcessError as error:
        _forward(sys.stdout, error.stdout or error.output or b"", failure=error)
        _forward(sys.stderr, error.stderr or b"", failure=error)
        raise
    _forward(sys.stdout, metadata.stdout or b"")
    _forward(sys.stderr, metadata.stderr or b"")
    pages = json.loads(metadata.stdout)
    if not isinstance(pages, list) or any(not isinstance(page, dict) or not isinstance(page.get("artifacts"), list) for page in pages):
        raise SystemExit("GitHub returned an invalid artifact list")
    artifacts = [artifact for page in pages for artifact in page["artifacts"]]
    artifact = _artifact_record(artifacts, name, current_attempt)
    if type(artifact.get("id")) is not int or artifact["id"] <= 0:
        raise SystemExit(f"artifact {name!r} has an invalid GitHub artifact ID")
    digest = artifact.get("digest") or ""
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-fA-F]{64}", digest):
        raise SystemExit(f"artifact {name!r} has no valid SHA-256 digest")
    expected_api_digest = digest.split(":", 1)[1].lower()
    if expected_digest is not None:
        expected_upload_digest = _normalize_digest(
            expected_digest, f"artifact {name!r} upload output digest",
        )
        if expected_upload_digest != expected_api_digest:
            raise SystemExit(
                f"artifact {name!r} digest does not match the upload-artifact output"
            )

    with tempfile.TemporaryFile() as archive:
        try:
            subprocess.run(
                ["gh", "api", f"repos/{repository}/actions/artifacts/{artifact['id']}/zip"],
                check=True,
                stdout=archive,
            )
        except subprocess.CalledProcessError as error:
            archive.flush()
            archive.seek(0)
            _forward(sys.stdout, archive.read(), failure=error)
            raise
        archive.flush()
        archive.seek(0)
        hasher = hashlib.sha256()
        for chunk in iter(lambda: archive.read(1024 * 1024), b""):
            hasher.update(chunk)
        actual_digest = hasher.hexdigest()
        if actual_digest.lower() != expected_api_digest:
            raise SystemExit(f"artifact {name!r} SHA-256 digest mismatch")
        archive.seek(0)
        destination_path = Path(destination)
        if destination_path.exists() and any(destination_path.iterdir()):
            raise SystemExit(f"refusing to overwrite existing artifact output: {destination}")
        os.makedirs(destination, exist_ok=True)
        with zipfile.ZipFile(archive) as zipped:
            zipped.extractall(destination)


def main():
    import argparse

    parser = argparse.ArgumentParser(
        description="Download one exact GitHub Actions artifact and verify its digest",
    )
    parser.add_argument("run_id")
    parser.add_argument("name")
    parser.add_argument("destination")
    parser.add_argument("--expected-digest")
    args = parser.parse_args()
    download_artifact(
        args.run_id, args.name, args.destination, os.environ["GITHUB_REPOSITORY"],
        expected_digest=args.expected_digest,
    )


if __name__ == "__main__":
    main()
