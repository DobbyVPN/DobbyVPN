from __future__ import annotations

import copy
import datetime
import json
from pathlib import Path
import sys
import tempfile
import unittest


sys.path.insert(0, str(Path(__file__).parent))
from publication_policy import (  # noqa: E402
    DOBBYVPN_REPOSITORY,
    REQUIRED_RELEASE_ARTIFACTS,
    TORTURER_REPOSITORY,
    PublicationPolicyError,
    normalize_identity,
    publication_identity_marker,
    validate_qualification_artifacts,
    validate_release_artifacts,
    validate_release_jobs,
    validate_release_jobs_for_attempt,
    validate_release_run,
    validate_torturer_jobs,
    validate_torturer_run,
    _read_json,
    validate_identity,
    validate_publication_state,
)


DOBBY_SHA = "a" * 40
TORTURER_SHA = "b" * 40
RELEASE_ID = 1234
RELEASE_ATTEMPT = 2
TORTURER_ID = 2345
TORTURER_ATTEMPT = 3
RELEASE_WORKFLOW_ID = 77
TORTURER_WORKFLOW_ID = 88


def identity() -> dict[str, str]:
    return {
        "dobbyvpn_commit": DOBBY_SHA,
        "release_run_id": str(RELEASE_ID),
        "release_run_attempt": str(RELEASE_ATTEMPT),
        "torturer_commit": TORTURER_SHA,
        "torturer_run_id": str(TORTURER_ID),
        "torturer_run_attempt": str(TORTURER_ATTEMPT),
    }


def release_workflow() -> dict[str, object]:
    return {
        "id": RELEASE_WORKFLOW_ID,
        "name": "Release",
        "path": ".github/workflows/release.yml",
        "state": "active",
    }


def release_run() -> dict[str, object]:
    return {
        "id": RELEASE_ID,
        "run_attempt": RELEASE_ATTEMPT,
        "workflow_id": RELEASE_WORKFLOW_ID,
        "name": "Release",
        "path": ".github/workflows/release.yml",
        "event": "push",
        "head_branch": "main",
        "head_sha": DOBBY_SHA,
        "status": "completed",
        "conclusion": "success",
        "repository": {"full_name": DOBBYVPN_REPOSITORY},
        "head_repository": {"full_name": DOBBYVPN_REPOSITORY},
    }


def torturer_workflow() -> dict[str, object]:
    return {
        "id": TORTURER_WORKFLOW_ID,
        "name": "Trusted public Release qualification",
        "path": ".github/workflows/functional.yml",
        "state": "active",
    }


def torturer_run() -> dict[str, object]:
    return {
        "id": TORTURER_ID,
        "run_attempt": TORTURER_ATTEMPT,
        "workflow_id": TORTURER_WORKFLOW_ID,
        "name": "Trusted public Release qualification",
        "path": ".github/workflows/functional.yml",
        "event": "workflow_dispatch",
        "head_branch": "main",
        "head_sha": TORTURER_SHA,
        "status": "completed",
        "conclusion": "success",
        "repository": {"full_name": TORTURER_REPOSITORY},
        "head_repository": {"full_name": TORTURER_REPOSITORY},
    }


