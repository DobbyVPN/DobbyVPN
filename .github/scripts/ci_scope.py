"""Choose the least expensive CI scope supported by a complete PR file list."""

from __future__ import annotations

import argparse
from collections.abc import Callable, Iterable
import json
from pathlib import PurePosixPath
import sys
from urllib.parse import urlencode
from urllib.request import Request, urlopen


PAGE_SIZE = 100
MAX_PULL_REQUEST_FILES = 3000
API_TIMEOUT_SECONDS = 15
MARKDOWN_SUFFIXES = {".md", ".markdown"}
WORKFLOW_SUFFIXES = {".yml", ".yaml"}


class ScopeDetectionError(RuntimeError):
    """The pull-request file list could not be proven complete."""


def _validate_request(repository: str, pull_request: int, token: str) -> None:
    parts = repository.split("/")
    if len(parts) != 2 or not all(parts) or any(".." in part for part in parts):
        raise ScopeDetectionError("repository name is invalid")
    if pull_request < 1 or not token:
        raise ScopeDetectionError("pull request number or token is missing")


def _api_json(
    url: str,
    token: str,
    description: str,
    *,
    opener: Callable | None = None,
) -> object:
    request = Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "DobbyVPN-CI-scope",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with (opener or urlopen)(request, timeout=API_TIMEOUT_SECONDS) as response:
            return json.loads(response.read())
    except Exception as error:
        raise ScopeDetectionError(f"could not read {description}: {error}") from error


def _pull_request_metadata(
    repository: str,
    pull_request: int,
    token: str,
    *,
    opener: Callable | None = None,
) -> tuple[str, str, int]:
    _validate_request(repository, pull_request, token)
    url = f"https://api.github.com/repos/{repository}/pulls/{pull_request}"
    payload = _api_json(url, token, "pull request metadata", opener=opener)
    if not isinstance(payload, dict):
        raise ScopeDetectionError("pull request metadata is missing")
    try:
        head_sha = payload["head"]["sha"]
        base_sha = payload["base"]["sha"]
        changed_files = payload["changed_files"]
    except (KeyError, TypeError) as error:
        raise ScopeDetectionError("pull request metadata is incomplete") from error
    if (
        not isinstance(head_sha, str)
        or not head_sha
        or not isinstance(base_sha, str)
        or not base_sha
        or not isinstance(changed_files, int)
        or isinstance(changed_files, bool)
        or changed_files < 0
    ):
        raise ScopeDetectionError("pull request metadata has invalid identities or count")
    return head_sha, base_sha, changed_files


def _pull_request_file_page(
    repository: str,
    pull_request: int,
    page_number: int,
    token: str,
    *,
    opener: Callable | None = None,
) -> list[dict[str, object]]:
    _validate_request(repository, pull_request, token)

    query = urlencode({"per_page": PAGE_SIZE, "page": page_number})
    url = (
        f"https://api.github.com/repos/{repository}/pulls/"
        f"{pull_request}/files?{query}"
    )
    payload = _api_json(
        url,
        token,
        f"pull request file-list page {page_number}",
        opener=opener,
    )

    if not isinstance(payload, list) or len(payload) > PAGE_SIZE:
        raise ScopeDetectionError(
            f"pull request file-list page {page_number} has an invalid shape"
        )
    for item in payload:
        if not isinstance(item, dict) or not isinstance(item.get("filename"), str):
            raise ScopeDetectionError(
                f"pull request file-list page {page_number} has an invalid file entry"
            )
        previous = item.get("previous_filename")
        if previous is not None and not isinstance(previous, str):
            raise ScopeDetectionError(
                f"pull request file-list page {page_number} has an invalid rename entry"
            )
    return payload


