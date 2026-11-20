"""Join one private qualification run and return only its verified public receipt."""
from __future__ import annotations

import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import zipfile

from test_manifest import ANDROID_PAIRING_FIELDS, REQUIRED_CHECKS, SHA256_RE, TORTURER_REPOSITORY

PUBLIC_CHECKS = {"resolve_build", "ios_analysis", "android_reproducibility", "fdroid_preflight"}
PRIVATE_CHECKS = REQUIRED_CHECKS - PUBLIC_CHECKS
WORKFLOW = "qualification.yml"


def api(path, *args):
    raw = subprocess.check_output(["gh", "api", *args, f"repos/{TORTURER_REPOSITORY}/{path}"])
    return json.loads(raw) if raw.strip() else None


def run_title(parent, build, source):
    return f"Qualify Test {parent} Build {build} at {source}"


def find_run(binding):
    title = run_title(binding["parent_run_id"], binding["build_run_id"], binding["source_sha"])
    pages = api(f"actions/workflows/{WORKFLOW}/runs?event=workflow_dispatch&branch=main&head_sha={binding['torturer_sha']}&per_page=100",
                "--paginate", "--slurp")
    matches = [run for page in pages for run in page["workflow_runs"] if run["display_title"] == title]
    if len(matches) > 1:
        raise ValueError("multiple private runs claim this Product Test")
    return matches[0] if matches else None


def verify_run(run, binding):
    if (run["event"] != "workflow_dispatch" or run["head_branch"] != "main"
            or run["head_sha"] != binding["torturer_sha"] or run["path"] != f".github/workflows/{WORKFLOW}"
            or run["display_title"] != run_title(binding["parent_run_id"], binding["build_run_id"], binding["source_sha"])
            or any(run[key]["full_name"] != TORTURER_REPOSITORY for key in ("repository", "head_repository"))
            or any(type(run[k]) is not int or run[k] < 1 for k in ("id", "run_attempt"))):
        raise ValueError("unexpected private workflow identity")


def public_receipt(record, binding, run):
    expected = {**binding, "run_id": run["id"], "run_attempt": run["run_attempt"]}
    if (not isinstance(record, dict) or set(record) != set(expected) | {"checks", "android_pairing"}
            or any(type(record[k]) is not type(v) or record[k] != v for k, v in expected.items())):
        raise ValueError("private coverage differs from the selected workflow or Build")
    if record["checks"] != dict.fromkeys(PRIVATE_CHECKS, "success"):
        raise ValueError("incomplete private qualification coverage")
    pairing = record["android_pairing"]
    if not isinstance(pairing, dict) or set(pairing) != ANDROID_PAIRING_FIELDS - {"build_run_id", "build_manifest_sha256"}:
        raise ValueError("unexpected private Android pairing fields")
    if (pairing["product_sha"] != binding["source_sha"] or pairing["torturer_sha"] != binding["torturer_sha"]
            or pairing["torturer_repository"] != TORTURER_REPOSITORY or pairing["torturer_tree_sha256"] != binding["torturer_tree_sha256"]
            or any(not isinstance(pairing[k], str) or not SHA256_RE.fullmatch(pairing[k])
                   for k in ("unsigned_product_apk_sha256", "test_companion_sha256"))):
        raise ValueError("private Android pairing differs from selected inputs")
    return record


def coverage(run):
    name = f"private-coverage-attempt-{run['run_attempt']}"
    pages = api(f"actions/runs/{run['id']}/artifacts?per_page=100", "--paginate", "--slurp")
    files = [a for p in pages for a in p["artifacts"] if a["name"] == name and not a["expired"]]
    if len(files) != 1 or files[0]["workflow_run"]["id"] != run["id"] or not 0 < files[0]["size_in_bytes"] <= 16384:
        raise ValueError("private coverage artifact missing or invalid")
    artifact = files[0]
    raw = subprocess.check_output(["gh", "api", f"repos/{TORTURER_REPOSITORY}/actions/artifacts/{artifact['id']}/zip"])
    if "sha256:" + hashlib.sha256(raw).hexdigest() != artifact["digest"]:
        raise ValueError("private coverage archive digest mismatch")
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        if archive.namelist() != ["coverage.json"] or archive.getinfo("coverage.json").file_size > 8192:
            raise ValueError("unexpected private coverage archive contents")
        return json.loads(archive.read("coverage.json"))


def environment_binding():
    return dict(parent_run_id=int(os.environ["GITHUB_RUN_ID"]), build_run_id=int(os.environ["BUILD_RUN_ID"]),
                   source_sha=os.environ["GITHUB_SHA"], torturer_sha=os.environ["TORTURER_SHA"],
                   build_manifest_sha256=os.environ["MANIFEST_SHA256"], torturer_tree_sha256=os.environ["TESTS_TREE"])

def main():
    binding = environment_binding()
    if sys.argv[1:] == ["inspect"]:
        run = find_run(binding)
        if run is not None:
            verify_run(run, binding)
            run = {key: run[key] for key in ("id", "run_attempt", "status", "conclusion")}
        print(json.dumps(run))
        return
    if sys.argv[1:]:
        raise ValueError("unexpected private qualification command")
    if os.environ["GITHUB_REF"] != "refs/heads/main" or os.environ["GITHUB_REPOSITORY"] != "DobbyVPN/DobbyVPN":
        raise ValueError("private qualification requires Product main")
    run = find_run(binding)
    if run is None:
        if api("git/ref/heads/main")["object"]["sha"] != binding["torturer_sha"]:
            raise ValueError("reviewed Torturer SHA differs from trusted private main")
        fields = {key: str(value) for key, value in binding.items() if key != "torturer_sha"}
        subprocess.run(["gh", "workflow", "run", WORKFLOW, "--repo", TORTURER_REPOSITORY, "--ref", "main", "--json"],
                       input=json.dumps(fields), text=True, check=True)
        deadline = time.monotonic() + 300
        while run is None:
            run = find_run(binding)
            if run is None:
                if time.monotonic() >= deadline:
                    raise TimeoutError("private dispatch was not identified")
                time.sleep(5)
    verify_run(run, binding)
    run_id, attempt = run["id"], run["run_attempt"]
    print(f"Private qualification run {run_id} attempt {attempt}", flush=True)
    while run["status"] != "completed":
        time.sleep(15)
        run = api(f"actions/runs/{run_id}")
        verify_run(run, binding)
        if run["id"] != run_id or run["run_attempt"] != attempt:
            raise ValueError("private qualification attempt changed during observation")
    if run["conclusion"] != "success":
        raise ValueError(f"private qualification {run_id}/{attempt} requires review")
    receipt = public_receipt(coverage(run), binding, run)
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        print("receipt=" + json.dumps(receipt, separators=(",", ":")), file=output)


if __name__ == "__main__":
    main()