def release_listing() -> dict[str, object]:
    members = {
        "dobbyVPN-linux.deb": "dobbyVPN-linux.deb",
        "dobbyVPN-windows-amd64.msi": "dobbyVPN-windows-amd64.msi",
        "dobbyVPN-macos-amd64.pkg": "dobbyVPN-macos-amd64.pkg",
        "dobbyVPN-macos-aarch64.pkg": "dobbyVPN-macos-aarch64.pkg",
        "dobbyvpn-android-sign.apk": "DobbyVPN-v1.5.0-sign.apk",
        "dobbyvpn-android-unsign.apk": "DobbyVPN-v1.5.0-unsign.apk",
        "dobbyvpn-android-provenance": "DobbyVPN-v1.5.0-android-provenance.json",
        "dobbyvpn-android-test-companion.apk": "DobbyVPN-v1.5.0-android-test-companion.apk",
        "DobbyVPN.ipa": "DobbyVPN.ipa",
        "DobbyVPN.ipa.provenance": "DobbyVPN.ipa.provenance.json",
    }
    artifacts = []
    for artifact_id, name in enumerate(REQUIRED_RELEASE_ARTIFACTS, start=100):
        artifacts.append(
            {
                "id": artifact_id,
                "name": name,
                "expired": False,
                "size_in_bytes": artifact_id,
                "workflow_run": {
                    "id": RELEASE_ID,
                    "run_attempt": RELEASE_ATTEMPT,
                    "head_sha": DOBBY_SHA,
                },
                "created_at": "2026-01-01T00:30:00Z",
                "archive_download_url": (
                    f"https://api.github.com/repos/{DOBBYVPN_REPOSITORY}"
                    f"/actions/artifacts/{artifact_id}/zip"
                ),
                "member": members[name],
            }
        )
    return {"total_count": len(artifacts), "artifacts": artifacts}


def qualification_listing() -> dict[str, object]:
    artifacts = []
    artifact_id = 1000
    for platform in ("linux", "windows", "macos", "android"):
        lease = f"{artifact_id:032x}"
        names = (
            f"public-functional-evidence-{platform}-{TORTURER_ID}-{TORTURER_ATTEMPT}",
            f"render-complete-{lease}-{platform}",
            f"functional-raw-logs-{TORTURER_ID}-{TORTURER_ATTEMPT}-{platform}",
        )
        for name in names:
            artifacts.append(
                {
                    "id": artifact_id,
                    "name": name,
                    "expired": False,
                    "size_in_bytes": 1,
                    "workflow_run": {
                        "id": TORTURER_ID,
                        "run_attempt": TORTURER_ATTEMPT,
                        "head_sha": TORTURER_SHA,
                    },
                    "created_at": "2026-01-01T00:30:00Z",
                    "archive_download_url": (
                        f"https://api.github.com/repos/{TORTURER_REPOSITORY}"
                        f"/actions/artifacts/{artifact_id}/zip"
                    ),
                }
            )
            artifact_id += 1
    return {"total_count": len(artifacts), "artifacts": artifacts}


def release_jobs() -> dict[str, object]:
    return {
        "jobs": [
            {
                "name": "ios_build / ios_build",
                "run_attempt": RELEASE_ATTEMPT,
                "status": "completed",
                "conclusion": "success",
                "started_at": "2026-01-01T00:00:00Z",
                "completed_at": "2026-01-01T01:00:00Z",
                "steps": [
                    {"name": "Fastlane upload_testflight", "conclusion": "success"}
                ],
            }
        ]
    }


def torturer_jobs() -> dict[str, object]:
    return {
        "jobs": [
            {
                "name": f"{platform} / Public Release qualification / {platform}",
                "run_attempt": TORTURER_ATTEMPT,
                "status": "completed",
                "conclusion": "success",
                "started_at": "2026-01-01T00:00:00Z",
                "completed_at": "2026-01-01T01:00:00Z",
            }
            for platform in ("linux", "windows", "macos", "android")
        ]
    }


class HandoffTests(unittest.TestCase):
    def test_handoff_is_identity_only_and_strictly_bound(self) -> None:
        self.assertEqual(validate_identity(identity())["release_run_id"], RELEASE_ID)
        changed = identity()
        changed["torturer_run_attempt"] = "4"
        self.assertEqual(validate_identity(changed)["torturer_run_attempt"], 4)
        changed["signature"] = "not-accepted"
        with self.assertRaises(PublicationPolicyError):
            validate_identity(changed)

    def test_identity_rejects_extra_fields_and_noncanonical_numbers(self) -> None:
        changed = identity()
        changed["extra"] = "not-allowed"
        with self.assertRaises(PublicationPolicyError):
            normalize_identity(changed)
        changed = identity()
        changed["release_run_id"] = "01"
        with self.assertRaises(PublicationPolicyError):
            normalize_identity(changed)


