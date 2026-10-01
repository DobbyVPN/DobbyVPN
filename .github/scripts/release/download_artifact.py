#!/usr/bin/env python3
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import zipfile


def download_artifact(run_id, name, destination, repository):
    endpoint = f"repos/{repository}/actions/runs/{run_id}/artifacts"
    pages = json.loads(subprocess.run(
        ["gh", "api", "--method", "GET", "--paginate", "--slurp", endpoint,
         "-f", f"name={name}", "-F", "per_page=100"],
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    ).stdout)
    matches = [artifact for page in pages for artifact in page["artifacts"]
               if artifact["name"] == name and not artifact["expired"]]
    if len(matches) != 1:
        raise SystemExit(f"expected one unexpired artifact named {name!r} in run {run_id}; found {len(matches)}")

    artifact = matches[0]
    digest = artifact.get("digest") or ""
    if not re.fullmatch(r"sha256:[0-9a-fA-F]{64}", digest):
        raise SystemExit(f"artifact {name!r} has no valid SHA-256 digest")

    with tempfile.TemporaryFile() as archive:
        subprocess.run(
            ["gh", "api", f"repos/{repository}/actions/artifacts/{artifact['id']}/zip"],
            check=True,
            stdout=archive,
        )
        archive.flush()
        archive.seek(0)
        hasher = hashlib.sha256()
        for chunk in iter(lambda: archive.read(1024 * 1024), b""):
            hasher.update(chunk)
        actual_digest = hasher.hexdigest()
        if actual_digest.lower() != digest.split(":", 1)[1].lower():
            raise SystemExit(f"artifact {name!r} SHA-256 digest mismatch")
        archive.seek(0)
        os.makedirs(destination, exist_ok=True)
        with zipfile.ZipFile(archive) as zipped:
            zipped.extractall(destination)


def main():
    run_id, name, destination = sys.argv[1:]
    download_artifact(run_id, name, destination, os.environ["GITHUB_REPOSITORY"])


if __name__ == "__main__":
    main()
