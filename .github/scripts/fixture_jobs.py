"""Keep runner-owned fixtures alive until their exact workflow consumers finish."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent / "release"))
from bounded_process import run_streamed


def remaining() -> float:
    value = os.environ.get("RUN_DEADLINE_EPOCH", "")
    if not re.fullmatch(r"[0-9]+", value):
        raise ValueError("RUN_DEADLINE_EPOCH must be an integer epoch deadline")
    left = int(value) - time.time()
    if left <= 0:
        raise TimeoutError("fixture workflow deadline expired")
    return left


def identity() -> tuple[str, int, int]:
    repo = os.environ["GITHUB_REPOSITORY"]
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
        raise ValueError("invalid GitHub repository")
    run, attempt = int(os.environ["GITHUB_RUN_ID"]), int(os.environ["GITHUB_RUN_ATTEMPT"])
    if run <= 0 or attempt <= 0:
        raise ValueError("invalid workflow run/attempt")
    return repo, run, attempt


def api(endpoint: str, field: str) -> list[dict]:
    command = ["gh", "api", "--method", "GET", "--paginate", "--slurp", endpoint]
    try:
        result = subprocess.run(command, check=True, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=remaining())
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        for stream, data in ((sys.stdout, error.stdout), (sys.stderr, error.stderr)):
            if data:
                stream.buffer.write(data)
                stream.buffer.flush()
        raise
    # Preserve complete provider output, including scheduling diagnostics.
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(result.stderr)
    sys.stderr.buffer.flush()
    pages = json.loads(result.stdout)
    if not isinstance(pages, list) or any(not isinstance(page, dict)
            or not isinstance(page.get(field), list) for page in pages):
        raise ValueError(f"invalid GitHub {field} response")
    return [item for page in pages for item in page[field]]


def latest_jobs(jobs: list[dict], attempt: int) -> dict[str, dict]:
    latest = {}
    for job in jobs:
        if (not isinstance(job, dict) or not isinstance(job.get("name"), str)
                or type(job.get("id")) is not int
                or type(job.get("run_attempt")) is not int):
            raise ValueError("invalid workflow job identity")
        if job["run_attempt"] > attempt:
            raise ValueError("workflow advanced to another attempt")
        previous = latest.get(job["name"])
        if previous is None or (job["run_attempt"], job["id"]) > (previous["run_attempt"], previous["id"]):
            latest[job["name"]] = job
    return latest


def consumers_complete(jobs: dict[str, dict], names: list[str], attempt: int,
                       require_current: bool = False) -> tuple[bool, bool]:
    """Only successful earlier results can be retained by failed-job reruns."""
    complete, success = True, True
    for name in names:
        job = jobs.get(name)
        if job is None or job.get("status") != "completed":
            complete = False
        elif job["run_attempt"] < attempt and (require_current or job.get("conclusion") != "success"):
            complete = False
        elif job.get("conclusion") != "success":
            success = False
    return complete, success


def jobs_snapshot() -> dict[str, dict]:
    repo, run, attempt = identity()
    return latest_jobs(api(f"repos/{repo}/actions/runs/{run}/jobs?filter=all&per_page=100", "jobs"), attempt)


def pause(seconds: float = 10) -> None:
    time.sleep(min(seconds, remaining()))


def ready(key: str, producer: str, output: Path) -> None:
    repo, run, attempt = identity()
    expected = f"vpn-fixtures-{run}-{key}-attempt-{attempt}"
    if os.environ.get("FIXTURE_ARTIFACT_NAME", expected) != expected:
        raise ValueError("fixture artifact differs from this run, platform or attempt")
    while True:
        remaining()
        job = jobs_snapshot().get(producer)
        if job is not None and job["run_attempt"] == attempt and job["status"] == "completed":
            raise RuntimeError(f"fixture producer stopped before consumption: {job}")
        artifacts = api(f"repos/{repo}/actions/runs/{run}/artifacts?per_page=100", "artifacts")
        matches = [item for item in artifacts if item.get("name") == expected and item.get("expired") is False]
        if len(matches) > 1:
            raise ValueError("duplicate fixture readiness artifacts")
        if matches and job is not None and job["run_attempt"] == attempt and job["status"] == "in_progress":
            result = run_streamed(
                [sys.executable, str(Path(__file__).parent / "release" / "download_artifact.py"),
                 str(run), expected, str(output)], timeout_seconds=remaining())
            if result.returncode != 0:
                raise RuntimeError(f"fixture artifact download failed: {result.returncode}")
            text = (output / "deadline_epoch.txt").read_text().strip()
            if not re.fullmatch(r"[0-9]+", text):
                raise ValueError("invalid fixture lifetime")
            deadline = min(int(os.environ["RUN_DEADLINE_EPOCH"]), int(text))
            if deadline <= time.time():
                raise TimeoutError("selected fixture already expired")
            for name in ("profile.toml", "websocket-profile.toml", "trusttunnel-profile.toml"):
                path = output / name
                if not path.is_file() or path.stat().st_size == 0:
                    raise ValueError(f"missing fixture profile: {name}")
            with open(os.environ["GITHUB_ENV"], "a", encoding="utf-8") as stream:
                stream.write(f"RUN_DEADLINE_EPOCH={deadline}\n")
            return
        pause()


def start(root: Path, tools: Path, tests: Path) -> None:
    root.mkdir(parents=True, exist_ok=True)
    public = root / "public"
    private = root / "private"
    logs = root / "logs"
    logs.mkdir(exist_ok=True)
    environment = os.environ.copy()
    environment["DOBBYVPN_COMMAND_DEADLINE_MONOTONIC"] = str(time.monotonic() + remaining())
    environment["PYTHONPATH"] = str(tests.resolve())
    command = [sys.executable, "-u", "-m", "disposable_vpn_server.service",
               "--tools", str(tools.resolve()), "--output", str(public.resolve()),
               "--private-dir", str(private.resolve()), "--lifetime-seconds", "3600"]
    with (logs / "service.stdout.log").open("xb") as stdout, (logs / "service.stderr.log").open("xb") as stderr:
        process = subprocess.Popen(command, cwd=tests, env=environment, stdout=stdout,
                                   stderr=stderr, start_new_session=True)
    (root / "pid").write_text(str(process.pid), encoding="ascii")
    while not (public / "ready.json").exists():
        if process.poll() is not None:
            raise RuntimeError(f"fixture service exited before readiness: {process.returncode}")
        pause()
    if (public / "result.json").exists():
        raise RuntimeError("fixture service stopped during readiness")


def stop(root: Path) -> None:
    public = root / "public"
    if not (root / "pid").exists():
        return
    public.mkdir(exist_ok=True)
    (public / "stop").touch()
    # Recovery is bounded separately; it never authorizes more testing.
    pid = int((root / "pid").read_text())
    result = public / "result.json"
    if not (public / "ready.json").exists() and not result.exists():
        # Setup does not watch the ordinary stop file; interrupt it so its
        # existing finally block can collect output and stop child groups.
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    until = time.monotonic() + 45
    while not result.exists() and time.monotonic() < until:
        time.sleep(.2)
    if not result.exists():
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        until = time.monotonic() + 15
        while not result.exists() and time.monotonic() < until:
            time.sleep(.2)
    if not result.exists():
        try:
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        raise RuntimeError("fixture supervisor forced to stop; child-group cleanup and final output are unconfirmed")
    if json.loads(result.read_text())["exit_code"] != 0:
        raise RuntimeError(f"fixture service failed: {result.read_text()}")


def monitor(root: Path, names: list[str], freshness_jobs: list[str] | None = None) -> None:
    if not names or len(names) != len(set(names)) or any(not isinstance(n, str) or not n for n in names):
        raise ValueError("consumer_names must contain unique exact job names")
    _, _, attempt = identity()
    try:
        while True:
            if (root / "public" / "result.json").exists():
                raise RuntimeError("fixture service stopped while consumers were active")
            jobs = jobs_snapshot()
            require_current = any(jobs.get(name, {}).get("run_attempt") == attempt
                                  for name in (freshness_jobs or []))
            complete, success = consumers_complete(jobs, names, attempt, require_current)
            if complete:
                if not success:
                    raise RuntimeError("one or more fixture consumers failed; retain this host for failed-job recovery")
                return
            pause(30)
    finally:
        stop(root)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    consumer = commands.add_parser("ready")
    consumer.add_argument("--key", choices=("linux", "windows", "macos", "android"), required=True)
    consumer.add_argument("--producer-name", required=True)
    consumer.add_argument("--output", type=Path, required=True)
    producer = commands.add_parser("start")
    producer.add_argument("--root", type=Path, required=True)
    producer.add_argument("--tools", type=Path, required=True)
    producer.add_argument("--tests", type=Path, required=True)
    watch = commands.add_parser("monitor")
    watch.add_argument("--root", type=Path, required=True)
    watch.add_argument("--consumers", required=True)
    watch.add_argument("--freshness-jobs", default="[]")
    cleanup = commands.add_parser("stop")
    cleanup.add_argument("--root", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "ready":
        ready(args.key, args.producer_name, args.output)
    elif args.command == "start":
        start(args.root, args.tools, args.tests)
    elif args.command == "monitor":
        monitor(args.root, json.loads(args.consumers), json.loads(args.freshness_jobs))
    else:
        stop(args.root)


if __name__ == "__main__":
    main()