class RunTests(unittest.TestCase):
    def test_accepts_exact_release_and_torturer_runs(self) -> None:
        self.assertEqual(
            validate_release_run(
                release_run(), release_workflow(), expected_run_id=RELEASE_ID,
                expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
            )["commit"],
            DOBBY_SHA,
        )
        self.assertEqual(
            validate_torturer_run(
                torturer_run(), torturer_workflow(), expected_run_id=TORTURER_ID,
                expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
            )["commit"],
            TORTURER_SHA,
        )
        validate_release_jobs_for_attempt(release_jobs(), expected_attempt=RELEASE_ATTEMPT)
        validate_release_jobs(release_jobs())
        validate_torturer_jobs(torturer_jobs(), expected_attempt=TORTURER_ATTEMPT)
        changed_jobs = copy.deepcopy(release_jobs())
        changed_jobs["jobs"][0]["run_attempt"] = RELEASE_ATTEMPT + 1  # type: ignore[index]
        with self.assertRaises(PublicationPolicyError):
            validate_release_jobs_for_attempt(changed_jobs, expected_attempt=RELEASE_ATTEMPT)

    def test_release_attempt_accepts_expected_skipped_jobs_but_not_failures(self) -> None:
        jobs = release_jobs()
        jobs["jobs"].append({
            "name": "pull-request-only check",
            "run_attempt": RELEASE_ATTEMPT,
            "status": "completed",
            "conclusion": "skipped",
        })
        self.assertEqual(
            len(validate_release_jobs_for_attempt(
                jobs, expected_attempt=RELEASE_ATTEMPT,
            )),
            1,
        )
        jobs["jobs"][-1]["conclusion"] = "failure"
        with self.assertRaises(PublicationPolicyError):
            validate_release_jobs_for_attempt(
                jobs, expected_attempt=RELEASE_ATTEMPT,
            )

    def test_torturer_jobs_ignore_other_reusable_jobs_with_platform_prefixes(self) -> None:
        jobs = torturer_jobs()
        jobs["jobs"].extend(
            {
                "name": f"{platform} / Dispatch isolated Render lease controller",
                "run_attempt": TORTURER_ATTEMPT,
                "status": "completed",
                "conclusion": "success",
                "started_at": "2026-01-01T00:00:00Z",
                "completed_at": "2026-01-01T01:00:00Z",
            }
            for platform in ("linux", "windows", "macos", "android")
        )
        validate_torturer_jobs(jobs, expected_attempt=TORTURER_ATTEMPT)

    def test_run_identity_or_state_mismatch_fails_closed(self) -> None:
        for key, value in {
            "id": RELEASE_ID + 1,
            "run_attempt": RELEASE_ATTEMPT + 1,
            "workflow_id": RELEASE_WORKFLOW_ID + 1,
            "event": "workflow_dispatch",
            "head_branch": "feature",
            "head_sha": "c" * 40,
            "status": "in_progress",
            "conclusion": "failure",
        }.items():
            changed = release_run()
            changed[key] = value
            with self.subTest(key=key), self.assertRaises(PublicationPolicyError):
                validate_release_run(
                    changed, release_workflow(), expected_run_id=RELEASE_ID,
                    expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
                )

        changed_workflow = release_workflow()
        changed_workflow["path"] = ".github/workflows/other.yml"
        with self.assertRaises(PublicationPolicyError):
            validate_release_run(
                release_run(), changed_workflow, expected_run_id=RELEASE_ID,
                expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
            )


