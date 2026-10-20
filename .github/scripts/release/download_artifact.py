#!/usr/bin/env python3
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import zipfile


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


def download_artifact(run_id, name, destination, repository, expected_digest=None):
    endpoint = f"repos/{repository}/actions/runs/{run_id}/artifacts"
    command = [
        "gh", "api", "--method", "GET", "--paginate", "--slurp", endpoint,
        "-f", f"name={name}", "-F", "per_page=100",
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
    matches = [artifact for page in pages for artifact in page["artifacts"]
               if artifact["name"] == name and not artifact["expired"]]
    if len(matches) != 1:
        raise SystemExit(f"expected one unexpired artifact named {name!r} in run {run_id}; found {len(matches)}")

    artifact = matches[0]
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