def pull_request_files(
    repository: str,
    pull_request: int,
    token: str,
    *,
    expected_head_sha: str,
    expected_base_sha: str,
    expected_changed_files: int,
    opener: Callable | None = None,
) -> list[dict[str, object]]:
    """Read a complete file list pinned to the pull-request event identities."""
    if (
        not expected_head_sha
        or not expected_base_sha
        or not isinstance(expected_changed_files, int)
        or isinstance(expected_changed_files, bool)
        or expected_changed_files < 0
    ):
        raise ScopeDetectionError("pull request event identities or file count are missing")
    before = _pull_request_metadata(
        repository, pull_request, token, opener=opener
    )
    expected = (expected_head_sha, expected_base_sha, expected_changed_files)
    if before != expected:
        raise ScopeDetectionError("pull request has advanced beyond the workflow event")

    files: list[dict[str, object]] = []
    page_number = 1
    while True:
        page = _pull_request_file_page(
            repository, pull_request, page_number, token, opener=opener
        )
        files.extend(page)
        if len(files) >= MAX_PULL_REQUEST_FILES:
            raise ScopeDetectionError(
                "pull request file list reached GitHub's 3000-file response cap"
            )
        if len(page) < PAGE_SIZE:
            break
        page_number += 1

    after = _pull_request_metadata(
        repository, pull_request, token, opener=opener
    )
    if after != before or after != expected:
        raise ScopeDetectionError("pull request changed while its file list was read")
    filenames = [item["filename"] for item in files]
    if len(set(filenames)) != len(filenames):
        raise ScopeDetectionError("pull request file list contains duplicate paths")
    if len(filenames) != expected_changed_files:
        raise ScopeDetectionError("pull request file count does not match its event metadata")
    return files


def _allowed_markdown(path: str) -> bool:
    candidate = PurePosixPath(path)
    return candidate.suffix.lower() in MARKDOWN_SUFFIXES


def _allowed_workflow_yaml(path: str) -> bool:
    parts = PurePosixPath(path).parts
    return (
        len(parts) == 3
        and parts[:2] == (".github", "workflows")
        and PurePosixPath(path).suffix.lower() in WORKFLOW_SUFFIXES
    )


def classify_paths(paths: Iterable[str]) -> tuple[str, str]:
    """Return light scope only when every changed path has an allowed type."""
    selected = tuple(paths)
    if not selected:
        return "full", "no-changed-files"
    kinds: set[str] = set()
    for path in selected:
        segments = path.split("/") if isinstance(path, str) else ()
        if (
            not isinstance(path, str)
            or not path
            or path.startswith("/")
            or "\\" in path
            or any(part in {"", ".", ".."} for part in segments)
        ):
            return "full", "unrecognized-path"
        if _allowed_markdown(path):
            kinds.add("markdown")
        elif _allowed_workflow_yaml(path):
            kinds.add("workflow-yaml")
        else:
            return "full", "non-documentation-or-workflow-path"
    if kinds == {"markdown"}:
        return "light", "markdown-only"
    if kinds == {"workflow-yaml"}:
        return "light", "workflow-yaml-only"
    return "light", "markdown-and-workflow-yaml-only"


def classify_pull_request_files(
    files: Iterable[dict[str, object]],
) -> tuple[str, str]:
    """Classify both names of renamed files and the path of deleted files."""
    paths: list[str] = []
    for item in files:
        filename = item.get("filename")
        if not isinstance(filename, str):
            return "full", "invalid-file-entry"
        paths.append(filename)
        previous_filename = item.get("previous_filename")
        if previous_filename is not None:
            if not isinstance(previous_filename, str):
                return "full", "invalid-rename-entry"
            paths.append(previous_filename)
    return classify_paths(paths)


def select_pull_request_scope(
    repository: str,
    pull_request: int,
    token: str,
    *,
    expected_head_sha: str,
    expected_base_sha: str,
    expected_changed_files: int,
    opener: Callable | None = None,
) -> tuple[str, str]:
    """Choose full CI whenever the API cannot establish the complete file set."""
    try:
        files = pull_request_files(
            repository,
            pull_request,
            token,
            expected_head_sha=expected_head_sha,
            expected_base_sha=expected_base_sha,
            expected_changed_files=expected_changed_files,
            opener=opener,
        )
    except ScopeDetectionError as error:
        print(f"CI scope detection is uncertain; selecting full CI: {error}", file=sys.stderr)
        return "full", "file-list-incomplete"
    return classify_pull_request_files(files)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--pull-request", required=True, type=int)
    parser.add_argument("--token", required=True)
    parser.add_argument("--expected-head-sha", required=True)
    parser.add_argument("--expected-base-sha", required=True)
    parser.add_argument("--expected-changed-files", required=True, type=int)
    args = parser.parse_args(argv)

    scope, reason = select_pull_request_scope(
        args.repository,
        args.pull_request,
        args.token,
        expected_head_sha=args.expected_head_sha,
        expected_base_sha=args.expected_base_sha,
        expected_changed_files=args.expected_changed_files,
    )
    print(f"scope={scope}")
    print(f"reason={reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