class ArtifactTests(unittest.TestCase):
    def test_exact_attempt_requires_successful_job_intervals(self) -> None:
        with self.assertRaises(PublicationPolicyError):
            validate_release_artifacts(
                release_listing(), expected_run_id=RELEASE_ID,
                expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
            )
        with self.assertRaises(PublicationPolicyError):
            validate_qualification_artifacts(
                qualification_listing(), expected_run_id=TORTURER_ID,
                expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
            )
        changed = release_jobs()
        changed["jobs"][0]["status"] = "in_progress"  # type: ignore[index]
        with self.assertRaises(PublicationPolicyError):
            validate_release_jobs_for_attempt(changed, expected_attempt=RELEASE_ATTEMPT)

    def test_requires_all_exact_release_artifacts_and_attempt(self) -> None:
        # Artifact workflow_run records do not carry run_attempt in GitHub's
        # REST schema. An exact attempt therefore requires the selected
        # attempt's successful job intervals.
        intervals = [
            (
                datetime.datetime.fromisoformat("2026-01-01T00:00:00+00:00"),
                datetime.datetime.fromisoformat("2026-01-01T01:00:00+00:00"),
            )
        ]
        validate_release_artifacts(
            release_listing(), expected_run_id=RELEASE_ID,
            expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
            job_intervals=intervals,
        )
        changed = copy.deepcopy(release_listing())
        changed["artifacts"][0]["workflow_run"]["id"] = RELEASE_ID + 1  # type: ignore[index]
        with self.assertRaises(PublicationPolicyError):
            validate_release_artifacts(
                changed, expected_run_id=RELEASE_ID,
                expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
                job_intervals=intervals,
            )
        changed = copy.deepcopy(release_listing())
        changed["artifacts"][0]["created_at"] = "2025-01-01T00:30:00Z"  # type: ignore[index]
        with self.assertRaises(PublicationPolicyError):
            validate_release_artifacts(
                changed, expected_run_id=RELEASE_ID,
                expected_attempt=RELEASE_ATTEMPT, expected_commit=DOBBY_SHA,
                job_intervals=intervals,
            )

    def test_requires_each_torturer_result_and_cleanup_marker(self) -> None:
        selected = validate_qualification_artifacts(
            qualification_listing(), expected_run_id=TORTURER_ID,
            expected_commit=TORTURER_SHA,
        )
        self.assertEqual(set(selected), {"linux", "windows", "macos", "android"})
        intervals = validate_torturer_jobs(torturer_jobs(), expected_attempt=TORTURER_ATTEMPT)
        validate_qualification_artifacts(
            qualification_listing(), expected_run_id=TORTURER_ID,
            expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
            job_intervals=intervals,
        )
        changed = copy.deepcopy(qualification_listing())
        changed["artifacts"] = [  # type: ignore[index]
            item for item in changed["artifacts"]  # type: ignore[index]
            if "render-complete" not in item["name"]  # type: ignore[index]
        ]
        changed["total_count"] = len(changed["artifacts"])  # type: ignore[arg-type]
        with self.assertRaises(PublicationPolicyError):
            validate_qualification_artifacts(
                changed, expected_run_id=TORTURER_ID,
                expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
                job_intervals=intervals,
            )

    def test_qualification_rerun_ignores_prior_attempt_artifacts(self) -> None:
        listing = qualification_listing()
        prior = copy.deepcopy(listing["artifacts"])
        for offset, item in enumerate(prior, start=5000):
            item["id"] = offset
            item["created_at"] = "2025-12-31T00:30:00Z"
            item["workflow_run"]["run_attempt"] = TORTURER_ATTEMPT - 1
            item["archive_download_url"] = (
                f"https://api.github.com/repos/{TORTURER_REPOSITORY}"
                f"/actions/artifacts/{offset}/zip"
            )
            if item["name"].startswith("public-functional-evidence-"):
                item["name"] = item["name"].rsplit("-", 1)[0] + f"-{TORTURER_ATTEMPT - 1}"
            elif item["name"].startswith("render-complete-"):
                parts = item["name"].split("-")
                parts[-2] = f"{offset:032x}"
                item["name"] = "-".join(parts)
            elif item["name"].startswith("functional-raw-logs-"):
                parts = item["name"].split("-")
                parts[-2] = str(TORTURER_ATTEMPT - 1)
                item["name"] = "-".join(parts)
        listing["artifacts"] = [*prior, *listing["artifacts"]]
        listing["total_count"] = len(listing["artifacts"])
        intervals = validate_torturer_jobs(
            torturer_jobs(), expected_attempt=TORTURER_ATTEMPT
        )

        selected = validate_qualification_artifacts(
            listing, expected_run_id=TORTURER_ID,
            expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
            job_intervals=intervals,
        )

        self.assertEqual(
            {record["id"] for kinds in selected.values() for record in kinds.values()},
            set(range(1000, 1012)),
        )
        changed = copy.deepcopy(qualification_listing())
        changed["artifacts"][2]["name"] = (  # type: ignore[index]
            f"functional-raw-logs-{TORTURER_ID}-{TORTURER_ATTEMPT}-windows"
        )
        with self.assertRaises(PublicationPolicyError):
            validate_qualification_artifacts(
                changed, expected_run_id=TORTURER_ID,
                expected_attempt=TORTURER_ATTEMPT, expected_commit=TORTURER_SHA,
                job_intervals=intervals,
            )


class JsonReaderTests(unittest.TestCase):
    def test_json_reader_rejects_duplicate_and_nonfinite_values(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "unsafe.json"
            path.write_text('{"value": 1, "value": 2}', encoding="utf-8")
            with self.assertRaises(PublicationPolicyError):
                _read_json(path)
            path.write_text('{"value": NaN}', encoding="utf-8")
            with self.assertRaises(PublicationPolicyError):
                _read_json(path)


class PublicationStateTests(unittest.TestCase):
    def test_existing_exact_state_is_retryable(self) -> None:
        validate_publication_state(
            tag_commit=DOBBY_SHA,
            release={
                "tag_name": "v1.5.0",
                "draft": False,
                "prerelease": False,
                "body": publication_identity_marker(identity()),
                "target_commitish": "main",
            },
            expected_commit=DOBBY_SHA,
            expected_tag="v1.5.0",
            expected_identity=identity(),
        )

    def test_conflict_or_draft_is_rejected_before_mutation(self) -> None:
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit="c" * 40, release=None,
                expected_commit=DOBBY_SHA, expected_tag="v1.5.0",
            )
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit=DOBBY_SHA,
                release={"tag_name": "v1.5.0", "draft": True},
                expected_commit=DOBBY_SHA, expected_tag="v1.5.0",
            )

    def test_exact_owned_draft_is_retryable_but_identity_conflict_is_not(self) -> None:
        owned = {
            "tag_name": "v1.5.0",
            "draft": True,
            "prerelease": False,
            "body": publication_identity_marker(identity()),
        }
        validate_publication_state(
            tag_commit=DOBBY_SHA,
            release=owned,
            expected_commit=DOBBY_SHA,
            expected_tag="v1.5.0",
            expected_identity=identity(),
        )
        changed = copy.deepcopy(owned)
        changed["body"] = publication_identity_marker({**identity(), "torturer_run_attempt": "4"})
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit=DOBBY_SHA,
                release=changed,
                expected_commit=DOBBY_SHA,
                expected_tag="v1.5.0",
                expected_identity=identity(),
            )

    def test_published_release_must_carry_the_same_stable_identity(self) -> None:
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit=DOBBY_SHA,
                release={
                    "tag_name": "v1.5.0",
                    "draft": False,
                    "prerelease": False,
                    "body": publication_identity_marker(identity()),
                },
                expected_commit=DOBBY_SHA,
                expected_tag="v1.5.0",
            )
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit=DOBBY_SHA,
                release={
                    "tag_name": "v1.5.0", "draft": False,
                    "prerelease": False, "body": "other",
                },
                expected_commit=DOBBY_SHA,
                expected_tag="v1.5.0",
                expected_identity=identity(),
            )

    def test_prerelease_is_never_accepted_as_completed_publication(self) -> None:
        with self.assertRaises(PublicationPolicyError):
            validate_publication_state(
                tag_commit=DOBBY_SHA,
                release={
                    "tag_name": "v1.5.0",
                    "draft": False,
                    "prerelease": True,
                    "body": publication_identity_marker(identity()),
                },
                expected_commit=DOBBY_SHA,
                expected_tag="v1.5.0",
                expected_identity=identity(),
            )


if __name__ == "__main__":
    unittest.main()
